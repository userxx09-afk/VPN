// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

//go:build windows

package main

import (
	"bytes"
	"image"
	"image/draw"
	"image/jpeg"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	rc "tappervpn.com/feature/remotecontrol"
)

var (
	procRegisterClassExW = user32.NewProc("RegisterClassExW")
	procCreateWindowExW  = user32.NewProc("CreateWindowExW")
	procDefWindowProcW   = user32.NewProc("DefWindowProcW")
	procGetMessageW      = user32.NewProc("GetMessageW")
	procTranslateMessage = user32.NewProc("TranslateMessage")
	procDispatchMessageW = user32.NewProc("DispatchMessageW")
	procPostMessageW     = user32.NewProc("PostMessageW")
	procPostQuitMessage  = user32.NewProc("PostQuitMessage")
	procDestroyWindow    = user32.NewProc("DestroyWindow")
	procDefLoadCursorW   = user32.NewProc("LoadCursorW")
	procGetClientRect    = user32.NewProc("GetClientRect")
	procInvalidateRect   = user32.NewProc("InvalidateRect")
	procBeginPaint       = user32.NewProc("BeginPaint")
	procEndPaint         = user32.NewProc("EndPaint")
	procShowWindow       = user32.NewProc("ShowWindow")
	procDrawTextW        = user32.NewProc("DrawTextW")
	procFillRect         = user32.NewProc("FillRect")
	procGetSysColorBrush = user32.NewProc("GetSysColorBrush")
	procSetWindowTextW   = user32.NewProc("SetWindowTextW")
	procSetBkMode        = gdi32.NewProc("SetBkMode")

	procStretchDIBits = gdi32.NewProc("StretchDIBits")
)

const (
	wsOverlappedWindow = 0x00CF0000
	wsVisible          = 0x10000000
	cwUseDefault       = 0x80000000
	swShow             = 5

	wmDestroy     = 0x0002
	wmClose       = 0x0010
	wmPaint       = 0x000F
	wmMouseMove   = 0x0200
	wmLButtonDown = 0x0201
	wmLButtonUp   = 0x0202
	wmRButtonDown = 0x0204
	wmRButtonUp   = 0x0205
	wmMouseWheel  = 0x020A
	wmKeyDown     = 0x0100
	wmKeyUp       = 0x0101
	wmChar        = 0x0102

	dibRGBColorsV = 0
	srcCopyV      = 0x00CC0020
	idcArrow      = 32512
	colorWindow   = 5
	colorInfoBk   = 24

	dtCenter      = 0x0001
	dtWordBreak   = 0x0010
	dtNoPrefix    = 0x0800
	bkTransparent = 1
)

type winViewer struct {
	peerName string

	mu           sync.Mutex
	frame        []byte // top-down BGRA
	fw, fh       int
	hostW, hostH int
	hwnd         uintptr

	status string // shown when there's no picture, or as a banner once final
	notice string // shown as a banner for the whole session (Accept.Note)
	final  bool   // the session is over; stay open so the user can read why

	input     chan rc.InputEvent
	closeOnce sync.Once
	ready     chan struct{}
}

func (h *windowsHost) NewViewer(peerName string) (rc.Viewer, error) {
	v := &winViewer{
		peerName: peerName,
		input:    make(chan rc.InputEvent, 64),
		ready:    make(chan struct{}),
	}
	go v.run()
	<-v.ready // window created (or failed)
	return v, nil
}

// run owns the window: it must stay on one OS thread for the message loop.
func (v *winViewer) run() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	className, _ := syscall.UTF16PtrFromString("TapperVPNRemoteViewer")
	title, _ := syscall.UTF16PtrFromString("TapperVPN — " + v.peerName)
	cursor, _, _ := procDefLoadCursorW.Call(0, idcArrow)

	wndProc := syscall.NewCallback(v.wndProc)
	wc := wndClassExW{
		cbSize:        uint32(unsafe.Sizeof(wndClassExW{})),
		lpfnWndProc:   wndProc,
		hCursor:       cursor,
		hbrBackground: colorWindow + 1,
		lpszClassName: className,
	}
	procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))

	hwnd, _, _ := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(title)),
		wsOverlappedWindow|wsVisible,
		cwUseDefault, cwUseDefault, 1280, 760,
		0, 0, 0, 0,
	)
	v.mu.Lock()
	v.hwnd = hwnd
	v.mu.Unlock()
	close(v.ready)
	if hwnd == 0 {
		return
	}
	procShowWindow.Call(hwnd, swShow)

	var msg msgW
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(r) <= 0 { // 0 = WM_QUIT, -1 = error
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
}

func (v *winViewer) wndProc(hwnd, msg, wparam, lparam uintptr) uintptr {
	switch msg {
	case wmPaint:
		v.paint(hwnd)
		return 0
	case wmClose:
		procDestroyWindow.Call(hwnd)
		return 0
	case wmDestroy:
		v.closeInput()
		procPostQuitMessage.Call(0)
		return 0
	case wmMouseMove:
		x, y := v.toHost(hwnd, lparam)
		v.send(rc.InputEvent{Kind: rc.InputPointerMove, X: int32(x), Y: int32(y)})
		return 0
	case wmLButtonDown, wmLButtonUp, wmRButtonDown, wmRButtonUp:
		x, y := v.toHost(hwnd, lparam)
		v.send(rc.InputEvent{Kind: rc.InputPointerMove, X: int32(x), Y: int32(y)})
		btn := uint8(0)
		if msg == wmRButtonDown || msg == wmRButtonUp {
			btn = 1
		}
		down := msg == wmLButtonDown || msg == wmRButtonDown
		v.send(rc.InputEvent{Kind: rc.InputPointerButton, Button: btn, Down: down})
		return 0
	case wmMouseWheel:
		delta := int16(uint16(wparam>>16)) / 120
		v.send(rc.InputEvent{Kind: rc.InputScroll, Y: int32(delta)})
		return 0
	case wmKeyDown:
		if nav := vkToNav(uint32(wparam)); nav != 0 {
			v.send(rc.InputEvent{Kind: rc.InputNav, Button: uint8(nav)})
		}
		return 0
	case wmChar:
		if wparam >= 0x20 { // printable; control chars handled via nav
			v.send(rc.InputEvent{Kind: rc.InputText, Text: string(rune(wparam))})
		}
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, msg, wparam, lparam)
	return r
}

func (v *winViewer) paint(hwnd uintptr) {
	var ps paintStruct
	hdc, _, _ := procBeginPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
	defer procEndPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))

	v.mu.Lock()
	frame, fw, fh := v.frame, v.fw, v.fh
	status, final, notice := v.status, v.final, v.notice
	v.mu.Unlock()
	cw, ch := v.clientSize(hwnd)
	if frame == nil || fw == 0 || fh == 0 {
		// No picture yet (or ever): say what's happening instead of a blank window.
		full := rectW{0, 0, int32(cw), int32(ch)}
		brush, _, _ := procGetSysColorBrush.Call(colorWindow)
		procFillRect.Call(hdc, uintptr(unsafe.Pointer(&full)), brush)
		if status != "" {
			v.drawText(hdc, status, rectW{40, int32(ch/2 - 40), int32(cw - 40), int32(ch)})
		}
		return
	}
	defer func() {
		if notice != "" && !final {
			band := rectW{0, int32(ch - 44), int32(cw), int32(ch)}
			brush, _, _ := procGetSysColorBrush.Call(colorInfoBk)
			procFillRect.Call(hdc, uintptr(unsafe.Pointer(&band)), brush)
			v.drawText(hdc, notice, rectW{20, int32(ch - 36), int32(cw - 20), int32(ch)})
		}
		if final && status != "" {
			// Session over: keep the last picture and put the reason on top.
			band := rectW{0, 0, int32(cw), 60}
			brush, _, _ := procGetSysColorBrush.Call(colorInfoBk)
			procFillRect.Call(hdc, uintptr(unsafe.Pointer(&band)), brush)
			v.drawText(hdc, status, rectW{20, 10, int32(cw - 20), 60})
		}
	}()
	bi := bitmapInfoHeader{
		Size:        uint32(unsafe.Sizeof(bitmapInfoHeader{})),
		Width:       int32(fw),
		Height:      -int32(fh), // top-down
		Planes:      1,
		BitCount:    32,
		Compression: biRGB,
	}
	procStretchDIBits.Call(hdc,
		0, 0, uintptr(cw), uintptr(ch),
		0, 0, uintptr(fw), uintptr(fh),
		uintptr(unsafe.Pointer(&frame[0])), uintptr(unsafe.Pointer(&bi)),
		dibRGBColorsV, srcCopyV)
}

func (v *winViewer) drawText(hdc uintptr, text string, r rectW) {
	t, err := syscall.UTF16FromString(text)
	if err != nil {
		return
	}
	procSetBkMode.Call(hdc, bkTransparent)
	procDrawTextW.Call(hdc, uintptr(unsafe.Pointer(&t[0])), uintptr(len(t)-1),
		uintptr(unsafe.Pointer(&r)), dtCenter|dtWordBreak|dtNoPrefix)
}

// Notice implements rc.NoticeViewer.
func (v *winViewer) Notice(msg string) {
	v.mu.Lock()
	v.notice = msg
	hwnd := v.hwnd
	v.mu.Unlock()
	if hwnd != 0 {
		procInvalidateRect.Call(hwnd, 0, 0)
	}
}

// Status implements rc.StatusViewer.
func (v *winViewer) Status(msg string, final bool) {
	v.mu.Lock()
	if v.final { // keep the first reason the session ended
		v.mu.Unlock()
		return
	}
	v.status, v.final = msg, final
	hwnd := v.hwnd
	v.mu.Unlock()
	if hwnd == 0 {
		return
	}
	if final {
		if t, err := syscall.UTF16PtrFromString("TapperVPN — " + v.peerName + " — session ended"); err == nil {
			procSetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(t)))
		}
	}
	procInvalidateRect.Call(hwnd, 0, 1)
}

// --- rc.Viewer ---

func (v *winViewer) Start(a rc.Accept) error {
	v.mu.Lock()
	v.hostW, v.hostH = a.Screen.Width, a.Screen.Height
	v.mu.Unlock()
	return nil
}

func (v *winViewer) Frame(payload []byte) {
	img, err := jpeg.Decode(bytes.NewReader(payload))
	if err != nil {
		return
	}
	b := img.Bounds()
	rgba := image.NewRGBA(b)
	draw.Draw(rgba, b, img, b.Min, draw.Src)
	// RGBA -> BGRA in place.
	for i := 0; i < len(rgba.Pix); i += 4 {
		rgba.Pix[i], rgba.Pix[i+2] = rgba.Pix[i+2], rgba.Pix[i]
	}
	v.mu.Lock()
	v.frame, v.fw, v.fh = rgba.Pix, b.Dx(), b.Dy()
	if !v.final {
		v.status = "" // the picture says it all
	}
	hwnd := v.hwnd
	v.mu.Unlock()
	if hwnd != 0 {
		procInvalidateRect.Call(hwnd, 0, 0)
	}
}

func (v *winViewer) Resize(s rc.ScreenSize) {
	v.mu.Lock()
	v.hostW, v.hostH = s.Width, s.Height
	v.mu.Unlock()
}

func (v *winViewer) Input() <-chan rc.InputEvent { return v.input }

func (v *winViewer) Close() {
	v.mu.Lock()
	hwnd, final := v.hwnd, v.final
	v.mu.Unlock()
	if final {
		// Leave the window up with the reason; the user closes it.
		return
	}
	if hwnd != 0 {
		procPostMessageW.Call(hwnd, wmClose, 0, 0)
	}
}

func (v *winViewer) closeInput() {
	v.closeOnce.Do(func() { close(v.input) })
}

func (v *winViewer) send(e rc.InputEvent) {
	defer func() { recover() }() // input channel may be closed on teardown
	select {
	case v.input <- e:
	default:
	}
}

func (v *winViewer) clientSize(hwnd uintptr) (int, int) {
	var r rectW
	procGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
	return int(r.right - r.left), int(r.bottom - r.top)
}

// toHost maps a client-area mouse position (packed in lparam) to host screen
// coordinates, so a click lands where the user pointed regardless of scaling.
func (v *winViewer) toHost(hwnd, lparam uintptr) (int, int) {
	cx := int(int16(uint16(lparam & 0xffff)))
	cy := int(int16(uint16((lparam >> 16) & 0xffff)))
	cw, ch := v.clientSize(hwnd)
	v.mu.Lock()
	hw, hh := v.hostW, v.hostH
	v.mu.Unlock()
	if cw <= 0 || ch <= 0 || hw == 0 || hh == 0 {
		return 0, 0
	}
	return cx * hw / cw, cy * hh / ch
}

func vkToNav(vk uint32) rc.NavAction {
	switch vk {
	case 0x26:
		return rc.NavUp
	case 0x28:
		return rc.NavDown
	case 0x25:
		return rc.NavLeft
	case 0x27:
		return rc.NavRight
	case 0x0D:
		return rc.NavOK
	case 0x1B:
		return rc.NavBack
	case 0x08: // Backspace -> treat as Back nav for TV hosts
		return rc.NavBack
	}
	return 0
}

type wndClassExW struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     uintptr
	hIcon         uintptr
	hCursor       uintptr
	hbrBackground uintptr
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       uintptr
}

type msgW struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      struct{ x, y int32 }
}

type rectW struct{ left, top, right, bottom int32 }

type paintStruct struct {
	hdc         uintptr
	fErase      int32
	rcPaint     rectW
	fRestore    int32
	fIncUpdate  int32
	rgbReserved [32]byte
}
