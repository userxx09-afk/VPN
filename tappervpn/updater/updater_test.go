// Copyright (c) TapperVPN contributors
// SPDX-License-Identifier: BSD-3-Clause

package updater

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"testing"
)

func TestSignVerify(t *testing.T) {
	seed := bytes.Repeat([]byte{7}, 32)
	keyNum := []byte("12345678")
	pub := PublicKeyFile(seed, keyNum)
	msg := []byte("hello\n")
	sig := Sign(msg, seed, keyNum)

	if err := Verify(msg, sig, pub); err != nil {
		t.Fatalf("good signature rejected: %v", err)
	}
	if err := Verify([]byte("hellO\n"), sig, pub); err == nil {
		t.Error("changed message accepted")
	}
	other := PublicKeyFile(bytes.Repeat([]byte{8}, 32), []byte("87654321"))
	if err := Verify(msg, sig, other); err == nil {
		t.Error("signature accepted under another key")
	}
}

func TestVerifiedHashNeedsReleaseKey(t *testing.T) {
	// Signed by a key that isn't release.pub: must be refused even though
	// the file and hash are well-formed.
	seed, keyNum := bytes.Repeat([]byte{9}, 32), []byte("abcdefgh")
	sum := sha256.Sum256([]byte("apk"))
	sums := []byte(fmt.Sprintf("%x  tappervpn-android.apk\n", sum))
	if _, err := VerifiedHash(sums, Sign(sums, seed, keyNum), "tappervpn-android.apk"); err == nil {
		t.Fatal("release signed by an unknown key was accepted")
	}
}

func TestIsNewer(t *testing.T) {
	for _, tt := range []struct {
		v, cur string
		want   bool
	}{
		{"0.2.0", "0.1.0", true},
		{"0.10.0", "0.9.0", true},
		{"0.1.0", "0.1.0", false},
		{"0.1.0", "0.2.0", false},
		{"0.1.0", "0.0.0-dev", true},
		{"", "0.1.0", false},
	} {
		if got := IsNewer(tt.v, tt.cur); got != tt.want {
			t.Errorf("IsNewer(%q, %q) = %v, want %v", tt.v, tt.cur, got, tt.want)
		}
	}
}
