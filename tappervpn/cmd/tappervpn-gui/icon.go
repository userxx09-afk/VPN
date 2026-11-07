// Copyright (c) TapperVPN contributors
// SPDX-License-Identifier: BSD-3-Clause

//go:build windows

package main

import (
	"bytes"
	"image"
	"image/color"
	"math"
	"sync"

	ico "github.com/Kodeworks/golang-image-ico"
)

// The tray icon is the TapperVPN "T ->" gate sign (installer/make-icon.py),
// drawn in the color of the connection state.
type iconState int

const (
	stateOff  iconState = iota // gray: disconnected
	stateWait                  // amber: connecting, or exit node offline
	stateOn                    // green: connected
	stateExit                  // cyan: connected through an exit node
)

var strokeColor = map[iconState]color.NRGBA{
	stateOff:  {0x9A, 0x9A, 0x9A, 0xFF},
	stateWait: {0xFF, 0xB3, 0x00, 0xFF},
	stateOn:   {0x39, 0xFF, 0x14, 0xFF},
	stateExit: {0x00, 0xE5, 0xFF, 0xFF},
}

type seg struct{ x0, y0, x1, y1 float64 }

// In a 60x60 box, like make-icon.py after its shift.
var logoStrokes = []seg{
	{9, 24, 21, 24},      // T bar
	{15, 24, 15, 37},     // T stem
	{26, 30, 49, 30},     // arrow shaft
	{41.5, 22.5, 49, 30}, // arrow head
	{49, 30, 41.5, 37.5},
}

var (
	iconMu    sync.Mutex
	iconCache = map[iconState][]byte{}
)

func iconICO(s iconState) []byte {
	iconMu.Lock()
	defer iconMu.Unlock()
	if b, ok := iconCache[s]; ok {
		return b
	}
	var buf bytes.Buffer
	ico.Encode(&buf, drawLogo(32, strokeColor[s]))
	iconCache[s] = buf.Bytes()
	return iconCache[s]
}

func drawLogo(size int, fg color.NRGBA) image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	const ss = 4 // supersampling per axis
	scale := 60.0 / float64(size)
	bg := color.NRGBA{0x0A, 0x0A, 0x0A, 0xFF}
	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			var r, g, b, a float64
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					x := (float64(px) + (float64(sx)+0.5)/ss) * scale
					y := (float64(py) + (float64(sy)+0.5)/ss) * scale
					var c color.NRGBA
					switch {
					case onStroke(x, y, 2.6):
						c = fg
					case inRoundRect(x, y, 0, 0, 60, 60, 11):
						c = bg
					default:
						continue
					}
					r += float64(c.R)
					g += float64(c.G)
					b += float64(c.B)
					a += 255
				}
			}
			if a == 0 {
				continue
			}
			n := a / 255
			img.SetNRGBA(px, py, color.NRGBA{
				uint8(r / n), uint8(g / n), uint8(b / n), uint8(a / (ss * ss)),
			})
		}
	}
	return img
}

// onStroke reports whether (x, y) is within half-width w of a logo stroke
// (round caps). Strokes are a little bolder than the big icon's so they
// read at 16px.
func onStroke(x, y, w float64) bool {
	for _, s := range logoStrokes {
		dx, dy := s.x1-s.x0, s.y1-s.y0
		t := ((x-s.x0)*dx + (y-s.y0)*dy) / (dx*dx + dy*dy)
		t = math.Max(0, math.Min(1, t))
		if math.Hypot(x-(s.x0+t*dx), y-(s.y0+t*dy)) <= w {
			return true
		}
	}
	return false
}

func inRoundRect(x, y, x0, y0, x1, y1, r float64) bool {
	if x < x0 || x > x1 || y < y0 || y > y1 {
		return false
	}
	cx := math.Max(x0+r, math.Min(x1-r, x))
	cy := math.Max(y0+r, math.Min(y1-r, y))
	return math.Hypot(x-cx, y-cy) <= r
}
