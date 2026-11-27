// Copyright (c) TapperVPN contributors
// SPDX-License-Identifier: BSD-3-Clause

//go:build !windows && !(linux && !android)

package remotecontrol

import "errors"

// On Android (and other app platforms) the app registers its own Restarter;
// these defaults only reconnect in place and never reboot.
func supervised() bool       { return false }
func exitForRestart()        {}
func canRebootMachine() bool { return false }

func rebootMachine(string) error { return errors.New("this device can't be rebooted remotely") }
