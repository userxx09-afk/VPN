// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

// Package tappervpndhooks provides hooks for optional features
// to add to during init that tappervpnd calls at runtime.
package tappervpndhooks

import "tappervpn.com/feature"

// UninstallSystemDaemonWindows is called when the Windows
// system daemon is uninstalled.
var UninstallSystemDaemonWindows feature.Hooks[func()]
