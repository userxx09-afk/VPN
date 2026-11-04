package capver

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestTapperVPNLatestMajorMinor(t *testing.T) {
	for _, test := range tappervpnLatestMajorMinorTests {
		t.Run("", func(t *testing.T) {
			output := TapperVPNLatestMajorMinor(test.n, test.stripV)
			if diff := cmp.Diff(output, test.expected); diff != "" {
				t.Errorf("TapperVPNLatestMajorMinor(%d, %v) mismatch (-want +got):\n%s", test.n, test.stripV, diff)
			}
		})
	}
}

func TestCapVerMinimumTapperVPNVersion(t *testing.T) {
	for _, test := range capVerMinimumTapperVPNVersionTests {
		t.Run("", func(t *testing.T) {
			output := TapperVPNVersion(test.input)
			if output != test.expected {
				t.Errorf("CapVerFromTapperVPNVersion(%d) = %s; want %s", test.input, output, test.expected)
			}
		})
	}
}
