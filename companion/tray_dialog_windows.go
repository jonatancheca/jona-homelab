//go:build windows

package main

import (
	"fmt"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	wmInitDialog   = 0x0110
	idDialogOK     = 1
	idDialogCancel = 2
	dialogWarning  = 1
	dialogError    = 2
)

var (
	procDialogBoxIndirect = user32.NewProc("DialogBoxIndirectParamW")
	procEndDialog         = user32.NewProc("EndDialog")
	procSetFocus          = user32.NewProc("SetFocus")
	procGetWindowRect     = user32.NewProc("GetWindowRect")
	procSetWindowPos      = user32.NewProc("SetWindowPos")
	procGetDC             = user32.NewProc("GetDC")
	procReleaseDC         = user32.NewProc("ReleaseDC")
	procMonitorFromWindow = user32.NewProc("MonitorFromWindow")
	procGetMonitorInfo    = user32.NewProc("GetMonitorInfoW")
	procMoveToEx          = gdi32.NewProc("MoveToEx")
	procLineTo            = gdi32.NewProc("LineTo")
	dialogCallback        = windows.NewCallback(companionDialogProc)
	openingDialogs        = make(map[uintptr]*companionDialog)
	dialogWindows         = make(map[windows.HWND]*companionDialog)
	nextDialogID          uintptr
)

type dialogContent struct {
	title, body, confirm string
	tone                 int
}

// The three WORDs after DLGTEMPLATE specify no menu, default dialog class and
// an empty caption. Controls and pixel dimensions are set in WM_INITDIALOG.
type emptyDialogTemplate struct {
	Style, Extended     uint32
	Count               uint16
	X, Y, Width, Height int16
	Menu, Class, Title  uint16
}

type nativeMonitorInfo struct {
	Size          uint32
	Monitor, Work nativeRect
	Flags         uint32
}

type companionDialog struct {
	view                      trayApplication
	owner                     *trayApplication
	content                   dialogContent
	width, height, cardHeight int
	initErr                   error
}

func updateDialogContent(result updateCheckResult) dialogContent {
	if result.LocalBuild {
		return dialogContent{title: "Manual updates", body: updateCheckMessage(result), tone: dialogWarning}
	}
	if result.Scheduled {
		return dialogContent{title: "Update requested", body: "Companion is preparing the update. Only its service will restart.\nUse Refresh to check progress or errors; installation is not yet confirmed."}
	}
	return dialogContent{title: "Already up to date", body: "The latest version of Companion is installed.\nNo update is needed."}
}

func (t *trayApplication) showDialog(content dialogContent) bool {
	if t.dialog != 0 {
		procSetForegroundWindow.Call(uintptr(t.dialog))
		return false
	}
	d := companionDialog{owner: t, content: content}
	result, err := d.run()
	if err != nil {
		// Keep an emergency path if Windows cannot allocate the themed dialog.
		messageBox(t.hwnd, content.body, content.title, mbOK|mbIconError)
		return false
	}
	return result == idYes
}

func (d *companionDialog) run() (int32, error) {
	if d.owner.theme.dpi != 0 {
		d.view.theme.initDPI(d.owner.theme.dpi)
	} else {
		d.view.theme.init()
	}
	defer d.view.theme.close()
	d.view.font = d.view.theme.body
	d.view.instance = d.owner.instance
	nextDialogID++
	id := nextDialogID
	openingDialogs[id] = d
	defer delete(openingDialogs, id)
	template := emptyDialogTemplate{Style: 0x80000000 | wsCaption | wsSysMenu | wsClipChildren | 0x80, Width: 320, Height: 180}
	result, _, err := procDialogBoxIndirect.Call(d.view.instance, uintptr(unsafe.Pointer(&template)), uintptr(d.owner.hwnd), dialogCallback, id)
	runtime.KeepAlive(&template)
	if d.initErr != nil {
		return idDialogCancel, d.initErr
	}
	if int32(result) == -1 || result == 0 {
		return idDialogCancel, fmt.Errorf("create Companion dialog: %w", err)
	}
	return int32(result), nil
}

func companionDialogProc(hwnd windows.HWND, message uint32, wParam, lParam uintptr) uintptr {
	if message == wmInitDialog {
		d := openingDialogs[lParam]
		if d == nil {
			return 0
		}
		dialogWindows[hwnd] = d
		d.view.hwnd = hwnd
		d.owner.dialog = hwnd
		if d.initErr = d.init(); d.initErr != nil {
			procEndDialog.Call(uintptr(hwnd), idDialogCancel)
		}
		return 0 // init sets keyboard focus explicitly.
	}
	d := dialogWindows[hwnd]
	if d == nil {
		return 0
	}
	switch message {
	case wmCommand:
		if highWord(wParam) != 0 {
			return 0
		}
		id := int(lowWord(wParam))
		if id == idDialogCancel || id == idDialogOK || (id == idYes && d.content.confirm != "") {
			procEndDialog.Call(uintptr(hwnd), uintptr(id))
			return 1
		}
	case wmClose:
		procEndDialog.Call(uintptr(hwnd), idDialogCancel)
		return 1
	case wmPaint:
		var paint nativePaint
		dc, _, _ := procBeginPaint.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&paint)))
		d.paint(dc)
		procEndPaint.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&paint)))
		return 1
	case wmEraseBkgnd:
		return 1
	case wmDrawItem:
		var item nativeDrawItem
		procRtlMoveMemory.Call(uintptr(unsafe.Pointer(&item)), lParam, unsafe.Sizeof(item))
		d.view.drawControl(&item)
		return 1
	case wmCtlColorStatic, wmCtlColorEdit:
		style := d.view.theme.controls[windows.HWND(lParam)]
		procSetTextColor.Call(wParam, colorRef(style.foreground))
		procSetBkColor.Call(wParam, colorRef(style.background))
		return d.view.theme.brush(style.background)
	case wmDestroy:
		d.owner.dialog = 0
		delete(dialogWindows, hwnd)
	}
	return 0
}

func (d *companionDialog) init() error {
	v, s := &d.view, &d.view.theme
	setWindowText(v.hwnd, d.content.title+" · Companion")
	s.applyCaption(v.hwnd)
	procSendMessage.Call(uintptr(v.hwnd), 0x0080, 0, d.owner.icons.small) // WM_SETICON / ICON_SMALL
	procSendMessage.Call(uintptr(v.hwnd), 0x0080, 1, d.owner.icons.large)
	monitor, _, _ := procMonitorFromWindow.Call(uintptr(d.owner.hwnd), 2)
	info := nativeMonitorInfo{Size: uint32(unsafe.Sizeof(nativeMonitorInfo{}))}
	if ok, _, err := procGetMonitorInfo.Call(monitor, uintptr(unsafe.Pointer(&info))); ok == 0 {
		return fmt.Errorf("get dialog monitor: %w", err)
	}
	logical := func(px int32) int { return int(px) * 96 / s.dpi }
	d.width = min(520, logical(info.Work.Right-info.Work.Left)-32)
	dc, _, _ := procGetDC.Call(uintptr(v.hwnd))
	if dc == 0 {
		return fmt.Errorf("get dialog drawing context")
	}
	textRect := s.rect(0, 0, d.width-88, 0)
	text := utf16(d.content.body)
	oldFont, _, _ := procSelectObject.Call(dc, s.body)
	procDrawText.Call(dc, uintptr(unsafe.Pointer(text)), ^uintptr(0), uintptr(unsafe.Pointer(&textRect)), 0x400|0x10|0x800)
	procSelectObject.Call(dc, oldFont)
	procReleaseDC.Call(uintptr(v.hwnd), dc)
	bodyHeight := max(44, (int(textRect.Bottom)*96+s.dpi-1)/s.dpi)
	availableBody := max(44, min(320, logical(info.Work.Bottom-info.Work.Top)-300))
	d.cardHeight = min(bodyHeight, availableBody) + 40
	d.height = 104 + d.cardHeight + 88
	outer := s.rect(0, 0, d.width, d.height)
	procAdjustWindowRectExForDpi.Call(uintptr(unsafe.Pointer(&outer)), 0x80000000|wsCaption|wsSysMenu|wsClipChildren, 0, 1, uintptr(s.dpi))
	width, height := outer.Right-outer.Left, outer.Bottom-outer.Top
	center := info.Work
	if d.owner.hwnd != 0 {
		procGetWindowRect.Call(uintptr(d.owner.hwnd), uintptr(unsafe.Pointer(&center)))
	}
	x := max(info.Work.Left, min(center.Left+(center.Right-center.Left-width)/2, info.Work.Right-width))
	y := max(info.Work.Top, min(center.Top+(center.Bottom-center.Top-height)/2, info.Work.Bottom-height))
	procSetWindowPos.Call(uintptr(v.hwnd), 0, uintptr(x), uintptr(y), uintptr(width), uintptr(height), 0x14)
	v.label("JONA HOMELAB", 92, 28, d.width-116, 18, s.caption, trayMuted, trayBackground)
	v.label(d.content.title, 90, 49, d.width-114, 30, s.heading, trayText, trayBackground)
	class, style := "STATIC", uint32(wsChild|wsVisible|0x80)
	if bodyHeight > availableBody {
		class, style = "EDIT", wsChild|wsVisible|wsTabStop|0x00200000|esReadOnly|0x4|0x40
	}
	bodyText := d.content.body
	if class == "EDIT" {
		bodyText = strings.ReplaceAll(strings.ReplaceAll(bodyText, "\r\n", "\n"), "\n", "\r\n")
	}
	body := v.newControl(class, bodyText, style, 0, 44, 124, d.width-88, d.cardHeight-40, 0)
	s.controls[body] = trayControlStyle{foreground: trayText, background: traySurface}
	v.label("Companion", 24, d.height-48, 160, 20, s.small, trayMuted, trayBackground)
	primaryID, primaryText := idDialogOK, "Got it"
	if d.content.confirm != "" {
		primaryID, primaryText = idYes, d.content.confirm
	}
	primary := v.button(primaryText, d.width-148, d.height-64, 124, 40, primaryID, trayBackground)
	buttonStyle := s.controls[primary]
	buttonStyle.primary = true
	s.controls[primary] = buttonStyle
	focus, defaultID := primary, primaryID
	if d.content.confirm != "" {
		focus = v.button("Cancel", d.width-260, d.height-64, 100, 40, idDialogCancel, trayBackground)
		defaultID = idDialogCancel // Enter never rotates a code without choosing the action.
	}
	procSendMessage.Call(uintptr(v.hwnd), 0x0401, uintptr(defaultID), 0) // DM_SETDEFID
	procSetFocus.Call(uintptr(focus))
	return nil
}

func (d *companionDialog) paint(dc uintptr) {
	s := &d.view.theme
	s.fill(dc, s.rect(0, 0, d.width, d.height), trayBackground)
	s.round(dc, s.rect(24, 104, d.width-48, d.cardHeight), traySurface, trayBorder, 10)
	s.fill(dc, s.rect(24, d.height-80, d.width-48, 1), trayBorder)
	background, border, foreground := uint32(0x1c3828), uint32(0x356347), uint32(0x8cdaa5)
	if d.content.tone == dialogWarning {
		background, border, foreground = 0x362d1c, 0x695533, 0xe9c57f
	}
	if d.content.tone == dialogError {
		background, border, foreground = 0x382322, 0x613a35, 0xef9a90
	}
	s.round(dc, s.rect(24, 28, 48, 48), background, border, 12)
	pen, _, _ := procCreatePen.Call(0, uintptr(s.px(2)), colorRef(foreground))
	oldPen, _, _ := procSelectObject.Call(dc, pen)
	line := func(x1, y1, x2, y2 int) {
		procMoveToEx.Call(dc, uintptr(s.px(x1)), uintptr(s.px(y1)), 0)
		procLineTo.Call(dc, uintptr(s.px(x2)), uintptr(s.px(y2)))
	}
	switch d.content.tone {
	case dialogWarning:
		line(48, 40, 48, 54)
		s.round(dc, s.rect(47, 59, 3, 3), foreground, foreground, 1)
	case dialogError:
		line(40, 44, 56, 60)
		line(56, 44, 40, 60)
	default:
		line(37, 52, 45, 59)
		line(45, 59, 59, 43)
	}
	procSelectObject.Call(dc, oldPen)
	procDeleteObject.Call(pen)
}
