// Copyright (c) TapperVPN contributors
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// The Linux updater is for the control server (tapper): Headscale plus the
// client binaries. Routers use glinet/files/usr/libexec/tappervpn/update.
const (
	assetName = "tappervpn-server-linux-amd64.tar.gz"
	binDir    = "/usr/local/bin"
	dbPath    = "/var/lib/headscale/db.sqlite"
)

// Binaries the server package may contain. Anything else in it is ignored.
var serverBinaries = map[string]bool{
	"headscale": true, "tappervpn": true, "tappervpnd": true, "tappervpn-updater": true,
}

func install(pkg, tmp string) error {
	staged := filepath.Join(tmp, "staged")
	if err := untar(pkg, staged); err != nil {
		return err
	}
	// New Headscale versions may migrate the database on start; keep a copy.
	backup := dbPath + ".pre-update-" + time.Now().Format("20060102-150405")
	if _, err := os.Stat(dbPath); err == nil {
		if err := exec.Command("cp", "-a", dbPath, backup).Run(); err != nil {
			return fmt.Errorf("backing up database: %w", err)
		}
		log.Printf("database backed up to %s", backup)
	}

	entries, _ := os.ReadDir(staged)
	var replaced []string
	rollback := func(cause error) error {
		for _, name := range replaced {
			p := filepath.Join(binDir, name)
			os.Rename(p+".prev", p)
		}
		if _, err := os.Stat(backup); err == nil {
			exec.Command("systemctl", "stop", "headscale").Run()
			exec.Command("cp", "-a", backup, dbPath).Run()
		}
		restart()
		return fmt.Errorf("%w (rolled back)", cause)
	}
	for _, e := range entries {
		name := e.Name()
		if !serverBinaries[name] {
			continue
		}
		p := filepath.Join(binDir, name)
		if _, err := os.Stat(p); err == nil {
			if err := exec.Command("cp", "-a", p, p+".prev").Run(); err != nil {
				return rollback(err)
			}
		}
		replaced = append(replaced, name)
		// Rename into place so a running binary is never half-written.
		if err := copyFile(filepath.Join(staged, name), p+".new", 0o755); err != nil {
			return rollback(err)
		}
		if err := os.Rename(p+".new", p); err != nil {
			return rollback(err)
		}
	}
	if err := restart(); err != nil {
		return rollback(err)
	}
	log.Printf("replaced %v in %s; previous versions kept as *.prev", replaced, binDir)
	return nil
}

// restart restarts headscale (and tappervpnd if it is in use) and checks
// that headscale is still running a few seconds later.
func restart() error {
	if exec.Command("systemctl", "is-enabled", "--quiet", "tappervpnd").Run() == nil {
		exec.Command("systemctl", "restart", "tappervpnd").Run()
	}
	if out, err := exec.Command("systemctl", "restart", "headscale").CombinedOutput(); err != nil {
		return fmt.Errorf("restarting headscale: %v: %s", err, out)
	}
	time.Sleep(10 * time.Second)
	if exec.Command("systemctl", "is-active", "--quiet", "headscale").Run() != nil {
		return fmt.Errorf("headscale isn't running after the update; see journalctl -u headscale")
	}
	return nil
}

func untar(pkg, dest string) error {
	f, err := os.Open(pkg)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		name := filepath.Base(h.Name)
		if h.Typeflag != tar.TypeReg || !serverBinaries[name] {
			continue
		}
		w, err := os.OpenFile(filepath.Join(dest, name), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return err
		}
		if _, err := io.Copy(w, tr); err != nil {
			w.Close()
			return err
		}
		if err := w.Close(); err != nil {
			return err
		}
	}
}

func copyFile(src, dst string, mode os.FileMode) error {
	r, err := os.Open(src)
	if err != nil {
		return err
	}
	defer r.Close()
	w, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(w, r); err != nil {
		w.Close()
		return err
	}
	return w.Close()
}
