//go:build ignore

// gen_icons.go regenerates the dock's icon assets: a monochrome compass rose
// (placeholder until an OpenBridge asset replaces it) as 32px tray glyphs — PNG
// for macOS/Linux, PNG-in-ICO for Windows, plus an error-badged variant — and a
// 512px appicon.png that packaging turns into the macOS AppIcon.icns and the
// Linux .desktop icon. Run with `go generate ./cmd/dock`.
package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
)

func glyph(size int, badge bool) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	s := float64(size) / 32 // all geometry is designed at 32px and scaled
	c := float64(size) / 2
	set := func(x, y int, a float64) {
		if a <= 0 {
			return
		}
		if a > 1 {
			a = 1
		}
		img.SetNRGBA(x, y, color.NRGBA{0, 0, 0, uint8(a * 255)})
	}
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			fx, fy := float64(x)+0.5-c, float64(y)+0.5-c
			r := math.Hypot(fx, fy)
			// Outer ring at radius 13.5, softened for AA.
			a := 1.5 - math.Abs(r-13.5*s)/(1.1*s)
			// Four-point star: pinched diamond |x·y| ≤ k, |x|+|y| ≤ L.
			if math.Abs(fx*fy) <= 14*s*s && math.Abs(fx)+math.Abs(fy) <= 11*s {
				a = 1
			}
			set(x, y, a)
		}
	}
	if badge {
		// Filled disc, bottom-right, with a punched-out gap so the badge reads.
		for y := 0; y < size; y++ {
			for x := 0; x < size; x++ {
				d := math.Hypot(float64(x)-25.5*s, float64(y)-25.5*s)
				switch {
				case d <= 5.5*s:
					img.SetNRGBA(x, y, color.NRGBA{0, 0, 0, 255})
				case d <= 7.5*s:
					img.SetNRGBA(x, y, color.NRGBA{})
				}
			}
		}
	}
	return img
}

func encode(size int, badge bool) []byte {
	var b bytes.Buffer
	if err := png.Encode(&b, glyph(size, badge)); err != nil {
		panic(err)
	}
	return b.Bytes()
}

// ico wraps one PNG in a single-image ICO container (valid since Vista).
func ico(pngData []byte, size int) []byte {
	var b bytes.Buffer
	binary.Write(&b, binary.LittleEndian, []uint16{0, 1, 1})    // ICONDIR
	b.Write([]byte{byte(size), byte(size), 0, 0})               // w, h, colors, reserved
	binary.Write(&b, binary.LittleEndian, []uint16{1, 32})      // planes, bpp
	binary.Write(&b, binary.LittleEndian, uint32(len(pngData))) // data size
	binary.Write(&b, binary.LittleEndian, uint32(6+16))         // data offset
	b.Write(pngData)
	return b.Bytes()
}

func write(name string, data []byte) {
	if err := os.WriteFile(name, data, 0o644); err != nil {
		panic(err)
	}
}

func main() {
	tray := encode(32, false)
	trayErr := encode(32, true)
	write("icon.png", tray)
	write("icon.ico", ico(tray, 32))
	write("icon_error.png", trayErr)
	write("icon_error.ico", ico(trayErr, 32))
	write("appicon.png", encode(512, false))
}
