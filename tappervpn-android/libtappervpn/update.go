// Copyright (c) TapperVPN contributors
// SPDX-License-Identifier: BSD-3-Clause

package libtappervpn

import (
	"encoding/hex"

	"tappervpn.com/updater"
)

// VerifyUpdate checks the release signature over sums (SHA256SUMS) and
// returns the hex SHA-256 it lists for asset. The app downloads the APK only
// after this succeeds and installs it only if the APK's hash matches.
// Called from AppUpdater.kt.
func VerifyUpdate(sums, sig []byte, asset string) (string, error) {
	h, err := updater.VerifiedHash(sums, sig, asset)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h), nil
}
