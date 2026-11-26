package hscontrol

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testWGKey(b byte) string {
	k := make([]byte, 32)
	for i := range k {
		k[i] = b
	}

	return base64.StdEncoding.EncodeToString(k)
}

func newTestWGBridge(t *testing.T) (*Headscale, *wgBridge) {
	t.Helper()

	dir := t.TempDir()
	b := newWGBridge(filepath.Join(dir, "db.sqlite"))

	if err := os.MkdirAll(b.dir, 0o750); err != nil {
		t.Fatal(err)
	}

	return &Headscale{wgBridge: b}, b
}

func wgDo(t *testing.T, h http.HandlerFunc, method, target, body string) (int, map[string]any) {
	t.Helper()

	req := httptest.NewRequest(method, target, strings.NewReader(body))
	rec := httptest.NewRecorder()
	h(rec, req)

	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)

	return rec.Code, out
}

func TestWGBridgeDisabledWithoutDir(t *testing.T) {
	h := &Headscale{wgBridge: newWGBridge(filepath.Join(t.TempDir(), "db.sqlite"))}

	code, _ := wgDo(t, h.WGPairHandler, http.MethodPost, "/tappervpn/wg/pair", `{}`)
	if code != http.StatusNotFound {
		t.Fatalf("pair without bridge dir: got %d, want 404", code)
	}

	h = &Headscale{}

	code, _ = wgDo(t, h.WGConfigHandler, http.MethodGet, "/tappervpn/wg/config?key=x", "")
	if code != http.StatusNotFound {
		t.Fatalf("config with nil bridge: got %d, want 404", code)
	}
}

func TestWGBridgePairApproveFetch(t *testing.T) {
	h, b := newTestWGBridge(t)
	key := testWGKey(7)
	q := "/tappervpn/wg/config?key=" + url.QueryEscape(key)

	code, out := wgDo(t, h.WGConfigHandler, http.MethodGet, q, "")
	if code != http.StatusNotFound || out["status"] != "unknown" {
		t.Fatalf("before pairing: %d %v", code, out)
	}

	code, out = wgDo(t, h.WGPairHandler, http.MethodPost, "/tappervpn/wg/pair",
		`{"publicKey":"`+key+`","name":"Living room <TV>"}`)
	if code != http.StatusOK || out["status"] != "pending" {
		t.Fatalf("pair: %d %v", code, out)
	}

	pairCode, _ := out["code"].(string)
	if len(pairCode) != 6 {
		t.Fatalf("code %q", pairCode)
	}

	// Asking again returns the same code rather than a new one.
	_, again := wgDo(t, h.WGPairHandler, http.MethodPost, "/tappervpn/wg/pair",
		`{"publicKey":"`+key+`"}`)
	if again["code"] != pairCode {
		t.Fatalf("second pair gave %v, want %s", again["code"], pairCode)
	}

	data, err := os.ReadFile(filepath.Join(b.dir, "pending", pairCode+".json"))
	if err != nil {
		t.Fatal(err)
	}

	var r wgPairRequest
	if err := json.Unmarshal(data, &r); err != nil || r.Name != "Living room TV" || r.PublicKey != key {
		t.Fatalf("pending file: %s (%v)", data, err)
	}

	code, out = wgDo(t, h.WGConfigHandler, http.MethodGet, q, "")
	if code != http.StatusAccepted || out["status"] != "pending" {
		t.Fatalf("waiting: %d %v", code, out)
	}

	// What wg-bridge.sh approve does.
	raw, _ := base64.StdEncoding.DecodeString(key)
	peers := filepath.Join(b.dir, "peers")
	os.MkdirAll(peers, 0o750)

	cfg := `{"name":"Living room TV","address":"10.77.0.2/32"}`
	if err := os.WriteFile(filepath.Join(peers, hex.EncodeToString(raw)+".json"), []byte(cfg), 0o640); err != nil {
		t.Fatal(err)
	}

	os.Remove(filepath.Join(b.dir, "pending", pairCode+".json"))

	code, out = wgDo(t, h.WGConfigHandler, http.MethodGet, q, "")
	if code != http.StatusOK || out["status"] != "approved" {
		t.Fatalf("approved: %d %v", code, out)
	}

	if c, _ := out["config"].(map[string]any); c["address"] != "10.77.0.2/32" {
		t.Fatalf("config %v", out["config"])
	}

	// Pairing an approved key doesn't make a new code.
	_, out = wgDo(t, h.WGPairHandler, http.MethodPost, "/tappervpn/wg/pair", `{"publicKey":"`+key+`"}`)
	if out["status"] != "approved" || out["code"] != nil {
		t.Fatalf("re-pair of approved key: %v", out)
	}
}

func TestWGBridgeRejectsBadKeysAndLimitsPending(t *testing.T) {
	h, b := newTestWGBridge(t)

	for _, k := range []string{"", "abc", base64.StdEncoding.EncodeToString(make([]byte, 31)), "../../etc"} {
		code, _ := wgDo(t, h.WGPairHandler, http.MethodPost, "/tappervpn/wg/pair", `{"publicKey":"`+k+`"}`)
		if code != http.StatusBadRequest {
			t.Errorf("pair key %q: got %d, want 400", k, code)
		}

		code, _ = wgDo(t, h.WGConfigHandler, http.MethodGet, "/tappervpn/wg/config?key="+url.QueryEscape(k), "")
		if code != http.StatusBadRequest {
			t.Errorf("config key %q: got %d, want 400", k, code)
		}
	}

	for i := range wgPairMaxPending {
		if _, err := b.pair(testWGKey(byte(i+1)), ""); err != nil {
			t.Fatal(err)
		}
	}

	code, _ := wgDo(t, h.WGPairHandler, http.MethodPost, "/tappervpn/wg/pair",
		`{"publicKey":"`+testWGKey(200)+`"}`)
	if code != http.StatusTooManyRequests {
		t.Fatalf("over the limit: got %d, want 429", code)
	}

	// Once they expire there is room again.
	b.now = func() time.Time { return time.Now().Add(wgPairTTL + time.Minute) }

	code, _ = wgDo(t, h.WGPairHandler, http.MethodPost, "/tappervpn/wg/pair",
		`{"publicKey":"`+testWGKey(200)+`"}`)
	if code != http.StatusOK {
		t.Fatalf("after expiry: got %d, want 200", code)
	}

	reqs, _ := b.pending()
	if len(reqs) != 1 {
		t.Fatalf("expired requests not cleaned up: %d left", len(reqs))
	}
}
