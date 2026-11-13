// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

// Package remotecontrol implements consent-gated remote control (screen view +
// input) of a TapperVPN node by a peer on the mesh.
//
// The feature is split across three layers: this shared Go package defines the
// transport, the consent handshake, and the wire protocol; platform packages
// (the Windows user-session helper and the Android app) supply screen capture,
// input injection, the consent prompt, and the controller viewer via the hooks
// in hooks.go.
//
// Security model: a session is only ever established over the authenticated,
// encrypted WireGuard mesh (via PeerAPI), it is always gated by an explicit
// consent decision on the host, and the host shows a visible indicator and
// retains a kill switch for the session's lifetime. See ext.go and peerapi.go.
package remotecontrol

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// ProtocolVersion is the wire-protocol version. Both ends send it in their
// handshake and refuse a mismatch; it is bumped on any breaking frame change.
const ProtocolVersion = 1

// Channel identifies the logical stream a frame belongs to on the single
// hijacked connection. Frames from different channels are interleaved.
type Channel uint8

const (
	// ChControl carries JSON control messages (see ControlMessage) in both
	// directions: handshake, stats, resize, and orderly shutdown.
	ChControl Channel = 1
	// ChVideo carries codec-tagged screen frames from host to controller.
	ChVideo Channel = 2
	// ChInput carries input events from controller to host.
	ChInput Channel = 3
)

// maxFrameLen bounds a single frame's payload so a malformed or hostile peer
// cannot force a huge allocation. Video keyframes are the largest legitimate
// payload; 16 MiB is comfortably above a full-screen JPEG/H.264 keyframe.
const maxFrameLen = 16 << 20

// ErrFrameTooLarge is returned by ReadFrame when a frame declares a payload
// larger than maxFrameLen.
var ErrFrameTooLarge = errors.New("remotecontrol: frame exceeds maximum length")

// WriteFrame writes a single length-prefixed frame to w. The wire format is:
//
//	[1 byte channel][4 byte big-endian payload length][payload]
//
// It is the caller's responsibility to serialize writes to w across goroutines;
// Session does this with a write mutex.
func WriteFrame(w io.Writer, ch Channel, payload []byte) error {
	if len(payload) > maxFrameLen {
		return ErrFrameTooLarge
	}
	var hdr [5]byte
	hdr[0] = byte(ch)
	binary.BigEndian.PutUint32(hdr[1:], uint32(len(payload)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	if len(payload) == 0 {
		return nil
	}
	_, err := w.Write(payload)
	return err
}

// ReadFrame reads a single frame written by WriteFrame. The returned payload is
// a freshly allocated slice owned by the caller.
func ReadFrame(r io.Reader) (Channel, []byte, error) {
	var hdr [5]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(hdr[1:])
	if n > maxFrameLen {
		return 0, nil, ErrFrameTooLarge
	}
	if n == 0 {
		return Channel(hdr[0]), nil, nil
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	return Channel(hdr[0]), payload, nil
}

// ControlMessage is the JSON envelope sent on ChControl. Exactly one of the
// pointer fields is set, selected by Type.
type ControlMessage struct {
	Type ControlType `json:"type"`

	Hello  *Hello        `json:"hello,omitempty"`
	Accept *Accept       `json:"accept,omitempty"`
	Resize *ScreenSize   `json:"resize,omitempty"`
	Stats  *SessionStats `json:"stats,omitempty"`
	Bye    *Bye          `json:"bye,omitempty"`
}

// ControlType names the kind of ControlMessage.
type ControlType string

const (
	// CtHello is the controller's opening message after the connection is
	// hijacked, declaring its protocol version and decode capabilities.
	CtHello ControlType = "hello"
	// CtAccept is the host's reply once consent has been granted, declaring the
	// negotiated codec and the initial screen size. A denied or timed-out
	// request never reaches this message: the host returns an HTTP error before
	// the hijack instead (see peerapi.go).
	CtAccept ControlType = "accept"
	// CtResize informs the controller that the host's screen size changed.
	CtResize ControlType = "resize"
	// CtStats carries periodic session statistics (either direction).
	CtStats ControlType = "stats"
	// CtBye requests an orderly shutdown of the session (either direction).
	CtBye ControlType = "bye"
)

// Codec identifies a video encoding. The controller advertises what it can
// decode in Hello; the host picks one and names it in Accept.
type Codec string

const (
	// CodecJPEGTiles is a dependency-free encoding: the frame is split into a
	// grid of tiles and only changed (dirty) tiles are sent, each as a JPEG.
	// It is the Windows v1 default and the universal fallback.
	CodecJPEGTiles Codec = "jpeg-tiles"
	// CodecH264 is an H.264 Annex-B elementary stream, used where a hardware
	// encoder is available (Android/Fire TV via MediaCodec).
	CodecH264 Codec = "h264"
)

// Hello is the controller's opening ChControl message.
type Hello struct {
	Version int     `json:"version"`
	Codecs  []Codec `json:"codecs"` // codecs the controller can decode, best first
}

// Accept is the host's ChControl reply after consent is granted.
type Accept struct {
	Version int        `json:"version"`
	Codec   Codec      `json:"codec"` // chosen from the controller's Hello list
	Screen  ScreenSize `json:"screen"`
	// HostKind describes the host platform ("windows", "android", "firetv") so
	// the controller can label the viewer and tailor its input pad.
	HostKind string `json:"hostKind,omitempty"`
}

// ScreenSize is the host screen's pixel dimensions.
type ScreenSize struct {
	Width  int `json:"w"`
	Height int `json:"h"`
}

// SessionStats is periodic telemetry shown in the UI and used for logging.
type SessionStats struct {
	FramesSent  uint64 `json:"framesSent,omitempty"`
	BytesSent   uint64 `json:"bytesSent,omitempty"`
	InputEvents uint64 `json:"inputEvents,omitempty"`
	FPS         int    `json:"fps,omitempty"`
}

// Bye carries an optional human-readable reason for ending the session.
type Bye struct {
	Reason string `json:"reason,omitempty"`
}

// encodeControl marshals a ControlMessage to a ChControl payload.
func encodeControl(m ControlMessage) ([]byte, error) { return json.Marshal(m) }

// decodeControl parses a ChControl payload.
func decodeControl(b []byte) (ControlMessage, error) {
	var m ControlMessage
	err := json.Unmarshal(b, &m)
	return m, err
}

// InputKind is the type of an input event carried on ChInput.
type InputKind uint8

const (
	// InputPointerMove moves the pointer to absolute position (X, Y), given in
	// the host screen's pixel coordinates.
	InputPointerMove InputKind = 1
	// InputPointerButton presses (Down=true) or releases a pointer button.
	// Button: 0=left, 1=right, 2=middle.
	InputPointerButton InputKind = 2
	// InputScroll scrolls by (X, Y) wheel deltas.
	InputScroll InputKind = 3
	// InputKey presses (Down=true) or releases a key, identified by Code (a
	// platform-neutral key code; see keycodes.go on each platform).
	InputKey InputKind = 4
	// InputText types a Unicode string (IME/paste style), carried in Text.
	InputText InputKind = 5
	// InputNav is an abstract TV-remote navigation action (see NavAction). A
	// Windows host maps it to arrow/Enter/Esc keystrokes; a Fire TV host maps it
	// to accessibility actions.
	InputNav InputKind = 6
)

// NavAction enumerates the abstract remote-navigation actions carried by an
// InputNav event (in InputEvent.Code).
type NavAction uint8

const (
	NavUp NavAction = iota + 1
	NavDown
	NavLeft
	NavRight
	NavOK
	NavBack
	NavHome
	NavMenu
	NavPlayPause
)

// InputEvent is a single input action sent from controller to host. It encodes
// to a compact fixed-layout binary form (EncodeInput); Text events append the
// UTF-8 bytes after the fixed header.
type InputEvent struct {
	Kind   InputKind
	X, Y   int32  // pointer position or scroll delta
	Button uint8  // pointer button, or NavAction for InputNav
	Down   bool   // key/button pressed vs released
	Code   uint32 // key code for InputKey
	Text   string // for InputText
}

// fixed input header: kind(1) x(4) y(4) button(1) down(1) code(4) textLen(4)
const inputHeaderLen = 1 + 4 + 4 + 1 + 1 + 4 + 4

func EncodeInput(e InputEvent) []byte {
	text := []byte(e.Text)
	buf := make([]byte, inputHeaderLen+len(text))
	buf[0] = byte(e.Kind)
	binary.BigEndian.PutUint32(buf[1:], uint32(e.X))
	binary.BigEndian.PutUint32(buf[5:], uint32(e.Y))
	buf[9] = e.Button
	if e.Down {
		buf[10] = 1
	}
	binary.BigEndian.PutUint32(buf[11:], e.Code)
	binary.BigEndian.PutUint32(buf[15:], uint32(len(text)))
	copy(buf[inputHeaderLen:], text)
	return buf
}

func DecodeInput(b []byte) (InputEvent, error) {
	if len(b) < inputHeaderLen {
		return InputEvent{}, fmt.Errorf("remotecontrol: short input event (%d bytes)", len(b))
	}
	e := InputEvent{
		Kind:   InputKind(b[0]),
		X:      int32(binary.BigEndian.Uint32(b[1:])),
		Y:      int32(binary.BigEndian.Uint32(b[5:])),
		Button: b[9],
		Down:   b[10] == 1,
		Code:   binary.BigEndian.Uint32(b[11:]),
	}
	textLen := binary.BigEndian.Uint32(b[15:])
	if int(textLen) != len(b)-inputHeaderLen {
		return InputEvent{}, fmt.Errorf("remotecontrol: input text length mismatch (declared %d, have %d)", textLen, len(b)-inputHeaderLen)
	}
	if textLen > 0 {
		e.Text = string(b[inputHeaderLen:])
	}
	return e, nil
}
