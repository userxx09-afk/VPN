// Copyright (c) Tailscale Inc & AUTHORS
// SPDX-License-Identifier: BSD-3-Clause

package com.tappervpn.ipn

import org.junit.Assert.assertEquals
import org.junit.Test

class IPNServiceTest {
  @Test
  fun allowedPackagesIncludeTapperVPN() {
    val packages =
        packagesForVpnBuilder(
            packagesList = listOf("com.termux"),
            allowPackages = true,
            tappervpnPackageName = "com.tappervpn.ipn",
            builtInDisallowedPackages = emptyList(),
        )

    assertEquals(listOf("com.termux", "com.tappervpn.ipn"), packages)
  }

  @Test
  fun excludedPackagesIncludeBuiltInDisallowedPackages() {
    val packages =
        packagesForVpnBuilder(
            packagesList = listOf("com.example.excluded"),
            allowPackages = false,
            tappervpnPackageName = "com.tappervpn.ipn",
            builtInDisallowedPackages = listOf("com.example.builtin"),
        )

    assertEquals(listOf("com.example.excluded", "com.example.builtin"), packages)
  }

  @Test
  fun emptyAllowedPackagesDoesNotAddTapperVPN() {
    val packages =
        packagesForVpnBuilder(
            packagesList = emptyList(),
            allowPackages = true,
            tappervpnPackageName = "com.tappervpn.ipn",
            builtInDisallowedPackages = emptyList(),
        )

    assertEquals(emptyList<String>(), packages)
  }

  @Test
  fun allowedPackagesDeduplicatesTapperVPN() {
    val packages =
        packagesForVpnBuilder(
            packagesList = listOf("com.termux", "com.tappervpn.ipn"),
            allowPackages = true,
            tappervpnPackageName = "com.tappervpn.ipn",
            builtInDisallowedPackages = emptyList(),
        )

    assertEquals(listOf("com.termux", "com.tappervpn.ipn"), packages)
  }
}
