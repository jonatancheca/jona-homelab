//go:build windows

package main

import (
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"math"
	"runtime"
	"unsafe"
)

var (
	procCreateIconFromResource = user32.NewProc("CreateIconFromResourceEx")
	procDestroyIcon            = user32.NewProc("DestroyIcon")
	procGetSystemMetricsForDpi = user32.NewProc("GetSystemMetricsForDpi")
	procRegisterWindowMessage  = user32.NewProc("RegisterWindowMessageW")
	procUnregisterClass        = user32.NewProc("UnregisterClassW")
)

type companionIcons struct{ small, large uintptr }

func (icons *companionIcons) init(dpi int) error {
	for _, target := range []struct {
		metric uintptr
		handle *uintptr
	}{{49, &icons.small}, {11, &icons.large}} {
		size, _, _ := procGetSystemMetricsForDpi.Call(target.metric, uintptr(dpi))
		if size == 0 {
			size = 32
		}
		pixels := companionIconImage(int(size))
		// RT_ICON contains a bottom-up 32-bit DIB followed by a DWORD-aligned AND mask.
		maskStride := ((int(size) + 31) / 32) * 4
		data := make([]byte, 40+int(size*size)*4+maskStride*int(size))
		binary.LittleEndian.PutUint32(data, 40)
		binary.LittleEndian.PutUint32(data[4:], uint32(size))
		binary.LittleEndian.PutUint32(data[8:], uint32(size)*2)
		binary.LittleEndian.PutUint16(data[12:], 1)
		binary.LittleEndian.PutUint16(data[14:], 32)
		for y := 0; y < int(size); y++ {
			for x := 0; x < int(size); x++ {
				c := pixels.RGBAAt(x, y)
				offset := 40 + ((int(size)-1-y)*int(size)+x)*4
				copy(data[offset:], []byte{c.B, c.G, c.R, c.A})
				if c.A == 0 {
					data[40+int(size*size)*4+(int(size)-1-y)*maskStride+x/8] |= 0x80 >> (x % 8)
				}
			}
		}
		handle, _, err := procCreateIconFromResource.Call(uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)), 1, 0x00030000, size, size, 0)
		runtime.KeepAlive(data)
		if handle == 0 {
			icons.close()
			return fmt.Errorf("create Companion icon: %w", err)
		}
		*target.handle = handle
	}
	return nil
}

func (icons *companionIcons) close() {
	for _, handle := range []uintptr{icons.small, icons.large} {
		if handle != 0 {
			procDestroyIcon.Call(handle)
		}
	}
	icons.small, icons.large = 0, 0
}

// Render the Companion's two-server emblem at the actual Windows icon size.
// Four samples per axis keep rounded edges clear at 100%, 150% and 200% DPI.
func companionIconImage(size int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	inside := func(x, y, left, top, right, bottom, radius float64) bool {
		dx := math.Max(math.Max(left+radius-x, x-right+radius), 0)
		dy := math.Max(math.Max(top+radius-y, y-bottom+radius), 0)
		return x >= left && x <= right && y >= top && y <= bottom && dx*dx+dy*dy <= radius*radius
	}
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var r, g, b, a uint32
			for sy := 0; sy < 4; sy++ {
				for sx := 0; sx < 4; sx++ {
					px, py := (float64(x)+(float64(sx)+0.5)/4)*32/float64(size), (float64(y)+(float64(sy)+0.5)/4)*32/float64(size)
					if !inside(px, py, 1, 1, 31, 31, 7) {
						continue
					}
					c := uint32(0x4d9365)
					if inside(px, py, 2, 2, 30, 30, 6) {
						c = 0x193325
					}
					for _, top := range []float64{8, 18} {
						if inside(px, py, 7, top, 25, top+6, 1) && !inside(px, py, 8.5, top+1.5, 23.5, top+4.5, 0.3) {
							c = 0xa4e6bf
						}
						if inside(px, py, 20.5, top+2.2, 22.2, top+3.8, 0.3) {
							c = 0xa4e6bf
						}
					}
					r += (c >> 16) & 255
					g += (c >> 8) & 255
					b += c & 255
					a += 255
				}
			}
			img.SetRGBA(x, y, color.RGBA{uint8(r / 16), uint8(g / 16), uint8(b / 16), uint8(a / 16)})
		}
	}
	return img
}
