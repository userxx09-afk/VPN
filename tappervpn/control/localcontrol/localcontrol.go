// Copyright (c) TapperVPN contributors
// SPDX-License-Identifier: BSD-3-Clause

// Package localcontrol finds a TapperVPN control server on the local network.
//
// Devices are set up with one logical control URL, [PublicURL]. Before each
// connection to it, the client asks the local network (mDNS, service
// [Service]) whether a control server is there. A server that answers is
// only used if it holds the control key [ServerKey]; otherwise the client
// goes to PublicURL as usual. Because the local server and the public name
// are the same control server (same key, same database), switching between
// them never signs a device out.
package localcontrol

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/dns/dnsmessage"
	"tappervpn.com/net/netmon"
	"tappervpn.com/net/netns"
	"tappervpn.com/tailcfg"
	"tappervpn.com/types/logger"
)

// PublicURL is the control URL that triggers local discovery. Clients set up
// with any other URL behave as before. Override at build time with
// -ldflags "-X tappervpn.com/control/localcontrol.PublicURL=...".
var PublicURL = "https://vp.tapper.co"

// ServerKey is the ts2021 public key ("mkey:...") of our control server(s).
// A server found on the local network is used only if it presents this key;
// the Noise handshake then proves it holds the matching private key. Every
// standby control server must use the same key (copy noise_private.key).
// Empty disables discovery.
var ServerKey = "mkey:4b226296a345e70c868bf9ea94fd21ca3be9c84b4d50e5648e00bb7bb8314b36"

// Service is the DNS-SD service type the control servers advertise.
const Service = "_tappervpn._tcp.local."

const (
	defaultPort  = 8080
	queryWait    = 700 * time.Millisecond // how long to listen for mDNS answers
	verifyWait   = 2 * time.Second        // per-candidate /key check
	resultTTL    = 2 * time.Minute        // how long a result is reused on the same network
	mdnsAddrPort = "224.0.0.251:5353"
)

// Applies reports whether serverURL is the logical URL that uses discovery.
func Applies(serverURL string) bool {
	return PublicURL != "" && ServerKey != "" &&
		strings.TrimRight(serverURL, "/") == strings.TrimRight(PublicURL, "/")
}

// Finder looks for a local control server and remembers the answer for the
// current network.
type Finder struct {
	logf   logger.Logf
	netMon *netmon.Monitor // may be nil

	mu      sync.Mutex
	netKey  string    // network the result belongs to
	at      time.Time // when it was found
	url     string    // "" = none found
	pending chan struct{}
}

// NewFinder returns a Finder. netMon may be nil.
func NewFinder(logf logger.Logf, netMon *netmon.Monitor) *Finder {
	return &Finder{logf: logger.WithPrefix(logf, "localcontrol: "), netMon: netMon}
}

// Find returns the base URL ("http://10.0.0.156:8080") of a verified local
// control server, or "" if there is none. It answers from its memory when the
// network hasn't changed recently, and otherwise takes up to about a second.
func (f *Finder) Find(ctx context.Context) string {
	if f == nil {
		return ""
	}
	key := f.networkKey()
	f.mu.Lock()
	if f.netKey == key && time.Since(f.at) < resultTTL {
		u := f.url
		f.mu.Unlock()
		return u
	}
	if p := f.pending; p != nil { // another caller is already looking
		f.mu.Unlock()
		select {
		case <-p:
		case <-ctx.Done():
			return ""
		}
		f.mu.Lock()
		u := f.url
		f.mu.Unlock()
		return u
	}
	p := make(chan struct{})
	f.pending = p
	f.mu.Unlock()

	u := f.search(ctx)

	f.mu.Lock()
	f.netKey, f.at, f.url, f.pending = key, time.Now(), u, nil
	f.mu.Unlock()
	close(p)
	return u
}

// Forget drops the remembered result, for example after the local server
// failed to answer, so the next Find searches again.
func (f *Finder) Forget() {
	if f == nil {
		return
	}
	f.mu.Lock()
	f.at = time.Time{}
	f.mu.Unlock()
}

// networkKey identifies the network we're on, so a result found at home
// isn't reused on another network.
func (f *Finder) networkKey() string {
	if f.netMon == nil {
		return ""
	}
	st := f.netMon.InterfaceState()
	if st == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(st.DefaultRouteInterface)
	for _, pfx := range st.InterfaceIPs[st.DefaultRouteInterface] {
		b.WriteByte(' ')
		b.WriteString(pfx.String())
	}
	return b.String()
}

func (f *Finder) search(ctx context.Context) string {
	cands, err := f.query(ctx)
	if err != nil {
		f.logf("search: %v", err)
		return ""
	}
	for _, c := range cands {
		if err := f.verify(ctx, c); err != nil {
			f.logf("ignoring %s: %v", c, err)
			continue
		}
		f.logf("using local control server %s", c)
		return c
	}
	if len(cands) == 0 {
		f.logf("no local control server; using %s", PublicURL)
	}
	return ""
}

// query sends one DNS-SD PTR question for [Service] from an ordinary UDP
// port. Responders answer such "legacy unicast" queries straight back to
// the asking port (RFC 6762 section 6.7), so no multicast listening (and no
// Android multicast lock) is needed.
func (f *Finder) query(ctx context.Context) ([]string, error) {
	name := dnsmessage.MustNewName(Service)
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{})
	b.EnableCompression()
	if err := b.StartQuestions(); err != nil {
		return nil, err
	}
	if err := b.Question(dnsmessage.Question{Name: name, Type: dnsmessage.TypePTR, Class: dnsmessage.ClassINET}); err != nil {
		return nil, err
	}
	msg, err := b.Finish()
	if err != nil {
		return nil, err
	}

	lc := new(net.ListenConfig)
	if f.netMon != nil {
		lc = netns.Listener(f.logf, f.netMon)
	}
	pc, err := lc.ListenPacket(ctx, "udp4", ":0")
	if err != nil {
		return nil, err
	}
	defer pc.Close()
	dst, err := net.ResolveUDPAddr("udp4", mdnsAddrPort)
	if err != nil {
		return nil, err
	}
	if _, err := pc.WriteTo(msg, dst); err != nil {
		return nil, fmt.Errorf("send: %w", err)
	}

	deadline := time.Now().Add(queryWait)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	pc.SetReadDeadline(deadline)

	var out []string
	seen := map[string]bool{}
	buf := make([]byte, 9000)
	for {
		n, from, err := pc.ReadFrom(buf)
		if err != nil {
			break // deadline
		}
		src, _ := from.(*net.UDPAddr)
		for _, c := range parseAnswer(buf[:n], src) {
			if !seen[c] {
				seen[c] = true
				out = append(out, c)
			}
		}
		if len(out) > 0 {
			// Got one; give other servers a moment to answer, then stop.
			if soon := time.Now().Add(100 * time.Millisecond); soon.Before(deadline) {
				deadline = soon
				pc.SetReadDeadline(deadline)
			}
		}
	}
	return out, nil
}

// parseAnswer returns the server URLs in one mDNS response.
func parseAnswer(pkt []byte, src *net.UDPAddr) []string {
	var p dnsmessage.Parser
	if _, err := p.Start(pkt); err != nil {
		return nil
	}
	if err := p.SkipAllQuestions(); err != nil {
		return nil
	}
	var all []dnsmessage.Resource
	for _, section := range []func() ([]dnsmessage.Resource, error){p.AllAnswers, p.AllAuthorities, p.AllAdditionals} {
		rs, err := section()
		if err != nil {
			break
		}
		all = append(all, rs...)
	}

	isOurs := false
	instances := map[string]bool{}
	for _, r := range all {
		if ptr, ok := r.Body.(*dnsmessage.PTRResource); ok && strings.EqualFold(r.Header.Name.String(), Service) {
			isOurs = true
			instances[strings.ToLower(ptr.PTR.String())] = true
		}
	}
	if !isOurs {
		return nil
	}

	type target struct {
		host string
		port uint16
	}
	var targets []target
	addrs := map[string][]netip.Addr{}
	for _, r := range all {
		switch body := r.Body.(type) {
		case *dnsmessage.SRVResource:
			if instances[strings.ToLower(r.Header.Name.String())] {
				targets = append(targets, target{strings.ToLower(body.Target.String()), body.Port})
			}
		case *dnsmessage.AResource:
			n := strings.ToLower(r.Header.Name.String())
			addrs[n] = append(addrs[n], netip.AddrFrom4(body.A))
		}
	}
	srcAddr := netip.Addr{}
	if src != nil {
		srcAddr, _ = netip.AddrFromSlice(src.IP)
		srcAddr = srcAddr.Unmap()
	}
	if len(targets) == 0 {
		targets = []target{{"", defaultPort}}
	}

	var out []string
	for _, t := range targets {
		port := t.port
		if port == 0 {
			port = defaultPort
		}
		ips := addrs[t.host]
		if len(ips) == 0 && srcAddr.Is4() {
			ips = []netip.Addr{srcAddr}
		}
		for _, ip := range ips {
			if !ip.IsPrivate() && !ip.IsLinkLocalUnicast() {
				continue // only ever a server on the local network
			}
			out = append(out, fmt.Sprintf("http://%s", netip.AddrPortFrom(ip, port)))
		}
	}
	return out
}

// verify checks that the server at base presents [ServerKey].
func (f *Finder) verify(ctx context.Context, base string) error {
	ctx, cancel := context.WithTimeout(ctx, verifyWait)
	defer cancel()
	dial := new(net.Dialer).DialContext
	if f.netMon != nil {
		dial = netns.NewDialer(f.logf, f.netMon).DialContext
	}
	hc := &http.Client{Transport: &http.Transport{
		DialContext:       dial,
		DisableKeepAlives: true,
	}}
	req, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s/key?v=%d", base, tailcfg.CurrentCapabilityVersion), nil)
	if err != nil {
		return err
	}
	res, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("/key: %s", res.Status)
	}
	var keys tailcfg.OverTLSPublicKeyResponse
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<16)).Decode(&keys); err != nil {
		return fmt.Errorf("/key: %w", err)
	}
	if got := keys.PublicKey.String(); got != ServerKey {
		return fmt.Errorf("wrong control key %s", keys.PublicKey.ShortString())
	}
	return nil
}
