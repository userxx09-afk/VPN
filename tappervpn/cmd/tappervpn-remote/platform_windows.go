// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

//go:build windows

package main

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/jpeg"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	rc "tappervpn.com/feature/remotecontrol"
)

var (
	user32 = windows.NewLazySystemDLL("user32.dll")
	gdi32  = windows.NewLazySystemDLL("gdi32.dll")

	procGetSystemMetrics = user32.NewProc("GetSystemMetrics")
	procGetDC            = user32.NewProc("GetDC")
	procReleaseDC        = user32.NewProc("ReleaseDC")
	procMessageBoxW      = user32.NewProc("MessageBoxW")
	procSendInput        = user32.NewProc("SendInput")
	procGetCursorInfo    = user32.NewProc("GetCursorInfo")
	procGetIconInfo      = user32.NewProc("GetIconInfo")
	procDrawIconEx       = user32.NewProc("DrawIconEx")
	procSetDPIAware      = user32.NewProc("SetProcessDPIAware")

	procCreateCompatibleDC     = gdi32.NewProc("CreateCompatibleDC")
	procCreateCompatibleBitmap = gdi32.NewProc("CreateCompatibleBitmap")
	procSelectObject           = gdi32.NewProc("SelectObject")
	procBitBlt                 = gdi32.NewProc("BitBlt")
	procGetDIBits              = gdi32.NewProc("GetDIBits")
	procDeleteObject           = gdi32.NewProc("DeleteObject")
	procDeleteDC               = gdi32.NewProc("DeleteDC")
)

const (
	smCXScreen = 0
	smCYScreen = 1

	srcCopy      = 0x00CC0020
	biRGB        = 0
	dibRGBColors = 0

	mbYesNo         = 0x00000004
	mbIconQuestion  = 0x00000020
	mbSystemModal   = 0x00001000
	mbSetForeground = 0x00010000
	idYes           = 6

	inputMouse    = 0
	inputKeyboard = 1

	mouseeventfMove      = 0x0001
	mouseeventfAbsolute  = 0x8000
	mouseeventfLeftDown  = 0x0002
	mouseeventfLeftUp    = 0x0004
	mouseeventfRightDown = 0x0008
	mouseeventfRightUp   = 0x0010
	mouseeventfMidDown   = 0x0020
	mouseeventfMidUp     = 0x0040
	mouseeventfWheel     = 0x0800
	wheelDelta           = 120

	keyeventfKeyUp   = 0x0002
	keyeventfUnicode = 0x0004
)

type windowsHost struct {
	controlledBy atomic.Value // string
}

func newHostPlatform() (Platform, error) {
	// Work in real pixels: otherwise on a scaled display (125%, 150%...) the
	// screen size, the capture and the click positions are all virtualized.
	procSetDPIAware.Call()
	return &windowsHost{}, nil
}

type cursorInfo struct {
	cbSize  uint32
	flags   uint32
	hCursor uintptr
	x, y    int32
}

type iconInfo struct {
	fIcon              int32
	xHotspot, yHotspot uint32
	hbmMask, hbmColor  uintptr
}

// drawCursor paints the mouse pointer onto dc: a GDI screen grab leaves it
// out, and without it the person controlling can't see where they point.
func drawCursor(dc uintptr) {
	ci := cursorInfo{cbSize: uint32(unsafe.Sizeof(cursorInfo{}))}
	if r, _, _ := procGetCursorInfo.Call(uintptr(unsafe.Pointer(&ci))); r == 0 || ci.flags&1 == 0 || ci.hCursor == 0 {
		return // hidden or unavailable
	}
	var ii iconInfo
	if r, _, _ := procGetIconInfo.Call(ci.hCursor, uintptr(unsafe.Pointer(&ii))); r != 0 {
		if ii.hbmMask != 0 {
			procDeleteObject.Call(ii.hbmMask)
		}
		if ii.hbmColor != 0 {
			procDeleteObject.Call(ii.hbmColor)
		}
	}
	const diNormal = 0x0003
	procDrawIconEx.Call(dc, uintptr(ci.x-int32(ii.xHotspot)), uintptr(ci.y-int32(ii.yHotspot)),
		ci.hCursor, 0, 0, 0, 0, diNormal)
}

func (h *windowsHost) Kind() string       { return "windows" }
func (h *windowsHost) Codecs() []rc.Codec { return []rc.Codec{rc.CodecMJPEG} }

func (h *windowsHost) Screen() rc.ScreenSize {
	w, _, _ := procGetSystemMetrics.Call(uintptr(smCXScreen))
	ht, _, _ := procGetSystemMetrics.Call(uintptr(smCYScreen))
	return rc.ScreenSize{Width: int(w), Height: int(ht)}
}

func (h *windowsHost) RequestConsent(peerName, peerID string, timeoutSeconds int) bool {
	// MessageBox is modal and has no timeout; the daemon applies its own
	// timeout and treats a slow answer as a denial, so an unanswered box that
	// is later clicked simply has no effect.
	text := "\"" + peerName + "\" on your TapperVPN network wants to view this screen and control this computer.\n\nAllow remote control?"
	ret, _, _ := procMessageBoxW.Call(
		0,
		strptr(text),
		strptr("TapperVPN remote control"),
		uintptr(mbYesNo|mbIconQuestion|mbSystemModal|mbSetForeground),
	)
	return int(ret) == idYes
}

func (h *windowsHost) SessionStart(peerName string) { h.controlledBy.Store(peerName) }
func (h *windowsHost) SessionEnd()                  { h.controlledBy.Store("") }

// StartCapture grabs the primary screen with GDI and emits a JPEG per frame
// until ctx is cancelled. Throttled to keep it light on CPU/bandwidth.
func (h *windowsHost) StartCapture(ctx context.Context, codec rc.Codec, sink func([]byte)) error {
	sz := h.Screen()
	w, ht := sz.Width, sz.Height
	if w <= 0 || ht <= 0 {
		return syscall.EINVAL
	}

	screenDC, _, _ := procGetDC.Call(0)
	defer procReleaseDC.Call(0, screenDC)
	memDC, _, _ := procCreateCompatibleDC.Call(screenDC)
	defer procDeleteDC.Call(memDC)
	bmp, _, _ := procCreateCompatibleBitmap.Call(screenDC, uintptr(w), uintptr(ht))
	defer procDeleteObject.Call(bmp)
	oldBmp, _, _ := procSelectObject.Call(memDC, bmp)
	defer procSelectObject.Call(memDC, oldBmp)

	// Top-down 32bpp BGRA.
	bi := bitmapInfoHeader{
		Size:        uint32(unsafe.Sizeof(bitmapInfoHeader{})),
		Width:       int32(w),
		Height:      -int32(ht),
		Planes:      1,
		BitCount:    32,
		Compression: biRGB,
	}
	buf := make([]byte, w*ht*4)
	img := image.NewRGBA(image.Rect(0, 0, w, ht))

	const fps = 12
	ticker := time.NewTicker(time.Second / fps)
	defer ticker.Stop()
	failures := 0
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
		blt, _, _ := procBitBlt.Call(memDC, 0, 0, uintptr(w), uintptr(ht), screenDC, 0, 0, srcCopy)
		if blt != 0 {
			drawCursor(memDC)
		}
		// GetDIBits requires the bitmap not be selected into a DC.
		procSelectObject.Call(memDC, oldBmp)
		r, _, _ := procGetDIBits.Call(memDC, bmp, 0, uintptr(ht),
			uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&bi)), dibRGBColors)
		procSelectObject.Call(memDC, bmp)
		if blt == 0 || r == 0 {
			// Two seconds of failures: the desktop is locked or on a secure
			// screen (UAC, sign-in), which a user-session process can't see.
			if failures++; failures >= 2*fps {
				return errors.New("its screen can't be captured right now (it may be locked or showing a security prompt)")
			}
			continue
		}
		failures = 0
		// BGRA -> RGBA.
		for i := 0; i < len(buf); i += 4 {
			img.Pix[i] = buf[i+2]
			img.Pix[i+1] = buf[i+1]
			img.Pix[i+2] = buf[i]
			img.Pix[i+3] = 255
		}
		var jbuf bytes.Buffer
		if err := jpeg.Encode(&jbuf, img, &jpeg.Options{Quality: 72}); err != nil {
			continue
		}
		sink(jbuf.Bytes())
	}
}

func (h *windowsHost) InjectInput(e rc.InputEvent) {
	sz := h.Screen()
	switch e.Kind {
	case rc.InputPointerMove:
		h.sendMouse(absX(int(e.X), sz.Width), absY(int(e.Y), sz.Height), 0, mouseeventfMove|mouseeventfAbsolute)
	case rc.InputPointerButton:
		var down, up uint32
		switch e.Button {
		case 1:
			down, up = mouseeventfRightDown, mouseeventfRightUp
		case 2:
			down, up = mouseeventfMidDown, mouseeventfMidUp
		default:
			down, up = mouseeventfLeftDown, mouseeventfLeftUp
		}
		if e.Down {
			h.sendMouse(0, 0, 0, down)
		} else {
			h.sendMouse(0, 0, 0, up)
		}
	case rc.InputScroll:
		h.sendMouse(0, 0, uint32(int32(e.Y)*wheelDelta), mouseeventfWheel)
	case rc.InputText:
		for _, r := range e.Text {
			h.sendKeyUnicode(uint16(r), false)
			h.sendKeyUnicode(uint16(r), true)
		}
	case rc.InputNav:
		if vk := navToVK(rc.NavAction(e.Button)); vk != 0 {
			h.sendKeyVK(vk, false)
			h.sendKeyVK(vk, true)
		}
	case rc.InputKey:
		if e.Code != 0 {
			h.sendKeyVK(uint16(e.Code), !e.Down)
		}
	}
}

// --- SendInput plumbing ---

type rawInput struct {
	typ   uint32
	_     uint32 // pad to 8-byte align the union on amd64
	union [32]byte
}

type mouseInput struct {
	dx, dy      int32
	mouseData   uint32
	dwFlags     uint32
	time        uint32
	dwExtraInfo uintptr
}

type keybdInput struct {
	wVk, wScan  uint16
	dwFlags     uint32
	time        uint32
	dwExtraInfo uintptr
}

func (h *windowsHost) sendMouse(dx, dy int32, data, flags uint32) {
	ri := rawInput{typ: inputMouse}
	mi := (*mouseInput)(unsafe.Pointer(&ri.union[0]))
	mi.dx, mi.dy, mi.mouseData, mi.dwFlags = dx, dy, data, flags
	sendInput(&ri)
}

func (h *windowsHost) sendKeyVK(vk uint16, keyUp bool) {
	ri := rawInput{typ: inputKeyboard}
	ki := (*keybdInput)(unsafe.Pointer(&ri.union[0]))
	ki.wVk = vk
	if keyUp {
		ki.dwFlags = keyeventfKeyUp
	}
	sendInput(&ri)
}

func (h *windowsHost) sendKeyUnicode(scan uint16, keyUp bool) {
	ri := rawInput{typ: inputKeyboard}
	ki := (*keybdInput)(unsafe.Pointer(&ri.union[0]))
	ki.wScan = scan
	ki.dwFlags = keyeventfUnicode
	if keyUp {
		ki.dwFlags |= keyeventfKeyUp
	}
	sendInput(&ri)
}

func sendInput(ri *rawInput) {
	procSendInput.Call(1, uintptr(unsafe.Pointer(ri)), unsafe.Sizeof(*ri))
}

func absX(x, w int) int32 {
	if w <= 1 {
		return 0
	}
	return int32(x * 65535 / (w - 1))
}
func absY(y, h int) int32 {
	if h <= 1 {
		return 0
	}
	return int32(y * 65535 / (h - 1))
}

func navToVK(a rc.NavAction) uint16 {
	switch a {
	case rc.NavUp:
		return 0x26
	case rc.NavDown:
		return 0x28
	case rc.NavLeft:
		return 0x25
	case rc.NavRight:
		return 0x27
	case rc.NavOK:
		return 0x0D // Enter
	case rc.NavBack:
		return 0x1B // Esc
	case rc.NavHome:
		return 0x5B // Left Windows key (opens Start)
	case rc.NavMenu:
		return 0x5D // Apps/Menu key
	case rc.NavPlayPause:
		return 0xB3 // Media play/pause
	}
	return 0
}

type bitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

func strptr(s string) uintptr {
	p, _ := windows.UTF16PtrFromString(s)
	return uintptr(unsafe.Pointer(p))
}
