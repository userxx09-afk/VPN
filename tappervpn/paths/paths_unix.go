// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

//go:build !windows && !wasm && !plan9 && !tamago

package paths

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"golang.org/x/sys/unix"
	"tappervpn.com/version/distro"
)

func init() {
	stateFileFunc = stateFileUnix
	ensureStateDirPerms = ensureStateDirPermsUnix
}

func statePath() string {
	if runtime.GOOS == "linux" && distro.Get() == distro.JetKVM {
		return "/userdata/tappervpn/var/tappervpnd.state"
	}
	switch runtime.GOOS {
	case "linux", "illumos", "solaris":
		return "/var/lib/tappervpn/tappervpnd.state"
	case "freebsd", "openbsd":
		return "/var/db/tappervpn/tappervpnd.state"
	case "darwin":
		return "/Library/TapperVPN/tappervpnd.state"
	case "aix":
		return "/var/tappervpn/tappervpnd.state"
	default:
		return ""
	}
}

func stateFileUnix() string {
	if distro.Get() == distro.Gokrazy {
		return "/perm/tappervpnd/tappervpnd.state"
	}
	path := statePath()
	if path == "" {
		return ""
	}

	try := path
	for range 3 { // check writability of the file, /var/lib/tappervpn, and /var/lib
		err := unix.Access(try, unix.O_RDWR)
		if err == nil {
			return path
		}
		try = filepath.Dir(try)
	}

	if os.Getuid() == 0 {
		return ""
	}

	// For non-root users, fall back to $XDG_DATA_HOME/tappervpn/*.
	return filepath.Join(xdgDataHome(), "tappervpn", "tappervpnd.state")
}

func xdgDataHome() string {
	if e := os.Getenv("XDG_DATA_HOME"); e != "" {
		return e
	}
	return filepath.Join(os.Getenv("HOME"), ".local/share")
}

func ensureStateDirPermsUnix(dir string) error {
	if filepath.Base(dir) != "tappervpn" {
		return nil
	}
	fi, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("expected %q to be a directory; is %v", dir, fi.Mode())
	}
	const perm = 0700
	if fi.Mode().Perm() == perm {
		// Already correct.
		return nil
	}
	return os.Chmod(dir, perm)
}
