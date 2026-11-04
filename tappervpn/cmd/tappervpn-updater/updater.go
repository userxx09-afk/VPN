// Copyright (c) TapperVPN contributors
// SPDX-License-Identifier: BSD-3-Clause

// The tappervpn-updater command installs the newest TapperVPN release from
// GitHub when it is newer than the installed one.
//
//	tappervpn-updater            check and install if newer
//	tappervpn-updater -check     only report
//	tappervpn-updater -force     reinstall the latest release even if not newer
//
// Windows (a daily scheduled task, see meshwall/scripts/install-windows.ps1):
// replaces the files in its own folder and restarts the TapperVPN service.
//
// Linux, the control server (a systemd timer, see meshwall/server/install.sh):
// replaces headscale, tappervpn and tappervpnd in /usr/local/bin, backs up the
// Headscale database, restarts headscale, and rolls back if it doesn't come up.
//
// Android and GL.iNet routers update themselves; see their own code.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"time"

	"tappervpn.com/updater"
)

var (
	checkOnly = flag.Bool("check", false, "only report whether an update is available")
	force     = flag.Bool("force", false, "install the latest release even if it isn't newer")
)

func main() {
	log.SetFlags(log.LstdFlags)
	log.SetPrefix("tappervpn-updater: ")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	rel, err := updater.Latest(ctx)
	if err != nil {
		log.Fatalf("checking for updates: %v", err)
	}
	if !updater.IsNewer(rel.Version, updater.Version) && !*force {
		log.Printf("up to date (installed %s, latest %s)", updater.Version, rel.Version)
		return
	}
	log.Printf("update available: %s -> %s", updater.Version, rel.Version)
	if *checkOnly {
		return
	}
	dir, err := os.MkdirTemp("", "tappervpn-update-")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(dir)
	pkg, err := rel.Download(ctx, assetName, dir)
	if err != nil {
		log.Fatalf("downloading %s: %v", assetName, err)
	}
	if err := install(pkg, dir); err != nil {
		log.Fatalf("installing %s: %v", rel.Version, err)
	}
	log.Printf("installed %s", rel.Version)
}
