// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

package approuting

import (
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantErr string // substring; "" means success
	}{
		{
			name: "minimal",
			in:   `{"version":1}`,
		},
		{
			name: "full",
			in: `{"version":1,"default":"direct","unidentified":"block","rules":[
				{"name":"browsers","action":"gateway","apps":[
					{"windows":{"exe":"chrome.exe"},"macos":{"bundleId":"com.google.Chrome"},"android":{"package":"com.android.chrome"}},
					{"windows":{"path":"%ProgramFiles%\\Mozilla Firefox\\firefox.exe"}}
				]}
			]}`,
		},
		{
			name:    "wrong version",
			in:      `{"version":2}`,
			wantErr: "unsupported policy version 2",
		},
		{
			name:    "missing version",
			in:      `{}`,
			wantErr: "unsupported policy version 0",
		},
		{
			name:    "bad default",
			in:      `{"version":1,"default":"tunnel"}`,
			wantErr: `invalid default action "tunnel"`,
		},
		{
			name:    "bad unidentified",
			in:      `{"version":1,"unidentified":"allow"}`,
			wantErr: `invalid unidentified action "allow"`,
		},
		{
			name:    "unknown top-level field",
			in:      `{"version":1,"mode":"include"}`,
			wantErr: `unknown field "mode"`,
		},
		{
			name:    "unknown matcher field",
			in:      `{"version":1,"rules":[{"action":"block","apps":[{"windows":{"exe":"a.exe","signer":"X"}}]}]}`,
			wantErr: `unknown field "signer"`,
		},
		{
			name:    "rule missing action",
			in:      `{"version":1,"rules":[{"apps":[{"windows":{"exe":"a.exe"}}]}]}`,
			wantErr: `invalid action ""`,
		},
		{
			name:    "rule without apps",
			in:      `{"version":1,"rules":[{"name":"empty","action":"block","apps":[]}]}`,
			wantErr: `rule 0 ("empty"): no apps`,
		},
		{
			name:    "app without OS matcher",
			in:      `{"version":1,"rules":[{"action":"block","apps":[{}]}]}`,
			wantErr: "no OS-specific matcher",
		},
		{
			name:    "windows matcher empty",
			in:      `{"version":1,"rules":[{"action":"block","apps":[{"windows":{}}]}]}`,
			wantErr: "exe or path required",
		},
		{
			name:    "exe with path separator",
			in:      `{"version":1,"rules":[{"action":"block","apps":[{"windows":{"exe":"C:\\x\\a.exe"}}]}]}`,
			wantErr: "must be a file name",
		},
		{
			name:    "macos empty bundle",
			in:      `{"version":1,"rules":[{"action":"block","apps":[{"macos":{"bundleId":""}}]}]}`,
			wantErr: "bundleId required",
		},
		{
			name:    "android empty package",
			in:      `{"version":1,"rules":[{"action":"block","apps":[{"android":{}}]}]}`,
			wantErr: "package required",
		},
		{
			name:    "trailing data",
			in:      `{"version":1} {"version":1}`,
			wantErr: "trailing data",
		},
		{
			name:    "not json",
			in:      `version: 1`,
			wantErr: "decoding policy",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.in))
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tt.wantErr != "" && err == nil:
				t.Fatalf("got nil error, want %q", tt.wantErr)
			case tt.wantErr != "" && !strings.Contains(err.Error(), tt.wantErr):
				t.Fatalf("error = %q, want substring %q", err, tt.wantErr)
			}
		})
	}
}

var testEnv = map[string]string{
	"ProgramFiles": `C:\Program Files`,
	"LocalAppData": `C:\Users\alice\AppData\Local`,
}

func testLookupEnv(k string) (string, bool) {
	v, ok := testEnv[k]
	return v, ok
}

const testPolicy = `{
	"version": 1,
	"default": "direct",
	"unidentified": "block",
	"rules": [
		{"name": "no-telemetry", "action": "block", "apps": [
			{"windows": {"exe": "CompatTelRunner.exe"}}
		]},
		{"name": "browsers", "action": "gateway", "apps": [
			{"windows": {"exe": "chrome.exe"},
			 "macos":   {"bundleId": "com.google.Chrome"},
			 "android": {"package": "com.android.chrome"}},
			{"windows": {"path": "%ProgramFiles%\\Mozilla Firefox\\firefox.exe"}}
		]},
		{"name": "user-apps", "action": "gateway", "apps": [
			{"windows": {"path": "%LocalAppData%\\Programs\\*\\*.exe"}}
		]},
		{"name": "specific-chrome", "action": "block", "apps": [
			{"windows": {"exe": "chrome.exe", "path": "D:\\portable\\chrome.exe"}}
		]}
	]
}`

func TestDecide(t *testing.T) {
	p, err := Parse([]byte(testPolicy))
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		action Action
		rule   string
	}
	tests := []struct {
		name string
		goos string
		app  *App
		want result
	}{
		{"unidentified", "windows", nil, result{ActionBlock, ""}},
		{"no rule matches", "windows", &App{Path: `C:\Windows\notepad.exe`}, result{ActionDirect, ""}},
		{"empty path", "windows", &App{}, result{ActionDirect, ""}},
		{"exe match", "windows", &App{Path: `C:\Program Files\Google\Chrome\Application\chrome.exe`}, result{ActionGateway, "browsers"}},
		{"exe case-insensitive", "windows", &App{Path: `C:\Program Files\Google\Chrome\Application\CHROME.EXE`}, result{ActionGateway, "browsers"}},
		{"exe must be whole name", "windows", &App{Path: `C:\x\notchrome.exe`}, result{ActionDirect, ""}},
		{"first rule wins", "windows", &App{Path: `C:\Windows\System32\CompatTelRunner.exe`}, result{ActionBlock, "no-telemetry"}},
		{"later rule shadowed", "windows", &App{Path: `D:\portable\chrome.exe`}, result{ActionGateway, "browsers"}},
		{"env path", "windows", &App{Path: `C:\Program Files\Mozilla Firefox\firefox.exe`}, result{ActionGateway, "browsers"}},
		{"env path case-insensitive", "windows", &App{Path: `c:\program files\mozilla firefox\FIREFOX.exe`}, result{ActionGateway, "browsers"}},
		{"forward slashes", "windows", &App{Path: `C:/Program Files/Mozilla Firefox/firefox.exe`}, result{ActionGateway, "browsers"}},
		{"long path prefix", "windows", &App{Path: `\\?\C:\Program Files\Mozilla Firefox\firefox.exe`}, result{ActionGateway, "browsers"}},
		{"path is not prefix match", "windows", &App{Path: `C:\Program Files\Mozilla Firefox\firefox.exe.bak`}, result{ActionDirect, ""}},
		{"glob", "windows", &App{Path: `C:\Users\alice\AppData\Local\Programs\Signal\Signal.exe`}, result{ActionGateway, "user-apps"}},
		{"glob star stops at separator", "windows", &App{Path: `C:\Users\alice\AppData\Local\Programs\a\b\c.exe`}, result{ActionDirect, ""}},
		{"glob needs suffix", "windows", &App{Path: `C:\Users\alice\AppData\Local\Programs\Signal\Signal.dll`}, result{ActionDirect, ""}},
		{"macos match", "darwin", &App{BundleID: "com.google.Chrome"}, result{ActionGateway, "browsers"}},
		{"macos no match", "darwin", &App{BundleID: "com.apple.Safari"}, result{ActionDirect, ""}},
		{"macos ignores windows path", "darwin", &App{Path: `C:\x\chrome.exe`}, result{ActionDirect, ""}},
		{"android match", "android", &App{Package: "com.android.chrome"}, result{ActionGateway, "browsers"}},
		{"android bundle id is not package", "android", &App{BundleID: "com.android.chrome"}, result{ActionDirect, ""}},
		{"unknown OS uses default", "plan9", &App{Path: `C:\x\chrome.exe`}, result{ActionDirect, ""}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := p.Compile(CompileOptions{GOOS: tt.goos, LookupEnv: testLookupEnv})
			if err != nil {
				t.Fatal(err)
			}
			d := m.Decide(tt.app)
			action, rule := d.Action, d.Rule
			if got := (result{action, rule}); got != tt.want {
				t.Errorf("Decide = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestCompileDefaults(t *testing.T) {
	tests := []struct {
		name             string
		in               string
		wantDefault      Action
		wantUnidentified Action
	}{
		{"both empty", `{"version":1}`, ActionGateway, ActionGateway},
		{"unidentified follows default", `{"version":1,"default":"direct"}`, ActionDirect, ActionDirect},
		{"both set", `{"version":1,"default":"direct","unidentified":"block"}`, ActionDirect, ActionBlock},
		{"only unidentified", `{"version":1,"unidentified":"block"}`, ActionGateway, ActionBlock},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := Parse([]byte(tt.in))
			if err != nil {
				t.Fatal(err)
			}
			m, err := p.Compile(CompileOptions{GOOS: "windows", LookupEnv: testLookupEnv})
			if err != nil {
				t.Fatal(err)
			}
			if got := m.Default(); got != tt.wantDefault {
				t.Errorf("Default = %q, want %q", got, tt.wantDefault)
			}
			if got := m.Decide(nil).Action; got != tt.wantUnidentified {
				t.Errorf("Decide(nil) = %q, want %q", got, tt.wantUnidentified)
			}
		})
	}
}

func TestCompileErrors(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		goos    string
		wantErr string
	}{
		{"unset variable", `%NoSuchVar%\a.exe`, "windows", "%NoSuchVar% is not set"},
		{"unterminated variable", `%ProgramFiles\a.exe`, "windows", "unterminated"},
		{"other OS ignores bad windows path", `%NoSuchVar%\a.exe`, "darwin", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Policy{Version: 1, Rules: []Rule{{
				Action: ActionBlock,
				Apps:   []AppMatcher{{Windows: &WindowsMatch{Path: tt.path}}},
			}}}
			_, err := p.Compile(CompileOptions{GOOS: tt.goos, LookupEnv: testLookupEnv})
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tt.wantErr != "" && err == nil:
				t.Fatalf("got nil error, want %q", tt.wantErr)
			case tt.wantErr != "" && !strings.Contains(err.Error(), tt.wantErr):
				t.Fatalf("error = %q, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestExpandWindowsEnv(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{`C:\plain.exe`, `C:\plain.exe`},
		{`%ProgramFiles%\a.exe`, `C:\Program Files\a.exe`},
		{`%ProgramFiles%\%LocalAppData%`, `C:\Program Files\C:\Users\alice\AppData\Local`},
		{`100%%\a.exe`, `100%\a.exe`},
	}
	for _, tt := range tests {
		got, err := expandWindowsEnv(tt.in, testLookupEnv)
		if err != nil {
			t.Errorf("expandWindowsEnv(%q): %v", tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("expandWindowsEnv(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestGlobQuotesMeta(t *testing.T) {
	re, err := globToRegexp(`C:\a+b (x86)\[v1].exe`)
	if err != nil {
		t.Fatal(err)
	}
	if !re.MatchString(`C:\a+b (x86)\[v1].exe`) {
		t.Error("literal path with regexp metacharacters did not match itself")
	}
	if re.MatchString(`C:\aab (x86)\v.exe`) {
		t.Error("metacharacters were not quoted")
	}
}

func TestMerge(t *testing.T) {
	a := []byte(`{"version":1,"default":"direct","rules":[{"name":"a","action":"block","apps":[{"windows":{"exe":"a.exe"}}]}]}`)
	b := []byte(`{"version":1,"default":"gateway","unidentified":"block","rules":[
		{"name":"a-again","action":"gateway","apps":[{"windows":{"exe":"a.exe"}}]},
		{"name":"b","action":"gateway","apps":[{"windows":{"exe":"b.exe"}}]}]}`)

	if p, err := Merge(nil); p != nil || err != nil {
		t.Errorf("Merge(nil) = %v, %v; want nil, nil", p, err)
	}

	p, err := Merge([][]byte{a, b})
	if err != nil {
		t.Fatal(err)
	}
	if p.Default != ActionDirect {
		t.Errorf("Default = %q, want first document's %q", p.Default, ActionDirect)
	}
	if p.Unidentified != ActionBlock {
		t.Errorf("Unidentified = %q, want first non-empty %q", p.Unidentified, ActionBlock)
	}
	m, err := p.Compile(CompileOptions{GOOS: "windows", LookupEnv: testLookupEnv})
	if err != nil {
		t.Fatal(err)
	}
	if d := m.Decide(&App{Path: `C:\a.exe`}); d.Action != ActionBlock || d.Rule != "a" {
		t.Errorf("a.exe: got %q (rule %q), want block from first document", d.Action, d.Rule)
	}
	if got := m.Decide(&App{Path: `C:\b.exe`}).Action; got != ActionGateway {
		t.Errorf("b.exe: got %q, want gateway", got)
	}

	if _, err := Merge([][]byte{a, []byte(`{"version":9}`)}); err == nil || !strings.Contains(err.Error(), "document 1") {
		t.Errorf("Merge with bad document: err = %v, want mention of document 1", err)
	}
}
