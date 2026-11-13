// Copyright (c) TapperVPN contributors
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"archive/zip"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const (
	assetName   = "tappervpn-windows-amd64.zip"
	serviceName = "TapperVPN"
)

// install stops the service, swaps in the files from the zip next to this
// executable, and starts the service again. Files in use can't be
// overwritten on Windows but can be renamed, so each old file is moved to
// <name>.old first (removed on the next run) and moved back if anything fails.
func install(pkg, tmp string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	dest := filepath.Dir(self)
	removeOld(dest)

	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("service manager (run as Administrator or SYSTEM): %w", err)
	}
	defer m.Disconnect()
	s, err := m.OpenService(serviceName)
	if err != nil {
		return fmt.Errorf("service %s isn't installed: %w", serviceName, err)
	}
	defer s.Close()

	zr, err := zip.OpenReader(pkg)
	if err != nil {
		return err
	}
	defer zr.Close()

	if err := stop(s); err != nil {
		return err
	}
	var moved []string
	rollback := func(cause error) error {
		for _, name := range moved {
			p := filepath.Join(dest, name)
			os.Remove(p)
			os.Rename(p+".old", p)
		}
		s.Start()
		return cause
	}
	for _, f := range zr.File {
		name := filepath.Base(f.Name)
		if f.FileInfo().IsDir() || name != f.Name || strings.HasSuffix(name, ".old") {
			continue // only plain top-level files
		}
		p := filepath.Join(dest, name)
		if _, err := os.Stat(p); err == nil {
			os.Remove(p + ".old")
			if err := os.Rename(p, p+".old"); err != nil {
				return rollback(err)
			}
		}
		moved = append(moved, name)
		if err := extract(f, p); err != nil {
			return rollback(err)
		}
	}
	if err := s.Start(); err != nil {
		return rollback(fmt.Errorf("starting %s: %w", serviceName, err))
	}
	log.Printf("replaced %d files in %s", len(moved), dest)
	startAppAtSignIn(dest)
	return nil
}

// startAppAtSignIn makes sure the TapperVPN app starts at sign-in, for installs
// made before the app existed (the installer sets this too). The running
// app restarts itself when its exe is replaced, so nothing else is needed.
func startAppAtSignIn(dir string) {
	app := filepath.Join(dir, "tappervpn-gui.exe")
	if _, err := os.Stat(app); err != nil {
		return
	}
	k, _, err := registry.CreateKey(registry.LOCAL_MACHINE,
		`Software\Microsoft\Windows\CurrentVersion\Run`, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		log.Printf("app autostart: %v", err)
		return
	}
	defer k.Close()
	want := `"` + app + `" -background`
	if v, _, err := k.GetStringValue("TapperVPN"); err == nil && v == want {
		return
	}
	if err := k.SetStringValue("TapperVPN", want); err != nil {
		log.Printf("app autostart: %v", err)
	}
}

func stop(s *mgr.Service) error {
	st, err := s.Control(svc.Stop)
	if err != nil {
		if st, qerr := s.Query(); qerr == nil && st.State == svc.Stopped {
			return nil
		}
		return fmt.Errorf("stopping %s: %w", serviceName, err)
	}
	for deadline := time.Now().Add(60 * time.Second); st.State != svc.Stopped; {
		if time.Now().After(deadline) {
			return fmt.Errorf("%s didn't stop within 60s", serviceName)
		}
		time.Sleep(500 * time.Millisecond)
		if st, err = s.Query(); err != nil {
			return err
		}
	}
	return nil
}

func extract(f *zip.File, path string) error {
	r, err := f.Open()
	if err != nil {
		return err
	}
	defer r.Close()
	w, err := os.Create(path)
	if err != nil {
		return err
	}
	if _, err := io.Copy(w, r); err != nil {
		w.Close()
		return err
	}
	return w.Close()
}

func removeOld(dir string) {
	old, _ := filepath.Glob(filepath.Join(dir, "*.old"))
	for _, p := range old {
		os.Remove(p) // fails harmlessly for the one still running
	}
}
