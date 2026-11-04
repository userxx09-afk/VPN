package capver

//go:generate go run ../../tools/capver/main.go

import (
	"maps"
	"slices"
	"strings"

	"tappervpn.com/tailcfg"
	"tappervpn.com/util/cmpver"
	"tappervpn.com/util/set"
)

// minVersionParts is the minimum number of version parts needed for major.minor.
const minVersionParts = 2

// fullNetmapRemovalsCapVer is the capability version of TapperVPN main when
// clients started reporting peers a full netmap drops as removed
// (tappervpn/tappervpn#15660); v1.104 is the first release at or above it.
const fullNetmapRemovalsCapVer tailcfg.CapabilityVersion = 148

// CanOldCodeBeCleanedUp is called at server startup to panic when
// [MinSupportedCapabilityVersion] has crossed a threshold at which a
// backwards-compat emit path can be deleted. Each entry pairs a
// [tailcfg.CapabilityVersion] threshold with the message identifying
// the code to remove.
//
// All capability-version-gated cleanups should be registered here.
func CanOldCodeBeCleanedUp() {
	if MinSupportedCapabilityVersion >= fullNetmapRemovalsCapVer {
		panic("the tappervpn/tappervpn#15660 compat can be cleaned up: grep for it (hscontrol/mapper/batcher.go and its tests)")
	}
}

func tappervpnVersSorted() []string {
	return slices.Sorted(maps.Keys(tappervpnToCapVer))
}

// TapperVPNVersion returns the TapperVPN version for the given [tailcfg.CapabilityVersion].
func TapperVPNVersion(ver tailcfg.CapabilityVersion) string {
	return capVerToTapperVPNVer[ver]
}

// CapabilityVersion returns the [tailcfg.CapabilityVersion] for the given TapperVPN version.
// It accepts both full versions (v1.90.1) and minor versions (v1.90).
func CapabilityVersion(ver string) tailcfg.CapabilityVersion {
	if !strings.HasPrefix(ver, "v") {
		ver = "v" + ver
	}

	// Try direct lookup first (works for minor versions like v1.90)
	if cv, ok := tappervpnToCapVer[ver]; ok {
		return cv
	}

	// Try extracting minor version from full version (v1.90.1 -> v1.90)
	parts := strings.Split(strings.TrimPrefix(ver, "v"), ".")
	if len(parts) >= minVersionParts {
		minor := "v" + parts[0] + "." + parts[1]
		return tappervpnToCapVer[minor]
	}

	return 0
}

// TapperVPNLatestMajorMinor returns the n latest TapperVPN versions (e.g. 1.80).
func TapperVPNLatestMajorMinor(n int, stripV bool) []string {
	if n <= 0 {
		return nil
	}

	majors := set.Set[string]{}

	for _, vers := range tappervpnVersSorted() {
		if stripV {
			vers = strings.TrimPrefix(vers, "v")
		}

		v := strings.Split(vers, ".")
		majors.Add(v[0] + "." + v[1])
	}

	majorSl := majors.Slice()
	// cmpver orders versions numerically, so v1.100 sorts after v1.98 rather than
	// lexically before it.
	slices.SortFunc(majorSl, cmpver.Compare)

	if n > len(majorSl) {
		return majorSl
	}

	return majorSl[len(majorSl)-n:]
}
