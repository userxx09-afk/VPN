// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

package remotecontrol

import (
	"bytes"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	cases := []struct {
		ch      Channel
		payload []byte
	}{
		{ChControl, []byte(`{"type":"hello"}`)},
		{ChVideo, bytes.Repeat([]byte{0xAB}, 4096)},
		{ChInput, nil}, // zero-length payload must round-trip
		{ChVideo, []byte{}},
	}
	var buf bytes.Buffer
	for _, c := range cases {
		if err := WriteFrame(&buf, c.ch, c.payload); err != nil {
			t.Fatalf("WriteFrame(%d): %v", c.ch, err)
		}
	}
	for i, c := range cases {
		ch, payload, err := ReadFrame(&buf)
		if err != nil {
			t.Fatalf("ReadFrame #%d: %v", i, err)
		}
		if ch != c.ch {
			t.Errorf("#%d channel = %d, want %d", i, ch, c.ch)
		}
		if len(payload) != len(c.payload) {
			t.Errorf("#%d payload len = %d, want %d", i, len(payload), len(c.payload))
		}
		if len(c.payload) > 0 && !bytes.Equal(payload, c.payload) {
			t.Errorf("#%d payload mismatch", i)
		}
	}
}

func TestFrameTooLarge(t *testing.T) {
	var buf bytes.Buffer
	// Hand-craft a header declaring a payload over the cap.
	if err := WriteFrame(&buf, ChVideo, make([]byte, 10)); err != nil {
		t.Fatal(err)
	}
	// Corrupt the length to exceed the cap.
	b := buf.Bytes()
	b[1], b[2], b[3], b[4] = 0xFF, 0xFF, 0xFF, 0xFF
	if _, _, err := ReadFrame(bytes.NewReader(b)); err != ErrFrameTooLarge {
		t.Fatalf("ReadFrame err = %v, want ErrFrameTooLarge", err)
	}
	if err := WriteFrame(&bytes.Buffer{}, ChVideo, make([]byte, maxFrameLen+1)); err != ErrFrameTooLarge {
		t.Fatalf("WriteFrame err = %v, want ErrFrameTooLarge", err)
	}
}

func TestControlRoundTrip(t *testing.T) {
	in := ControlMessage{
		Type:   CtAccept,
		Accept: &Accept{Version: ProtocolVersion, Codec: CodecJPEGTiles, Screen: ScreenSize{1920, 1080}, HostKind: "windows"},
	}
	b, err := encodeControl(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := decodeControl(b)
	if err != nil {
		t.Fatal(err)
	}
	if out.Type != CtAccept || out.Accept == nil {
		t.Fatalf("decoded = %+v", out)
	}
	if out.Accept.Codec != CodecJPEGTiles || out.Accept.Screen.Width != 1920 || out.Accept.Screen.Height != 1080 {
		t.Errorf("accept mismatch: %+v", out.Accept)
	}
}

func TestInputRoundTrip(t *testing.T) {
	cases := []InputEvent{
		{Kind: InputPointerMove, X: 100, Y: 250},
		{Kind: InputPointerButton, Button: 1, Down: true},
		{Kind: InputScroll, X: 0, Y: -3},
		{Kind: InputKey, Code: 65, Down: true},
		{Kind: InputText, Text: "héllo, 世界"},
		{Kind: InputNav, Button: uint8(NavOK)},
		{Kind: InputPointerMove, X: -5, Y: -5}, // negative deltas must survive
	}
	for i, in := range cases {
		out, err := DecodeInput(EncodeInput(in))
		if err != nil {
			t.Fatalf("#%d decode: %v", i, err)
		}
		if out != in {
			t.Errorf("#%d = %+v, want %+v", i, out, in)
		}
	}
}

func TestInputShort(t *testing.T) {
	if _, err := DecodeInput([]byte{1, 2, 3}); err == nil {
		t.Fatal("expected error on short input event")
	}
}
