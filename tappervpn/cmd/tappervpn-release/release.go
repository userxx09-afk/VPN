// Copyright (c) TapperVPN contributors
// SPDX-License-Identifier: BSD-3-Clause

// The tappervpn-release command manages the key that signs TapperVPN
// releases and signs a release folder. meshwall/scripts/release.ps1 runs it.
//
//	tappervpn-release keygen              make the key (once), print the public key
//	tappervpn-release pubkey              print the public key
//	tappervpn-release sign <dir>          write <dir>/SHA256SUMS and SHA256SUMS.sig
//	tappervpn-release verify <dir>        check them, as clients will
//
// The private key lives in %USERPROFILE%\.tappervpn\release.key (or
// $TAPPERVPN_RELEASE_KEY). Keep a copy somewhere safe and never commit it:
// whoever has it can push updates to every TapperVPN device.
package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"tappervpn.com/updater"
)

type keyFile struct {
	Seed   string `json:"seed"`   // hex, 32 bytes
	KeyNum string `json:"keynum"` // hex, 8 bytes
}

func keyPath() string {
	if p := os.Getenv("TAPPERVPN_RELEASE_KEY"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatal(err)
	}
	return filepath.Join(home, ".tappervpn", "release.key")
}

func loadKey() (seed, keyNum []byte) {
	b, err := os.ReadFile(keyPath())
	if err != nil {
		log.Fatalf("%v\nRun `tappervpn-release keygen` first, or restore your backup of the key.", err)
	}
	var k keyFile
	if err := json.Unmarshal(b, &k); err != nil {
		log.Fatalf("%s: %v", keyPath(), err)
	}
	seed, err1 := hex.DecodeString(k.Seed)
	keyNum, err2 := hex.DecodeString(k.KeyNum)
	if err1 != nil || err2 != nil || len(seed) != 32 || len(keyNum) != 8 {
		log.Fatalf("%s is damaged", keyPath())
	}
	return seed, keyNum
}

func main() {
	log.SetFlags(0)
	if len(os.Args) < 2 {
		log.Fatal("usage: tappervpn-release keygen | pubkey | sign <dir> | verify <dir>")
	}
	switch os.Args[1] {
	case "keygen":
		if _, err := os.Stat(keyPath()); err == nil {
			log.Fatalf("%s already exists; refusing to replace it (devices only trust that key)", keyPath())
		}
		seed, keyNum := make([]byte, 32), make([]byte, 8)
		rand.Read(seed)
		rand.Read(keyNum)
		b, _ := json.MarshalIndent(keyFile{hex.EncodeToString(seed), hex.EncodeToString(keyNum)}, "", "  ")
		if err := os.MkdirAll(filepath.Dir(keyPath()), 0o700); err != nil {
			log.Fatal(err)
		}
		if err := os.WriteFile(keyPath(), b, 0o600); err != nil {
			log.Fatal(err)
		}
		fmt.Fprintf(os.Stderr, "Wrote %s. Back it up somewhere safe.\n", keyPath())
		os.Stdout.Write(updater.PublicKeyFile(seed, keyNum))
	case "pubkey":
		os.Stdout.Write(updater.PublicKeyFile(loadKey()))
	case "sign", "verify":
		if len(os.Args) != 3 {
			log.Fatalf("usage: tappervpn-release %s <dir>", os.Args[1])
		}
		dir := os.Args[2]
		if os.Args[1] == "sign" {
			sign(dir)
		}
		verify(dir)
	default:
		log.Fatalf("unknown command %q", os.Args[1])
	}
}

func sign(dir string) {
	seed, keyNum := loadKey()
	if pub := updater.PublicKeyFile(seed, keyNum); strings.TrimSpace(string(pub)) != strings.TrimSpace(updater.ReleasePub) {
		log.Fatal("this key doesn't match tappervpn/updater/release.pub; devices would reject the release")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		log.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		if n := e.Name(); e.Type().IsRegular() && n != updater.SumsFile && n != updater.SigFile {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	var sums strings.Builder
	for _, n := range names {
		f, err := os.Open(filepath.Join(dir, n))
		if err != nil {
			log.Fatal(err)
		}
		h := sha256.New()
		_, err = io.Copy(h, f)
		f.Close()
		if err != nil {
			log.Fatal(err)
		}
		fmt.Fprintf(&sums, "%x  %s\n", h.Sum(nil), n)
	}
	msg := []byte(sums.String())
	must(os.WriteFile(filepath.Join(dir, updater.SumsFile), msg, 0o644))
	must(os.WriteFile(filepath.Join(dir, updater.SigFile), updater.Sign(msg, seed, keyNum), 0o644))
	fmt.Printf("Signed %d files in %s\n", len(names), dir)
}

func verify(dir string) {
	sums, err := os.ReadFile(filepath.Join(dir, updater.SumsFile))
	must(err)
	sig, err := os.ReadFile(filepath.Join(dir, updater.SigFile))
	must(err)
	if err := updater.Verify(sums, sig, []byte(updater.ReleasePub)); err != nil {
		log.Fatalf("verify: %v", err)
	}
	fmt.Println("Signature OK")
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
