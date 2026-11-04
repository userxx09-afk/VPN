package v2

import (
	"net/netip"
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/juanfont/headscale/hscontrol/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"tappervpn.com/tailcfg"
	"tappervpn.com/tailcfg/nodecap"
)

// nodeAttrsTestUsers returns a minimal user set: two passkey-style users on
// different domains, mirroring the production multi-domain shape so user-target
// resolution is exercised across both.
func nodeAttrsTestUsers() types.Users {
	return types.Users{
		{ID: 1, Name: "alice", Email: "alice@example.com"},
		{ID: 2, Name: "bob", Email: "bob@example.org"},
	}
}

// nodeAttrsTestNodes returns a fixed mix of user-owned and tagged nodes used
// by every nodeAttrs unit test. Two user-owned nodes (one per user) and three
// tagged nodes (server, client, prod) so target resolution can be exercised
// across user, group, tag, autogroup, and wildcard alias forms.
func nodeAttrsTestNodes(users types.Users) types.Nodes {
	return types.Nodes{
		{
			ID:        1,
			GivenName: "alice-laptop",
			User:      &users[0],
			UserID:    &users[0].ID,
			IPv4:      ptrAddr("100.64.0.1"),
			IPv6:      ptrAddr("fd7a:115c:a1e0::1"),
			Hostinfo:  &tailcfg.Hostinfo{},
		},
		{
			ID:        2,
			GivenName: "bob-laptop",
			User:      &users[1],
			UserID:    &users[1].ID,
			IPv4:      ptrAddr("100.64.0.2"),
			IPv6:      ptrAddr("fd7a:115c:a1e0::2"),
			Hostinfo:  &tailcfg.Hostinfo{},
		},
		{
			ID:        3,
			GivenName: "server",
			Tags:      []string{"tag:server"},
			IPv4:      ptrAddr("100.64.0.3"),
			IPv6:      ptrAddr("fd7a:115c:a1e0::3"),
			Hostinfo:  &tailcfg.Hostinfo{},
		},
		{
			ID:        4,
			GivenName: "client",
			Tags:      []string{"tag:client"},
			IPv4:      ptrAddr("100.64.0.4"),
			IPv6:      ptrAddr("fd7a:115c:a1e0::4"),
			Hostinfo:  &tailcfg.Hostinfo{},
		},
		{
			ID:        5,
			GivenName: "prod",
			Tags:      []string{"tag:prod"},
			IPv4:      ptrAddr("100.64.0.5"),
			IPv6:      ptrAddr("fd7a:115c:a1e0::5"),
			Hostinfo:  &tailcfg.Hostinfo{},
		},
	}
}

const nodeAttrsTagOwners = `"tag:server": ["alice@example.com"],
		"tag:client": ["alice@example.com"],
		"tag:prod":   ["alice@example.com"]`

const appRoutingCap nodecap.Cap = "meshwall.dev/cap/app-routing"

func TestNodeAttrsCompile(t *testing.T) {
	t.Parallel()

	capMap := func(c nodecap.Cap) tailcfg.NodeCapMap {
		return tailcfg.NodeCapMap{c: nil}
	}

	tests := []struct {
		name string
		// extra is appended inside the policy block alongside tagOwners.
		extra string
		want  map[types.NodeID]tailcfg.NodeCapMap
	}{
		{
			// One unregistered member must not reject the policy (#3513).
			name: "group target with unregistered member hits registered members",
			extra: `"groups": {"group:g": ["alice@example.com", "ghost@example.com"]},
		"nodeAttrs": [{"target": ["group:g"], "attr": ["randomize-client-port"]}]`,
			want: map[types.NodeID]tailcfg.NodeCapMap{
				1: capMap("randomize-client-port"),
			},
		},
		{
			name:  "wildcard target hits every node",
			extra: `"nodeAttrs": [{"target": ["*"], "attr": ["randomize-client-port"]}]`,
			want: map[types.NodeID]tailcfg.NodeCapMap{
				1: capMap(nodecap.RandomizeClientPort),
				2: capMap(nodecap.RandomizeClientPort),
				3: capMap(nodecap.RandomizeClientPort),
				4: capMap(nodecap.RandomizeClientPort),
				5: capMap(nodecap.RandomizeClientPort),
			},
		},
		{
			name:  "user target hits only that user's untagged nodes",
			extra: `"nodeAttrs": [{"target": ["alice@example.com"], "attr": ["randomize-client-port"]}]`,
			want: map[types.NodeID]tailcfg.NodeCapMap{
				1: capMap(nodecap.RandomizeClientPort),
			},
		},
		{
			name:  "tag target hits only matching tagged nodes",
			extra: `"nodeAttrs": [{"target": ["tag:server"], "attr": ["drive:share", "drive:access"]}]`,
			want: map[types.NodeID]tailcfg.NodeCapMap{
				3: {
					nodecap.TaildriveShare:  nil,
					nodecap.TaildriveAccess: nil,
				},
			},
		},
		{
			name:  "autogroup:member hits untagged nodes only",
			extra: `"nodeAttrs": [{"target": ["autogroup:member"], "attr": ["randomize-client-port"]}]`,
			want: map[types.NodeID]tailcfg.NodeCapMap{
				1: capMap(nodecap.RandomizeClientPort),
				2: capMap(nodecap.RandomizeClientPort),
			},
		},
		{
			name:  "autogroup:tagged hits tagged nodes only",
			extra: `"nodeAttrs": [{"target": ["autogroup:tagged"], "attr": ["disable-captive-portal-detection"]}]`,
			want: map[types.NodeID]tailcfg.NodeCapMap{
				3: capMap(nodecap.DisableCaptivePortalDetection),
				4: capMap(nodecap.DisableCaptivePortalDetection),
				5: capMap(nodecap.DisableCaptivePortalDetection),
			},
		},
		{
			name: "merging two grants on overlapping targets unions attrs",
			extra: `"nodeAttrs": [
				{"target": ["*"],          "attr": ["drive:access"]},
				{"target": ["tag:server"], "attr": ["drive:share"]}
			]`,
			want: map[types.NodeID]tailcfg.NodeCapMap{
				1: capMap(nodecap.TaildriveAccess),
				2: capMap(nodecap.TaildriveAccess),
				3: {
					nodecap.TaildriveAccess: nil,
					nodecap.TaildriveShare:  nil,
				},
				4: capMap(nodecap.TaildriveAccess),
				5: capMap(nodecap.TaildriveAccess),
			},
		},
		{
			name: "app cap is stamped on targeted nodes only",
			extra: `"nodeAttrs": [{"target": ["tag:server"],
				"app": {"meshwall.dev/cap/app-routing": [{"version": 1, "default": "direct"}]}}]`,
			want: map[types.NodeID]tailcfg.NodeCapMap{
				3: {appRoutingCap: {`{"version": 1, "default": "direct"}`}},
			},
		},
		{
			name: "app values append in policy order across grants",
			extra: `"nodeAttrs": [
				{"target": ["*"],          "app": {"meshwall.dev/cap/app-routing": [{"n": 1}]}},
				{"target": ["tag:server"], "app": {"meshwall.dev/cap/app-routing": [{"n": 2}, {"n": 3}]}}
			]`,
			want: map[types.NodeID]tailcfg.NodeCapMap{
				1: {appRoutingCap: {`{"n": 1}`}},
				2: {appRoutingCap: {`{"n": 1}`}},
				3: {appRoutingCap: {`{"n": 1}`, `{"n": 2}`, `{"n": 3}`}},
				4: {appRoutingCap: {`{"n": 1}`}},
				5: {appRoutingCap: {`{"n": 1}`}},
			},
		},
		{
			name: "app and attr in one grant coexist, other caps stay separate",
			extra: `"nodeAttrs": [
				{"target": ["tag:client"], "attr": ["drive:share"],
				 "app": {"meshwall.dev/cap/app-routing": [{"n": 1}], "meshwall.dev/cap/other": [true]}}
			]`,
			want: map[types.NodeID]tailcfg.NodeCapMap{
				4: {
					nodecap.TaildriveShare:   nil,
					appRoutingCap:            {`{"n": 1}`},
					"meshwall.dev/cap/other": {`true`},
				},
			},
		},
		{
			name:  "top-level randomizeClientPort stamps every node",
			extra: `"randomizeClientPort": true`,
			want: map[types.NodeID]tailcfg.NodeCapMap{
				1: capMap(nodecap.RandomizeClientPort),
				2: capMap(nodecap.RandomizeClientPort),
				3: capMap(nodecap.RandomizeClientPort),
				4: capMap(nodecap.RandomizeClientPort),
				5: capMap(nodecap.RandomizeClientPort),
			},
		},
		{
			name: "global randomize plus per-tag entry merges",
			extra: `"randomizeClientPort": true,
				"nodeAttrs": [{"target": ["tag:server"], "attr": ["disable-captive-portal-detection"]}]`,
			want: map[types.NodeID]tailcfg.NodeCapMap{
				1: capMap(nodecap.RandomizeClientPort),
				2: capMap(nodecap.RandomizeClientPort),
				3: {
					nodecap.RandomizeClientPort:           nil,
					nodecap.DisableCaptivePortalDetection: nil,
				},
				4: capMap(nodecap.RandomizeClientPort),
				5: capMap(nodecap.RandomizeClientPort),
			},
		},
	}

	users := nodeAttrsTestUsers()
	nodes := nodeAttrsTestNodes(users)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			policy := `{
		"tagOwners": {` + nodeAttrsTagOwners + `},
		` + tt.extra + `
	}`

			pm, err := NewPolicyManager([]byte(policy), users, nodes.ViewSlice())
			require.NoErrorf(t, err, "policy must parse and validate:\n%s", policy)

			got, err := pm.pol.compileNodeAttrs(users, pm.nodes)
			require.NoError(t, err)

			if diff := cmp.Diff(tt.want, got, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("compileNodeAttrs (-want +got):\n%s", diff)
			}
		})
	}
}

func TestNodeAttrsValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		extra   string
		wantErr error
	}{
		{
			name:    "autogroup:self target rejected",
			extra:   `"nodeAttrs": [{"target": ["autogroup:self"], "attr": ["randomize-client-port"]}]`,
			wantErr: ErrNodeAttrsAutogroupNotAllowed,
		},
		{
			name:    "autogroup:admin target rejected with user-role hint",
			extra:   `"nodeAttrs": [{"target": ["autogroup:admin"], "attr": ["randomize-client-port"]}]`,
			wantErr: ErrNodeAttrsAutogroupNotAllowed,
		},
		{
			name:    "autogroup:owner target rejected with user-role hint",
			extra:   `"nodeAttrs": [{"target": ["autogroup:owner"], "attr": ["randomize-client-port"]}]`,
			wantErr: ErrNodeAttrsAutogroupNotAllowed,
		},
		{
			name:    "funnel attr rejected as unsupported",
			extra:   `"nodeAttrs": [{"target": ["*"], "attr": ["funnel"]}]`,
			wantErr: ErrNodeAttrUnsupported,
		},
		{
			// TapperVPN accepts empty entries (see tappervpn_nodeattrs_compat_test).
			name:  "empty entry accepted",
			extra: `"nodeAttrs": [{"target": ["*"]}]`,
		},
		{
			name:  "empty app map alone accepted",
			extra: `"nodeAttrs": [{"target": ["*"], "app": {}}]`,
		},
		{
			name:    "app cap outside meshwall namespace rejected",
			extra:   `"nodeAttrs": [{"target": ["*"], "app": {"tappervpn.com/cap/funnel": [{}]}}]`,
			wantErr: ErrNodeAttrAppCapUnsupported,
		},
		{
			name:    "app cap with bare name rejected",
			extra:   `"nodeAttrs": [{"target": ["*"], "app": {"app-routing": [{}]}}]`,
			wantErr: ErrNodeAttrAppCapUnsupported,
		},
		{
			name:    "ipPool set rejected as unsupported",
			extra:   `"nodeAttrs": [{"target": ["autogroup:member"], "ipPool": ["100.81.0.0/16"]}]`,
			wantErr: ErrNodeAttrIPPoolUnsupported,
		},
		{
			name:    "ipPool overlapping reserved range rejected at validate",
			extra:   `"nodeAttrs": [{"target": ["autogroup:member"], "ipPool": ["100.100.100.0/24"]}]`,
			wantErr: ErrNodeAttrsIPPoolReserved,
		},
	}

	users := nodeAttrsTestUsers()
	nodes := nodeAttrsTestNodes(users)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			policy := `{
		"tagOwners": {` + nodeAttrsTagOwners + `},
		` + tt.extra + `
	}`

			_, err := NewPolicyManager([]byte(policy), users, nodes.ViewSlice())
			if tt.wantErr == nil {
				require.NoError(t, err)

				return
			}

			require.Error(t, err)
			assert.ErrorIs(t, err, tt.wantErr)
		})
	}
}

func TestNodeAttrsIPPoolValidator(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		prefix  string
		wantErr error
	}{
		{name: "in cgnat", prefix: "100.81.0.0/16"},
		{name: "outside cgnat", prefix: "10.0.0.0/8", wantErr: ErrNodeAttrsIPPoolOutOfRange},
		{name: "less specific than cgnat", prefix: "100.0.0.0/8", wantErr: ErrNodeAttrsIPPoolOutOfRange},
		{name: "whole cgnat overlaps reserved", prefix: "100.64.0.0/10", wantErr: ErrNodeAttrsIPPoolReserved},
		{name: "overlaps quad100", prefix: "100.100.100.0/24", wantErr: ErrNodeAttrsIPPoolReserved},
		{name: "overlaps ipn", prefix: "100.115.92.0/24", wantErr: ErrNodeAttrsIPPoolReserved},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := validateNodeAttrIPPool(netip.MustParsePrefix(tt.prefix))
			if tt.wantErr != nil {
				require.Error(t, err)
				assert.ErrorIs(t, err, tt.wantErr)

				return
			}

			require.NoError(t, err)
		})
	}
}

func TestNodesWithChangedCapMap(t *testing.T) {
	t.Parallel()

	users := nodeAttrsTestUsers()
	nodes := nodeAttrsTestNodes(users)

	policyA := `{
		"tagOwners": {` + nodeAttrsTagOwners + `},
		"nodeAttrs": [{
			"target": ["tag:server"],
			"attr":   ["randomize-client-port"]
		}]
	}`

	pm, err := NewPolicyManager([]byte(policyA), users, nodes.ViewSlice())
	require.NoError(t, err)

	initial := pm.NodesWithChangedCapMap()
	slices.Sort(initial)
	assert.Equal(t, []types.NodeID{3}, initial,
		"first build reports every node with a non-empty CapMap")

	// Swap targets: server loses the attr, client and prod gain it.
	policyB := `{
		"tagOwners": {` + nodeAttrsTagOwners + `},
		"nodeAttrs": [{
			"target": ["tag:client", "tag:prod"],
			"attr":   ["randomize-client-port"]
		}]
	}`

	changed, err := pm.SetPolicy([]byte(policyB))
	require.NoError(t, err)
	require.True(t, changed)

	delta := pm.NodesWithChangedCapMap()
	slices.Sort(delta)
	assert.Equal(t, []types.NodeID{3, 4, 5}, delta,
		"server lost the cap, client and prod gained it -- diff is the symmetric difference")

	assert.Empty(t, pm.NodesWithChangedCapMap(),
		"NodesWithChangedCapMap drains its buffer on read")

	// Reload the same bytes. updateLocked still runs, but no node's
	// CapMap hash should change.
	_, err = pm.SetPolicy([]byte(policyB))
	require.NoError(t, err)

	assert.Empty(t, pm.NodesWithChangedCapMap(),
		"reloading the same policy must not produce CapMap diffs")
}

func TestNodeAttrsAppValidateAccepts(t *testing.T) {
	t.Parallel()

	users := nodeAttrsTestUsers()
	nodes := nodeAttrsTestNodes(users)

	tests := []struct {
		name  string
		extra string
	}{
		{
			name:  "app only",
			extra: `"nodeAttrs": [{"target": ["tag:server"], "app": {"meshwall.dev/cap/app-routing": [{"version": 1, "default": "direct", "rules": []}]}}]`,
		},
		{
			name:  "app with attr",
			extra: `"nodeAttrs": [{"target": ["*"], "attr": ["drive:share"], "app": {"meshwall.dev/cap/x": [1, "two", null]}}]`,
		},
		{
			name:  "app cap with no values",
			extra: `"nodeAttrs": [{"target": ["*"], "app": {"meshwall.dev/cap/x": []}}]`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			policy := `{"tagOwners": {` + nodeAttrsTagOwners + `}, ` + tt.extra + `}`

			_, err := NewPolicyManager([]byte(policy), users, nodes.ViewSlice())
			require.NoError(t, err)
		})
	}
}

func TestNodeAttrsAppValueInvalidJSON(t *testing.T) {
	t.Parallel()

	pol := &Policy{
		NodeAttrs: []NodeAttrGrant{{
			Targets: Aliases{Wildcard},
			App: tailcfg.NodeCapMap{
				appRoutingCap: {`{"version": `},
			},
		}},
	}

	err := pol.validate()
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNodeAttrAppValueInvalid)
}

// TestNodeAttrsAppSelfVsPeer pins that app caps are in the targeted node's own
// policy CapMap (merged into its self netmap) and never in the CapMap
// exposed to peers.
func TestNodeAttrsAppSelfVsPeer(t *testing.T) {
	t.Parallel()

	users := nodeAttrsTestUsers()
	nodes := nodeAttrsTestNodes(users)
	nodes[2].Hostinfo = &tailcfg.Hostinfo{
		RoutableIPs: []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0"), netip.MustParsePrefix("::/0")},
	}
	nodes[2].ApprovedRoutes = []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0"), netip.MustParsePrefix("::/0")}

	policy := `{
		"tagOwners": {` + nodeAttrsTagOwners + `},
		"nodeAttrs": [{"target": ["tag:server"],
			"app": {"meshwall.dev/cap/app-routing": [{"version": 1}]}}]
	}`

	pm, err := NewPolicyManager([]byte(policy), users, nodes.ViewSlice())
	require.NoError(t, err)

	want := []tailcfg.RawMessage{`{"version": 1}`}

	tests := []struct {
		name string
		id   types.NodeID
		// selfWant is the app cap values on the node's own policy CapMap.
		selfWant []tailcfg.RawMessage
	}{
		{name: "targeted node (also an exit node)", id: 3, selfWant: want},
		{name: "untargeted node", id: 4, selfWant: nil},
	}

	views := nodes.ViewSlice()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			self := pm.NodeCapMap(tt.id)
			assert.Equal(t, tt.selfWant, self[appRoutingCap], "self policy CapMap")

			var nv types.NodeView

			for _, v := range views.All() {
				if v.ID() == tt.id {
					nv = v
				}
			}

			peer := PeerCapMap(nv, self)
			assert.NotContains(t, peer, appRoutingCap, "peer view must not expose app caps")
		})
	}
}
