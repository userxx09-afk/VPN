// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

package tsconst

// WindowsMode is how tappervpnd is being run on Windows, as given by its
// --windows-mode flag. It decides where tappervpnd listens for the CLI and
// GUI and who may connect.
type WindowsMode string

const (
	// WindowsModeDefault is the default: tappervpnd is the TapperVPN service
	// (running as LocalSystem), or an administrator running tappervpnd.exe by
	// hand in its place. It must listen on the default named pipe under
	// \\.\pipe\ProtectedPrefix\Administrators\, which only administrators
	// can create; that is what lets clients trust it. It serves every local
	// user, with per-user access decided by tappervpnd itself.
	WindowsModeDefault WindowsMode = ""

	// WindowsModeDev is a developer running tappervpnd by hand, possibly
	// without administrator rights. It listens by default on a per-user
	// named pipe, \\.\pipe\tappervpn-<SID>, which is created owned by and
	// accessible to that user alone, and the CLI falls back to that pipe
	// when the default one doesn't exist. Nothing about it is trusted by
	// other users, and it can't serve them.
	WindowsModeDev WindowsMode = "dev"
)
