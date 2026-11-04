// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

package approuting

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// CapName is the node capability under which the control server delivers
// the app routing policy. Each value is one JSON-encoded [Policy].
const CapName = "meshwall.dev/cap/app-routing"

// Action is what happens to traffic from an app that would otherwise
// go to the exit node.
type Action string

const (
	// ActionGateway sends the traffic through the mesh to the exit node.
	ActionGateway Action = "gateway"
	// ActionDirect sends the traffic out the local network, bypassing the
	// exit node.
	ActionDirect Action = "direct"
	// ActionBlock drops the traffic.
	ActionBlock Action = "block"
)

func (a Action) valid() bool {
	switch a {
	case ActionGateway, ActionDirect, ActionBlock:
		return true
	}
	return false
}

// Policy is the per-node app routing policy document.
type Policy struct {
	// Version must be 1.
	Version int `json:"version"`

	// Default is the action for apps that match no rule.
	// If empty, it is ActionGateway, which is tappervpnd's behavior
	// without app routing.
	Default Action `json:"default,omitempty"`

	// Unidentified is the action for traffic whose owning app can't be
	// determined. If empty, it is the same as Default.
	Unidentified Action `json:"unidentified,omitempty"`

	// GatewayUnavailable is the action for traffic whose rule names a
	// gateway that can't be used right now (offline, not advertising a
	// gateway route, or an IPv6 destination, which named gateways don't
	// support). If empty, it is ActionBlock, so traffic never silently
	// takes a different path than the policy asked for.
	GatewayUnavailable Action `json:"gatewayUnavailable,omitempty"`

	// Rules are evaluated in order; the first rule with a matching app wins.
	Rules []Rule `json:"rules,omitempty"`
}

// Rule applies Action to any of Apps.
type Rule struct {
	Name   string `json:"name,omitempty"`
	Action Action `json:"action"`

	// Gateway optionally names the node that ActionGateway traffic goes
	// through, instead of the selected exit node. The node must advertise
	// a 4via6 route for 0.0.0.0/0 (see docs/per-app-routing.md). Names
	// are matched case-insensitively against the node's MagicDNS name
	// (without the domain) and its hostname. Only valid with ActionGateway.
	Gateway string `json:"gateway,omitempty"`

	Apps []AppMatcher `json:"apps"`
}

// Decision is the result of matching an app against a policy.
type Decision struct {
	Action  Action
	Gateway string // named gateway for ActionGateway; "" means the exit node
	Rule    string // name of the matching rule, or ""
}

// AppMatcher identifies one app, with a separate identifier per OS.
// A client only considers the field for its own OS; a matcher with no
// field for the client's OS never matches there.
type AppMatcher struct {
	Windows *WindowsMatch `json:"windows,omitempty"`
	MacOS   *MacOSMatch   `json:"macos,omitempty"`
	Android *AndroidMatch `json:"android,omitempty"`
}

// WindowsMatch matches a Windows process by its image. All non-empty
// fields must match.
type WindowsMatch struct {
	// Exe is the image file name, such as "chrome.exe". Case-insensitive.
	Exe string `json:"exe,omitempty"`

	// Path is the full image path. Case-insensitive. Environment variables
	// written as %NAME% are expanded, '*' matches any run of characters
	// other than '\', and '?' matches one such character.
	Path string `json:"path,omitempty"`
}

// MacOSMatch matches a macOS app.
type MacOSMatch struct {
	BundleID string `json:"bundleId"`
}

// AndroidMatch matches an Android app.
type AndroidMatch struct {
	Package string `json:"package"`
}

// App describes the process that owns a flow. Only the fields relevant to
// the current OS are set.
type App struct {
	Path     string // Windows: full image path
	BundleID string // macOS
	Package  string // Android

	// Self is set when the flow belongs to tappervpnd itself (for example
	// STUN or DERP traffic over a family the physical interface lacks).
	// Such flows are never subject to app routing: re-originating them
	// would feed them straight back into the tunnel.
	Self bool
}

// Parse decodes and validates a policy document. Unknown fields are
// rejected: silently ignoring a matcher field this client doesn't know
// about would make that matcher match more apps than the author intended.
func Parse(data []byte) (*Policy, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var p Policy
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("approuting: decoding policy: %w", err)
	}
	if dec.More() {
		return nil, errors.New("approuting: trailing data after policy")
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return &p, nil
}

// Validate reports whether p is well-formed.
func (p *Policy) Validate() error {
	if p.Version != 1 {
		return fmt.Errorf("approuting: unsupported policy version %d", p.Version)
	}
	if p.Default != "" && !p.Default.valid() {
		return fmt.Errorf("approuting: invalid default action %q", p.Default)
	}
	if p.Unidentified != "" && !p.Unidentified.valid() {
		return fmt.Errorf("approuting: invalid unidentified action %q", p.Unidentified)
	}
	if p.GatewayUnavailable != "" && !p.GatewayUnavailable.valid() {
		return fmt.Errorf("approuting: invalid gatewayUnavailable action %q", p.GatewayUnavailable)
	}
	for i, r := range p.Rules {
		if err := r.validate(); err != nil {
			return fmt.Errorf("approuting: rule %d (%q): %w", i, r.Name, err)
		}
	}
	return nil
}

func (r *Rule) validate() error {
	if !r.Action.valid() {
		return fmt.Errorf("invalid action %q", r.Action)
	}
	if r.Gateway != "" {
		if r.Action != ActionGateway {
			return fmt.Errorf("gateway %q set on a %q rule; only gateway rules can name one", r.Gateway, r.Action)
		}
		if strings.TrimSpace(r.Gateway) != r.Gateway || strings.ContainsAny(r.Gateway, " /\\") {
			return fmt.Errorf("invalid gateway name %q", r.Gateway)
		}
	}
	if len(r.Apps) == 0 {
		return errors.New("no apps")
	}
	for i, m := range r.Apps {
		if err := m.validate(); err != nil {
			return fmt.Errorf("app %d: %w", i, err)
		}
	}
	return nil
}

func (m *AppMatcher) validate() error {
	if m.Windows == nil && m.MacOS == nil && m.Android == nil {
		return errors.New("no OS-specific matcher")
	}
	if w := m.Windows; w != nil {
		if w.Exe == "" && w.Path == "" {
			return errors.New("windows: exe or path required")
		}
		if strings.ContainsAny(w.Exe, `\/`) {
			return fmt.Errorf("windows: exe %q must be a file name, not a path", w.Exe)
		}
		if w.Path != "" {
			if _, err := globToRegexp(w.Path); err != nil {
				return fmt.Errorf("windows: path: %w", err)
			}
		}
	}
	if m.MacOS != nil && m.MacOS.BundleID == "" {
		return errors.New("macos: bundleId required")
	}
	if m.Android != nil && m.Android.Package == "" {
		return errors.New("android: package required")
	}
	return nil
}

// Merge combines several policy documents, as delivered when more than one
// control-server grant applies to a node. Rules are concatenated in order;
// Default and Unidentified come from the first document that sets them.
// Merge returns nil, nil if docs is empty.
func Merge(docs [][]byte) (*Policy, error) {
	if len(docs) == 0 {
		return nil, nil
	}
	out := &Policy{Version: 1}
	for i, d := range docs {
		p, err := Parse(d)
		if err != nil {
			return nil, fmt.Errorf("document %d: %w", i, err)
		}
		if out.Default == "" {
			out.Default = p.Default
		}
		if out.Unidentified == "" {
			out.Unidentified = p.Unidentified
		}
		if out.GatewayUnavailable == "" {
			out.GatewayUnavailable = p.GatewayUnavailable
		}
		out.Rules = append(out.Rules, p.Rules...)
	}
	return out, nil
}

// Matcher is a [Policy] compiled for one OS.
type Matcher struct {
	defaultAction      Action
	unidentified       Action
	gatewayUnavailable Action
	rules              []compiledRule
}

type compiledRule struct {
	name    string
	action  Action
	gateway string
	apps    []func(*App) bool
}

// CompileOptions configures [Policy.Compile].
type CompileOptions struct {
	// GOOS selects which per-OS matchers are used ("windows", "darwin",
	// "android"). Matchers for other OSes are ignored.
	GOOS string

	// LookupEnv expands %NAME% in Windows paths. If nil, os.LookupEnv is
	// used. A variable that isn't set is an error, so that a rule never
	// silently turns into a pattern like `\foo.exe`.
	LookupEnv func(string) (string, bool)
}

// Compile returns a Matcher for opts.GOOS.
func (p *Policy) Compile(opts CompileOptions) (*Matcher, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	lookup := opts.LookupEnv
	if lookup == nil {
		lookup = os.LookupEnv
	}
	m := &Matcher{
		defaultAction:      p.Default,
		unidentified:       p.Unidentified,
		gatewayUnavailable: p.GatewayUnavailable,
	}
	if m.defaultAction == "" {
		m.defaultAction = ActionGateway
	}
	if m.unidentified == "" {
		m.unidentified = m.defaultAction
	}
	if m.gatewayUnavailable == "" {
		m.gatewayUnavailable = ActionBlock
	}
	for i, r := range p.Rules {
		cr := compiledRule{name: r.Name, action: r.Action, gateway: r.Gateway}
		for j, am := range r.Apps {
			f, err := compileApp(am, opts.GOOS, lookup)
			if err != nil {
				return nil, fmt.Errorf("approuting: rule %d (%q) app %d: %w", i, r.Name, j, err)
			}
			if f != nil {
				cr.apps = append(cr.apps, f)
			}
		}
		if len(cr.apps) > 0 {
			m.rules = append(m.rules, cr)
		}
	}
	return m, nil
}

// compileApp returns a match func for am on goos, or nil if am has no
// matcher for goos.
func compileApp(am AppMatcher, goos string, lookup func(string) (string, bool)) (func(*App) bool, error) {
	switch goos {
	case "windows":
		w := am.Windows
		if w == nil {
			return nil, nil
		}
		exe := w.Exe
		var re *regexp.Regexp
		if w.Path != "" {
			expanded, err := expandWindowsEnv(w.Path, lookup)
			if err != nil {
				return nil, err
			}
			if re, err = globToRegexp(expanded); err != nil {
				return nil, err
			}
		}
		return func(a *App) bool {
			p := NormalizeWindowsPath(a.Path)
			if p == "" {
				return false
			}
			if exe != "" && !strings.EqualFold(windowsBase(p), exe) {
				return false
			}
			if re != nil && !re.MatchString(p) {
				return false
			}
			return true
		}, nil
	case "darwin":
		if am.MacOS == nil {
			return nil, nil
		}
		id := am.MacOS.BundleID
		return func(a *App) bool { return a.BundleID == id }, nil
	case "android":
		if am.Android == nil {
			return nil, nil
		}
		pkg := am.Android.Package
		return func(a *App) bool { return a.Package == pkg }, nil
	}
	return nil, nil
}

// Decide returns the decision for app. A nil app means the owner couldn't
// be identified.
func (m *Matcher) Decide(app *App) Decision {
	if app == nil {
		return Decision{Action: m.unidentified}
	}
	for _, r := range m.rules {
		for _, f := range r.apps {
			if f(app) {
				return Decision{Action: r.action, Gateway: r.gateway, Rule: r.name}
			}
		}
	}
	return Decision{Action: m.defaultAction}
}

// Default returns the action for apps that match no rule.
func (m *Matcher) Default() Action { return m.defaultAction }

// GatewayUnavailable returns the action used when a rule's named gateway
// can't be used.
func (m *Matcher) GatewayUnavailable() Action { return m.gatewayUnavailable }

// Gateways returns the distinct gateway names the rules refer to.
func (m *Matcher) Gateways() []string {
	var out []string
	seen := map[string]bool{}
	for _, r := range m.rules {
		if k := strings.ToLower(r.gateway); k != "" && !seen[k] {
			seen[k] = true
			out = append(out, r.gateway)
		}
	}
	return out
}

// NormalizeWindowsPath converts forward slashes to backslashes and strips
// the `\\?\` long-path prefix, so paths from different APIs compare equal.
func NormalizeWindowsPath(p string) string {
	p = strings.ReplaceAll(p, "/", `\`)
	p = strings.TrimPrefix(p, `\\?\`)
	return p
}

func windowsBase(p string) string {
	if i := strings.LastIndexByte(p, '\\'); i >= 0 {
		return p[i+1:]
	}
	return p
}

// expandWindowsEnv replaces %NAME% with its value. "%%" is a literal '%'.
func expandWindowsEnv(s string, lookup func(string) (string, bool)) (string, error) {
	var b strings.Builder
	for {
		i := strings.IndexByte(s, '%')
		if i < 0 {
			b.WriteString(s)
			return b.String(), nil
		}
		b.WriteString(s[:i])
		s = s[i+1:]
		j := strings.IndexByte(s, '%')
		if j < 0 {
			return "", errors.New("unterminated %VARIABLE%")
		}
		name := s[:j]
		s = s[j+1:]
		if name == "" {
			b.WriteByte('%')
			continue
		}
		v, ok := lookup(name)
		if !ok || v == "" {
			return "", fmt.Errorf("environment variable %%%s%% is not set", name)
		}
		b.WriteString(v)
	}
}

// globToRegexp compiles a case-insensitive Windows path glob.
func globToRegexp(glob string) (*regexp.Regexp, error) {
	glob = NormalizeWindowsPath(glob)
	var b strings.Builder
	b.WriteString(`(?i)^`)
	for _, r := range glob {
		switch r {
		case '*':
			b.WriteString(`[^\\]*`)
		case '?':
			b.WriteString(`[^\\]`)
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	b.WriteString(`$`)
	return regexp.Compile(b.String())
}
