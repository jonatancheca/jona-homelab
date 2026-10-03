//go:build windows

package main

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// Keep these colors aligned with the dark theme in app/assets/main.css.
const (
	trayBackground = 0x101713
	traySurface    = 0x16201b
	trayRaised     = 0x1b2821
	trayInput      = 0x101913
	trayBorder     = 0x2a3b31
	trayStrong     = 0x3b5545
	trayText       = 0xe5eee8
	trayMuted      = 0xa4b5a9
	trayPrimary    = 0x3fa86c
	trayHover      = 0x50be7d

	wmPaint        = 0x000f
	wmEraseBkgnd   = 0x0014
	wmDrawItem     = 0x002b
	wmCtlColorEdit = 0x0133
	wmMouseMove    = 0x0200
	wmMouseLeave   = 0x02a3
	wmNCDestroy    = 0x0082
	wsClipChildren = 0x02000000
	ssOwnerDraw    = 0x0000000d
	bsOwnerDraw    = 0x0000000b
)

var (
	procBeginPaint                   = user32.NewProc("BeginPaint")
	procEndPaint                     = user32.NewProc("EndPaint")
	procFillRect                     = user32.NewProc("FillRect")
	procDrawText                     = user32.NewProc("DrawTextW")
	procGetWindowText                = user32.NewProc("GetWindowTextW")
	procInvalidateRect               = user32.NewProc("InvalidateRect")
	procGetFocus                     = user32.NewProc("GetFocus")
	procIsDialogMessage              = user32.NewProc("IsDialogMessageW")
	procTrackMouseEvent              = user32.NewProc("TrackMouseEvent")
	procGetDpiForSystem              = user32.NewProc("GetDpiForSystem")
	procSetThreadDpiAwarenessContext = user32.NewProc("SetThreadDpiAwarenessContext")
	procAdjustWindowRectExForDpi     = user32.NewProc("AdjustWindowRectExForDpi")
	procCreateSolidBrush             = gdi32.NewProc("CreateSolidBrush")
	procCreatePen                    = gdi32.NewProc("CreatePen")
	procCreateFont                   = gdi32.NewProc("CreateFontW")
	procSelectObject                 = gdi32.NewProc("SelectObject")
	procDeleteObject                 = gdi32.NewProc("DeleteObject")
	procSetBkColor                   = gdi32.NewProc("SetBkColor")
	procRoundRect                    = gdi32.NewProc("RoundRect")
	procDwmSetWindowAttribute        = windows.NewLazySystemDLL("dwmapi.dll").NewProc("DwmSetWindowAttribute")
	comctl32                         = windows.NewLazySystemDLL("comctl32.dll")
	procSetWindowSubclass            = comctl32.NewProc("SetWindowSubclass")
	procRemoveWindowSubclass         = comctl32.NewProc("RemoveWindowSubclass")
	procDefSubclassProc              = comctl32.NewProc("DefSubclassProc")
	trayButtonCallback               uintptr
	themedButtons                    = make(map[windows.HWND]*trayTheme)
)

func init() { trayButtonCallback = windows.NewCallback(trayButtonProc) }

type nativeRect struct{ Left, Top, Right, Bottom int32 }

type nativePaint struct {
	DC        uintptr
	Erase     int32
	Rect      nativeRect
	Restore   int32
	IncUpdate int32
	Reserved  [32]byte
}

type nativeDrawItem struct {
	Type, ID, Item, Action, State uint32
	Window                        windows.HWND
	DC                            uintptr
	Rect                          nativeRect
	Data                          uintptr
}

type nativeTrackMouse struct {
	Size, Flags uint32
	Window      windows.HWND
	HoverTime   uint32
}

type trayStatus uint8

const (
	trayStatusPending trayStatus = iota
	trayStatusConnected
	trayStatusError
)

type trayControlStyle struct {
	foreground, background uint32
	primary                bool
}

type trayTheme struct {
	dpi                                        int
	body, title, heading, caption, small, mono uintptr
	brushes                                    map[uint32]uintptr
	controls                                   map[windows.HWND]trayControlStyle
	hover                                      windows.HWND
	status                                     trayStatus
}

func (s *trayTheme) init() {
	dpi, _, _ := procGetDpiForSystem.Call()
	s.initDPI(int(dpi))
}

func (s *trayTheme) initDPI(dpi int) {
	s.dpi = dpi
	if s.dpi == 0 {
		s.dpi = 96
	}
	s.brushes = make(map[uint32]uintptr)
	s.controls = make(map[windows.HWND]trayControlStyle)
	s.body = s.font("Segoe UI", 14, 400)
	s.title = s.font("Segoe UI", 28, 600)
	s.heading = s.font("Segoe UI", 18, 600)
	s.caption = s.font("Segoe UI", 11, 600)
	s.small = s.font("Segoe UI", 12, 400)
	s.mono = s.font("Consolas", 14, 400)
}

func (s *trayTheme) px(value int) int { return (value*s.dpi + 48) / 96 }

// Win32 COLORREF uses BGR byte order, unlike the web's RGB tokens.
func colorRef(rgb uint32) uintptr {
	return uintptr((rgb&0xff)<<16 | rgb&0xff00 | (rgb>>16)&0xff)
}

func (s *trayTheme) brush(color uint32) uintptr {
	if brush, ok := s.brushes[color]; ok {
		return brush
	}
	brush, _, _ := procCreateSolidBrush.Call(colorRef(color))
	s.brushes[color] = brush
	return brush
}

func (s *trayTheme) font(family string, size, weight int) uintptr {
	font, _, _ := procCreateFont.Call(uintptr(-s.px(size)), 0, 0, 0, uintptr(weight), 0, 0, 0, 1, 0, 0, 5, 0, uintptr(unsafe.Pointer(utf16(family))))
	if font == 0 {
		return stockFont()
	}
	return font
}

func (s *trayTheme) close() {
	for _, brush := range s.brushes {
		procDeleteObject.Call(brush)
	}
	for _, font := range []uintptr{s.body, s.title, s.heading, s.caption, s.small, s.mono} {
		if font != 0 && font != stockFont() {
			procDeleteObject.Call(font)
		}
	}
}

func (s *trayTheme) applyCaption(hwnd windows.HWND) {
	// Unsupported attributes are harmless on older Windows versions.
	for attribute, value := range map[uint32]uint32{20: 1, 35: uint32(colorRef(trayBackground)), 36: uint32(colorRef(trayMuted))} {
		procDwmSetWindowAttribute.Call(uintptr(hwnd), uintptr(attribute), uintptr(unsafe.Pointer(&value)), 4)
	}
}

func (t *trayApplication) label(text string, x, y, width, height int, font uintptr, foreground, background uint32) windows.HWND {
	control := t.newControl("STATIC", text, wsChild|wsVisible|0x0000c080, 0, x, y, width, height, 0) // End ellipsis, no mnemonic prefix.
	t.theme.controls[control] = trayControlStyle{foreground: foreground, background: background}
	procSendMessage.Call(uintptr(control), wmSetFont, font, 1)
	return control
}

func (t *trayApplication) button(text string, x, y, width, height, id int, background uint32) windows.HWND {
	control := t.newControl("BUTTON", text, wsChild|wsVisible|wsTabStop|bsOwnerDraw, 0, x, y, width, height, id)
	t.theme.controls[control] = trayControlStyle{foreground: trayText, background: background, primary: id == idCopy}
	themedButtons[control] = &t.theme
	procSetWindowSubclass.Call(uintptr(control), trayButtonCallback, 1, 0)
	return control
}

func trayButtonProc(hwnd windows.HWND, message uint32, wParam, lParam, id, _ uintptr) uintptr {
	if theme := themedButtons[hwnd]; theme != nil {
		switch message {
		case 0x00f4: // BM_SETSTYLE: keep owner drawing when the dialog changes its default button.
			wParam = bsOwnerDraw
		case wmMouseMove:
			if theme.hover != hwnd {
				theme.hover = hwnd
				track := nativeTrackMouse{Size: uint32(unsafe.Sizeof(nativeTrackMouse{})), Flags: 2, Window: hwnd}
				procTrackMouseEvent.Call(uintptr(unsafe.Pointer(&track)))
				procInvalidateRect.Call(uintptr(hwnd), 0, 0)
			}
		case wmMouseLeave:
			if theme.hover == hwnd {
				theme.hover = 0
			}
			procInvalidateRect.Call(uintptr(hwnd), 0, 0)
		case wmNCDestroy:
			procRemoveWindowSubclass.Call(uintptr(hwnd), trayButtonCallback, id)
			delete(themedButtons, hwnd)
		}
	}
	result, _, _ := procDefSubclassProc.Call(uintptr(hwnd), uintptr(message), wParam, lParam)
	if message == 0x0087 { // WM_GETDLGCODE: Enter activates the focused owner-drawn button.
		if focus, _, _ := procGetFocus.Call(); focus == uintptr(hwnd) {
			result |= 0x10 // DLGC_DEFPUSHBUTTON
		} else {
			result |= 0x20 // DLGC_UNDEFPUSHBUTTON
		}
	}
	return result
}

func (t *trayApplication) setStatus(text string, status trayStatus) {
	t.theme.status = status
	setWindowText(t.status, text)
	procInvalidateRect.Call(uintptr(t.status), 0, 0)
}

func (s *trayTheme) rect(x, y, width, height int) nativeRect {
	return nativeRect{int32(s.px(x)), int32(s.px(y)), int32(s.px(x + width)), int32(s.px(y + height))}
}

func (s *trayTheme) fill(dc uintptr, rect nativeRect, color uint32) {
	procFillRect.Call(dc, uintptr(unsafe.Pointer(&rect)), s.brush(color))
}

func (s *trayTheme) round(dc uintptr, rect nativeRect, fill, border uint32, radius int) {
	pen, _, _ := procCreatePen.Call(0, uintptr(s.px(1)), colorRef(border))
	oldPen, _, _ := procSelectObject.Call(dc, pen)
	oldBrush, _, _ := procSelectObject.Call(dc, s.brush(fill))
	procRoundRect.Call(dc, uintptr(rect.Left), uintptr(rect.Top), uintptr(rect.Right), uintptr(rect.Bottom), uintptr(s.px(radius*2)), uintptr(s.px(radius*2)))
	procSelectObject.Call(dc, oldPen)
	procSelectObject.Call(dc, oldBrush)
	procDeleteObject.Call(pen)
}

func (t *trayApplication) paint() {
	var paint nativePaint
	dc, _, _ := procBeginPaint.Call(uintptr(t.hwnd), uintptr(unsafe.Pointer(&paint)))
	defer procEndPaint.Call(uintptr(t.hwnd), uintptr(unsafe.Pointer(&paint)))
	s := &t.theme
	s.fill(dc, paint.Rect, trayBackground)
	s.round(dc, s.rect(32, 176, 596, 250), traySurface, trayBorder, 10)
	s.round(dc, s.rect(32, 438, 596, 118), traySurface, trayBorder, 10)
	border := uint32(trayStrong)
	if focus, _, _ := procGetFocus.Call(); focus == uintptr(t.code) {
		border = trayPrimary
	}
	s.round(dc, s.rect(56, 284, 548, 44), trayInput, border, 7)
	// The same two-server emblem used by the web's device cards.
	s.round(dc, s.rect(32, 30, 46, 46), 0x1d3327, 0x385945, 10)
	for _, y := range []int{42, 56} {
		s.round(dc, s.rect(43, y, 24, 9), 0x1d3327, 0x8bcda1, 1)
		s.fill(dc, s.rect(61, y+3, 3, 3), 0x8bcda1)
	}
}

func (t *trayApplication) drawControl(item *nativeDrawItem) {
	s := &t.theme
	fill, border, foreground := uint32(trayRaised), uint32(trayStrong), uint32(trayText)
	background := s.controls[item.Window].background
	textRect := item.Rect
	if item.Window == t.status {
		background = trayBackground
		fill, border, foreground = 0x1c2821, trayBorder, trayMuted
		if s.status == trayStatusConnected {
			fill, border, foreground = 0x1c3828, 0x356347, 0x8cdaa5
		} else if s.status == trayStatusError {
			fill, border, foreground = 0x382322, 0x613a35, 0xef9a90
		}
	} else {
		if s.controls[item.Window].primary {
			fill, border, foreground = trayPrimary, trayPrimary, 0x08140d
		}
		if item.State&0x4 != 0 { // ODS_DISABLED
			fill, border, foreground = trayRaised, trayBorder, 0x7d9183
		} else if item.State&0x1 != 0 { // ODS_SELECTED
			fill = 0x274b34
			foreground = trayText
		} else if s.hover == item.Window {
			fill, border = 0x24362b, 0x5b8067
			if s.controls[item.Window].primary {
				fill, border = trayHover, trayHover
			}
		}
	}
	s.fill(item.DC, item.Rect, background)
	radius := 7
	if item.Window == t.status {
		radius = 15
	}
	s.round(item.DC, item.Rect, fill, border, radius)
	if item.State&0x10 != 0 && item.Window != t.status { // ODS_FOCUS
		rect := item.Rect
		inset := int32(s.px(3))
		rect.Left += inset
		rect.Top += inset
		rect.Right -= inset
		rect.Bottom -= inset
		s.round(item.DC, rect, fill, trayText, 5)
	}
	flags := uintptr(0x20 | 0x4 | 0x800 | 0x8000) // Single line, centered vertically, no prefix, ellipsis.
	if item.Window == t.status {
		s.round(item.DC, s.rect(13, 12, 6, 6), foreground, foreground, 3)
		textRect.Left += int32(s.px(28))
		textRect.Right -= int32(s.px(10))
	} else {
		flags |= 1 // Center horizontally.
	}
	var text [512]uint16
	length, _, _ := procGetWindowText.Call(uintptr(item.Window), uintptr(unsafe.Pointer(&text[0])), uintptr(len(text)))
	oldFont, _, _ := procSelectObject.Call(item.DC, s.body)
	procSetTextColor.Call(item.DC, colorRef(foreground))
	procSetBkMode.Call(item.DC, transparent)
	procDrawText.Call(item.DC, uintptr(unsafe.Pointer(&text[0])), length, uintptr(unsafe.Pointer(&textRect)), flags)
	procSelectObject.Call(item.DC, oldFont)
}
