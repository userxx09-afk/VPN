// Copyright (c) TapperVPN contributors
// SPDX-License-Identifier: BSD-3-Clause

// Package updater finds, downloads and verifies TapperVPN releases published
// on GitHub (github.com/userxx09-afk/VPN).
//
// Every release carries SHA256SUMS (one "<hex>  <file name>" line per asset)
// and SHA256SUMS.sig, an ed25519 signature over SHA256SUMS in usign format so
// OpenWrt routers can check it with the stock usign tool. An asset is only
// returned when the signature verifies against the key in release.pub and
// the asset's hash matches its SHA256SUMS line. The private key never leaves
// the release machine (see meshwall/scripts/release.ps1).
package updater

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tappervpn.com/util/cmpver"
)

// Version is the TapperVPN release this binary belongs to, e.g. "0.2.0".
// Set at build time: -ldflags "-X tappervpn.com/updater.Version=0.2.0".
var Version = "0.0.0-dev"

// Repo is the GitHub repository whose releases are checked.
const Repo = "userxx09-afk/VPN"

// ReleasePub is the usign-format public key release signatures must verify against.
//
//go:embed release.pub
var ReleasePub string

const (
	SumsFile = "SHA256SUMS"
	SigFile  = "SHA256SUMS.sig"
)

// Release is a published GitHub release.
type Release struct {
	Version string            // tag without the leading "v"
	Assets  map[string]string // file name -> download URL
}

var client = &http.Client{Timeout: 10 * time.Minute}

// Latest returns the newest published (non-draft, non-prerelease) release.
func Latest(ctx context.Context) (*Release, error) {
	url := "https://api.github.com/repos/" + Repo + "/releases/latest"
	body, err := get(ctx, url, 1<<20)
	if err != nil {
		if strings.Contains(err.Error(), "404") {
			return nil, fmt.Errorf("no releases published on github.com/%s yet", Repo)
		}
		return nil, err
	}
	var gh struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(body, &gh); err != nil {
		return nil, fmt.Errorf("parsing release: %w", err)
	}
	r := &Release{Version: strings.TrimPrefix(gh.TagName, "v"), Assets: map[string]string{}}
	for _, a := range gh.Assets {
		r.Assets[a.Name] = a.URL
	}
	return r, nil
}

// IsNewer reports whether release version v is newer than current.
func IsNewer(v, current string) bool {
	return v != "" && cmpver.Less(current, v)
}

// Download fetches asset from r into dir and returns its path. It verifies
// the release signature and the asset's hash first; on any mismatch it
// deletes what it downloaded and returns an error.
func (r *Release) Download(ctx context.Context, asset, dir string) (string, error) {
	sums, err := r.fetchSmall(ctx, SumsFile)
	if err != nil {
		return "", err
	}
	sig, err := r.fetchSmall(ctx, SigFile)
	if err != nil {
		return "", err
	}
	want, err := VerifiedHash(sums, sig, asset)
	if err != nil {
		return "", err
	}
	url, ok := r.Assets[asset]
	if !ok {
		return "", fmt.Errorf("release %s has no %s", r.Version, asset)
	}
	path := filepath.Join(dir, asset)
	if err := downloadFile(ctx, url, path, want); err != nil {
		os.Remove(path)
		return "", err
	}
	return path, nil
}

func (r *Release) fetchSmall(ctx context.Context, name string) ([]byte, error) {
	url, ok := r.Assets[name]
	if !ok {
		return nil, fmt.Errorf("release %s has no %s; refusing to update", r.Version, name)
	}
	return get(ctx, url, 1<<20)
}

// VerifiedHash checks sig over sums and returns the SHA-256 that sums lists for asset.
func VerifiedHash(sums, sig []byte, asset string) ([]byte, error) {
	if err := Verify(sums, sig, []byte(ReleasePub)); err != nil {
		return nil, err
	}
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		hexSum, name, ok := strings.Cut(strings.TrimSpace(sc.Text()), "  ")
		if ok && strings.TrimPrefix(name, "*") == asset {
			return hex.DecodeString(hexSum)
		}
	}
	return nil, fmt.Errorf("%s is not listed in %s", asset, SumsFile)
}

// usign blobs: "Ed" + 8-byte key number + key or signature.
const usignAlg = "Ed"

// Verify checks a usign-format signature over msg against a usign-format public key.
func Verify(msg, sig, pub []byte) error {
	pk, pkNum, err := usignBlob(pub, ed25519.PublicKeySize)
	if err != nil {
		return fmt.Errorf("public key: %w", err)
	}
	s, sNum, err := usignBlob(sig, ed25519.SignatureSize)
	if err != nil {
		return fmt.Errorf("signature: %w", err)
	}
	if !bytes.Equal(pkNum, sNum) {
		return errors.New("signature was made with a different key")
	}
	if !ed25519.Verify(pk, msg, s) {
		return errors.New("bad signature")
	}
	return nil
}

// Sign returns a usign-format signature over msg. seed is the 32-byte ed25519
// private key seed and keyNum the 8-byte key number shared with the public key.
func Sign(msg, seed, keyNum []byte) []byte {
	sig := ed25519.Sign(ed25519.NewKeyFromSeed(seed), msg)
	return usignFile("signed by TapperVPN release key "+hex.EncodeToString(keyNum), keyNum, sig)
}

// PublicKeyFile returns the usign-format public key file for seed.
func PublicKeyFile(seed, keyNum []byte) []byte {
	pub := ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)
	return usignFile("TapperVPN release key "+hex.EncodeToString(keyNum), keyNum, pub)
}

func usignFile(comment string, keyNum, payload []byte) []byte {
	blob := append(append([]byte(usignAlg), keyNum...), payload...)
	return []byte("untrusted comment: " + comment + "\n" + base64.StdEncoding.EncodeToString(blob) + "\n")
}

func usignBlob(file []byte, size int) (payload, keyNum []byte, err error) {
	lines := strings.Split(strings.TrimSpace(string(file)), "\n")
	if len(lines) < 2 || !strings.HasPrefix(lines[0], "untrusted comment:") {
		return nil, nil, errors.New("not in usign format")
	}
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(lines[1]))
	if err != nil {
		return nil, nil, err
	}
	if len(b) != 2+8+size || string(b[:2]) != usignAlg {
		return nil, nil, errors.New("not an ed25519 usign blob")
	}
	return b[10:], b[2:10], nil
}

func get(ctx context.Context, url string, limit int64) ([]byte, error) {
	resp, err := do(ctx, url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

func downloadFile(ctx context.Context, url, path string, wantSHA256 []byte) error {
	resp, err := do(ctx, url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(f, h), resp.Body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if !bytes.Equal(h.Sum(nil), wantSHA256) {
		return fmt.Errorf("%s: SHA-256 mismatch; download corrupted or tampered with", filepath.Base(path))
	}
	return nil
}

func do(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "tappervpn-updater/"+Version)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		resp.Body.Close()
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return resp, nil
}
