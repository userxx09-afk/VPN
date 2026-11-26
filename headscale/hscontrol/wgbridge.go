package hscontrol

// TapperVPN: pairing for plain WireGuard devices (Fire TV on Vega OS).
//
// Vega OS only offers apps a system WireGuard tunnel, so a Vega TV can't run
// the mesh client. Instead it connects with plain WireGuard to a bridge on
// the server (server/wg-bridge.sh), and its traffic goes on through the mesh.
//
// Pairing, without typing keys on a TV remote:
//
//  1. The TV makes its own WireGuard key pair (the private key never leaves
//     it) and POSTs its public key to /tappervpn/wg/pair. It gets a 6-digit
//     code and shows it on screen.
//  2. The admin runs `sudo wg-bridge approve <code>` on the server. That
//     script (root) adds the peer to the WireGuard interface and writes the
//     TV's settings to <db dir>/wg-bridge/peers/<key hex>.json.
//  3. The TV polls GET /tappervpn/wg/config?key=<public key> and gets them.
//
// Headscale itself only reads and writes files under <db dir>/wg-bridge;
// it never touches the network configuration. Nothing served here is secret:
// the settings are the server's public key, the endpoint and the TV's
// address. The bridge is off (404) until wg-bridge.sh install creates the
// directory.

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"
)

const (
	wgPairTTL        = 15 * time.Minute
	wgPairMaxPending = 20
	wgPairMaxName    = 64
)

// wgPairRequest is a TV waiting for approval: <dir>/pending/<code>.json.
type wgPairRequest struct {
	Code      string    `json:"code"`
	PublicKey string    `json:"publicKey"`
	Name      string    `json:"name"`
	Created   time.Time `json:"created"`
}

type wgBridge struct {
	dir string
	mu  sync.Mutex
	now func() time.Time
}

func newWGBridge(dbPath string) *wgBridge {
	if dbPath == "" {
		return nil
	}

	return &wgBridge{dir: filepath.Join(filepath.Dir(dbPath), "wg-bridge"), now: time.Now}
}

func (b *wgBridge) enabled() bool {
	if b == nil {
		return false
	}

	st, err := os.Stat(b.dir)

	return err == nil && st.IsDir()
}

// parseWGKey checks a base64 WireGuard public key and returns it in hex,
// which is safe to use as a file name.
func parseWGKey(s string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil || len(raw) != 32 {
		return "", errors.New("not a WireGuard public key")
	}

	return hex.EncodeToString(raw), nil
}

func cleanWGName(s string) string {
	var out strings.Builder

	for _, r := range strings.TrimSpace(s) {
		if out.Len() >= wgPairMaxName {
			break
		}

		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == ' ' || r == '-' || r == '_' || r == '.' {
			out.WriteRune(r)
		}
	}

	if out.Len() == 0 {
		return "Fire TV"
	}

	return out.String()
}

func writeJSONFile(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o640); err != nil {
		return err
	}

	return os.Rename(tmp, path)
}

// pending returns the unexpired requests and deletes the expired ones.
func (b *wgBridge) pending() ([]wgPairRequest, error) {
	dir := filepath.Join(b.dir, "pending")

	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	var out []wgPairRequest

	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}

		p := filepath.Join(dir, e.Name())

		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}

		var r wgPairRequest
		if json.Unmarshal(data, &r) != nil || b.now().Sub(r.Created) > wgPairTTL {
			os.Remove(p)

			continue
		}

		out = append(out, r)
	}

	return out, nil
}

func (b *wgBridge) newCode(taken map[string]bool) (string, error) {
	for range 50 {
		n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
		if err != nil {
			return "", err
		}

		c := fmt.Sprintf("%06d", n.Int64())
		if !taken[c] {
			return c, nil
		}
	}

	return "", errors.New("no free pairing code")
}

// pair records a request and returns its code. A key that is already
// waiting gets its existing code back.
func (b *wgBridge) pair(publicKey, name string) (wgPairRequest, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if _, err := parseWGKey(publicKey); err != nil {
		return wgPairRequest{}, err
	}

	reqs, err := b.pending()
	if err != nil {
		return wgPairRequest{}, err
	}

	taken := map[string]bool{}

	for _, r := range reqs {
		if r.PublicKey == publicKey {
			return r, nil
		}

		taken[r.Code] = true
	}

	if len(reqs) >= wgPairMaxPending {
		return wgPairRequest{}, errWGBusy
	}

	code, err := b.newCode(taken)
	if err != nil {
		return wgPairRequest{}, err
	}

	r := wgPairRequest{Code: code, PublicKey: publicKey, Name: cleanWGName(name), Created: b.now().UTC()}

	dir := filepath.Join(b.dir, "pending")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return wgPairRequest{}, err
	}

	return r, writeJSONFile(filepath.Join(dir, code+".json"), r)
}

var errWGBusy = errors.New("too many devices waiting for approval")

// config returns the settings written by wg-bridge.sh for an approved key,
// or nil while it is still waiting.
func (b *wgBridge) config(publicKey string) (json.RawMessage, error) {
	h, err := parseWGKey(publicKey)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(filepath.Join(b.dir, "peers", h+".json"))
	if os.IsNotExist(err) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	if !json.Valid(data) {
		return nil, errors.New("bad peer file")
	}

	return data, nil
}

func (b *wgBridge) waiting(publicKey string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	reqs, _ := b.pending()
	for _, r := range reqs {
		if r.PublicKey == publicKey {
			return true
		}
	}

	return false
}

func wgReply(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// WGPairHandler is POST /tappervpn/wg/pair {"publicKey": "...", "name": "..."}.
func (h *Headscale) WGPairHandler(w http.ResponseWriter, req *http.Request) {
	b := h.wgBridge
	if !b.enabled() {
		http.NotFound(w, req)

		return
	}

	var body struct {
		PublicKey string `json:"publicKey"`
		Name      string `json:"name"`
	}

	if err := json.NewDecoder(io.LimitReader(req.Body, 4096)).Decode(&body); err != nil {
		wgReply(w, http.StatusBadRequest, map[string]string{"error": "bad request"})

		return
	}

	// Already approved (the TV lost track, e.g. after a restart): no new code.
	if cfg, err := b.config(body.PublicKey); err == nil && cfg != nil {
		wgReply(w, http.StatusOK, map[string]any{"status": "approved"})

		return
	}

	r, err := b.pair(body.PublicKey, body.Name)

	switch {
	case errors.Is(err, errWGBusy):
		wgReply(w, http.StatusTooManyRequests, map[string]string{"error": err.Error()})
	case err != nil && strings.Contains(err.Error(), "public key"):
		wgReply(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case err != nil:
		wgReply(w, http.StatusInternalServerError, map[string]string{"error": "couldn't save the request"})
	default:
		left := wgPairTTL - b.now().Sub(r.Created)
		wgReply(w, http.StatusOK, map[string]any{
			"status":    "pending",
			"code":      r.Code,
			"expiresIn": int(left.Seconds()),
		})
	}
}

// WGConfigHandler is GET /tappervpn/wg/config?key=<public key>.
// 200 {"status":"approved","config":{...}}, 202 {"status":"pending"},
// 404 {"status":"unknown"} (not approved and no request waiting, or expired).
func (h *Headscale) WGConfigHandler(w http.ResponseWriter, req *http.Request) {
	b := h.wgBridge
	if !b.enabled() {
		http.NotFound(w, req)

		return
	}

	key := req.URL.Query().Get("key")

	cfg, err := b.config(key)
	if err != nil {
		wgReply(w, http.StatusBadRequest, map[string]string{"error": err.Error()})

		return
	}

	if cfg != nil {
		wgReply(w, http.StatusOK, map[string]any{"status": "approved", "config": cfg})

		return
	}

	if b.waiting(key) {
		wgReply(w, http.StatusAccepted, map[string]string{"status": "pending"})

		return
	}

	wgReply(w, http.StatusNotFound, map[string]string{"status": "unknown"})
}
