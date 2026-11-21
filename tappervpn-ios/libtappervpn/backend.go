// Copyright (c) TapperVPN contributors
// SPDX-License-Identifier: BSD-3-Clause

package libtappervpn

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime/debug"

	"github.com/tailscale/wireguard-go/tun"
	_ "tappervpn.com/feature/condregister"
	"tappervpn.com/hostinfo"
	"tappervpn.com/ipn"
	"tappervpn.com/ipn/ipnlocal"
	"tappervpn.com/ipn/store"
	"tappervpn.com/net/dns"
	"tappervpn.com/net/netmon"
	"tappervpn.com/net/tsdial"
	"tappervpn.com/paths"
	"tappervpn.com/tsd"
	"tappervpn.com/types/logger"
	"tappervpn.com/types/logid"
	"tappervpn.com/wgengine"
	"tappervpn.com/wgengine/netstack"
	"tappervpn.com/wgengine/router"
)

// mtu matches the Android app; the tunnel carries WireGuard over UDP, so
// 1280 is always safe.
const mtu = 1280

// memoryLimit keeps the Go heap well under iOS's Network Extension limit
// (about 50 MB for the whole process, which is killed when it goes over).
const memoryLimit = 30 << 20

type backend struct {
	host    Host
	lb      *ipnlocal.LocalBackend
	engine  wgengine.Engine
	tunFile *os.File
	logFile *os.File

	lastSettings string // last JSON handed to iOS
}

// setupProcess is done once per Start: environment, logs and memory limits.
func setupProcess(dataDir string) {
	os.MkdirAll(dataDir, 0o700)
	for k, v := range map[string]string{
		"HOME":            dataDir,
		"XDG_CACHE_HOME":  filepath.Join(dataDir, "cache"),
		"XDG_CONFIG_HOME": filepath.Join(dataDir, "config"),
		"TMPDIR":          filepath.Join(dataDir, "tmp"),
	} {
		os.MkdirAll(v, 0o700)
		os.Setenv(k, v)
	}
	debug.SetMemoryLimit(memoryLimit)
	debug.SetGCPercent(50)
	paths.AppSharedDir.Store(dataDir)
}

func newBackend(dataDir string, host Host) (_ *backend, err error) {
	b := &backend{host: host}
	defer func() {
		if err != nil {
			b.close()
		}
	}()
	b.logFile = openLog(filepath.Join(dataDir, "tappervpn.log"))
	logf := logger.Logf(log.Printf)
	log.Printf("TapperVPN for iOS starting, data in %s", dataDir)

	hostinfo.SetOSVersion(host.SystemVersion())
	hostinfo.SetDeviceModel(host.ModelName())
	hostinfo.SetPackage("ios")
	hostinfo.SetHostnameFn(func() (string, error) { return host.DeviceName(), nil })

	fd, name, err := findTunFD()
	if err != nil {
		return nil, err
	}
	log.Printf("tunnel interface %s (fd %d)", name, fd)
	b.tunFile = os.NewFile(uintptr(fd), "/dev/"+name)
	dev, err := tun.CreateTUNFromFile(b.tunFile, mtu)
	if err != nil {
		return nil, fmt.Errorf("opening the tunnel: %w", err)
	}

	sys := tsd.NewSystem()
	st, err := store.New(logf, filepath.Join(dataDir, "tappervpnd.state"))
	if err != nil {
		dev.Close()
		return nil, fmt.Errorf("state: %w", err)
	}
	sys.Set(st)

	netMon, err := netmon.New(sys.Bus.Get(), logf)
	if err != nil {
		dev.Close()
		return nil, fmt.Errorf("netmon: %w", err)
	}

	vf := &vpnFacade{apply: b.applySettings}
	dialer := new(tsdial.Dialer)
	engine, err := wgengine.NewUserspaceEngine(logf, wgengine.Config{
		Tun:            dev,
		Router:         vf,
		DNS:            vf,
		ReconfigureVPN: vf.reconfigure,
		Dialer:         dialer,
		SetSubsystem:   sys.Set,
		NetMon:         netMon,
		HealthTracker:  sys.HealthTracker.Get(),
		Metrics:        sys.UserMetricsRegistry(),
		EventBus:       sys.Bus.Get(),
	})
	if err != nil {
		return nil, fmt.Errorf("engine: %w", err)
	}
	b.engine = engine
	sys.Set(engine)

	ns, err := netstack.Create(logf, sys.Tun.Get(), engine, sys.MagicSock.Get(), dialer, sys.DNSManager.Get(), sys.ProxyMapper())
	if err != nil {
		return nil, fmt.Errorf("netstack: %w", err)
	}
	sys.Set(ns)
	ns.ProcessLocalIPs = false // iOS delivers traffic for our own address
	ns.ProcessSubnets = false  // iOS can't be an exit node or subnet router
	sys.NetstackRouter.Set(false)

	logID, err := loadLogID(dataDir)
	if err != nil {
		return nil, err
	}
	lb, err := ipnlocal.NewLocalBackend(logf, logID.Public(), sys, 0)
	if err != nil {
		return nil, fmt.Errorf("backend: %w", err)
	}
	b.lb = lb
	if err := ns.Start(lb); err != nil {
		return nil, fmt.Errorf("netstack start: %w", err)
	}
	if err := lb.Start(ipn.Options{}); err != nil {
		return nil, fmt.Errorf("backend start: %w", err)
	}
	// The tunnel is up because the user (or iOS, on demand) turned it on.
	if lb.State() != ipn.NeedsLogin {
		lb.EditPrefs(&ipn.MaskedPrefs{Prefs: ipn.Prefs{WantRunning: true}, WantRunningSet: true})
	}
	return b, nil
}

func (b *backend) close() {
	if b.lb != nil {
		b.lb.Shutdown()
	} else if b.engine != nil {
		b.engine.Close()
	}
	if b.logFile != nil {
		log.SetOutput(os.Stderr)
		b.logFile.Close()
	}
}

// loadLogID keeps one log ID per install. Logs are not uploaded anywhere;
// the backend needs an ID regardless.
func loadLogID(dataDir string) (logid.PrivateID, error) {
	path := filepath.Join(dataDir, "logid")
	var id logid.PrivateID
	if txt, err := os.ReadFile(path); err == nil && id.UnmarshalText(txt) == nil {
		return id, nil
	}
	id, err := logid.NewPrivateID()
	if err != nil {
		return id, err
	}
	txt, _ := id.MarshalText()
	os.WriteFile(path, txt, 0o600)
	return id, nil
}

func (b *backend) applySettings(rcfg *router.Config, dcfg *dns.OSConfig) error {
	s := makeSettings(rcfg, dcfg)
	js, err := s.marshal()
	if err != nil {
		return err
	}
	if js == b.lastSettings {
		return nil // reapplying would briefly interrupt the tunnel
	}
	log.Printf("applying network settings: %d addresses, %d+%d routes", len(s.IPv4Addresses)+len(s.IPv6Addresses), len(s.IPv4Routes), len(s.IPv6Routes))
	if err := b.host.ApplyNetworkSettings(js); err != nil {
		return err
	}
	b.lastSettings = js
	return nil
}
