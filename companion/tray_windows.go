//go:build windows

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"runtime"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	windowClassName  = "JonaHomelabCompanionTrayWindow"
	wmCreate         = 0x0001
	wmDestroy        = 0x0002
	wmClose          = 0x0010
	wmContextMenu    = 0x007B
	wmCommand        = 0x0111
	wmSetFont        = 0x0030
	wmCtlColorStatic = 0x0138
	wmAppResult      = 0x8001
	wmTrayMessage    = 0x8002
	wmNull           = 0x0000
	wmLButtonDown    = 0x0201
	wmLButtonDblClk  = 0x0203
	wmRButtonDown    = 0x0204
	wmRButtonUp      = 0x0205
	ninSelect        = 0x0400
	ninKeySelect     = 0x0401

	wsCaption     = 0x00C00000
	wsSysMenu     = 0x00080000
	wsMinimizeBox = 0x00020000
	wsChild       = 0x40000000
	wsVisible     = 0x10000000
	wsTabStop     = 0x00010000

	esReadOnly    = 0x0800
	esAutoHScroll = 0x0080

	swHide = 0
	swShow = 5

	mbOK          = 0x00000000
	mbYesNo       = 0x00000004
	mbIconError   = 0x00000010
	mbIconWarning = 0x00000030
	idYes         = 6

	transparent    = 1
	defaultGuiFont = 17

	nifMessage         = 0x00000001
	nifIcon            = 0x00000002
	nifTip             = 0x00000004
	nimAdd             = 0x00000000
	nimDelete          = 0x00000002
	nimSetFocus        = 0x00000003
	nimSetVersion      = 0x00000004
	notifyIconVersion4 = 4

	mfString       = 0x00000000
	mfSeparator    = 0x00000800
	tpmRightButton = 0x00000002

	cfUnicodeText = 13
	gmemMoveable  = 0x00000002

	idCopy    = 1001
	idRotate  = 1002
	idRefresh = 1003
	idUpdate  = 1004
	idExit    = 1005
	idCode    = 1006
)

type trayEventKind uint8

const (
	trayEventIgnored trayEventKind = iota
	trayEventShow
	trayEventMenu
)

var (
	user32                  = windows.NewLazySystemDLL("user32.dll")
	shell32                 = windows.NewLazySystemDLL("shell32.dll")
	kernel32                = windows.NewLazySystemDLL("kernel32.dll")
	gdi32                   = windows.NewLazySystemDLL("gdi32.dll")
	procRegisterClassEx     = user32.NewProc("RegisterClassExW")
	procCreateWindowEx      = user32.NewProc("CreateWindowExW")
	procDefWindowProc       = user32.NewProc("DefWindowProcW")
	procShowWindow          = user32.NewProc("ShowWindow")
	procUpdateWindow        = user32.NewProc("UpdateWindow")
	procDestroyWindow       = user32.NewProc("DestroyWindow")
	procGetMessage          = user32.NewProc("GetMessageW")
	procTranslateMessage    = user32.NewProc("TranslateMessage")
	procDispatchMessage     = user32.NewProc("DispatchMessageW")
	procPostQuitMessage     = user32.NewProc("PostQuitMessage")
	procPostMessage         = user32.NewProc("PostMessageW")
	procSetWindowText       = user32.NewProc("SetWindowTextW")
	procSendMessage         = user32.NewProc("SendMessageW")
	procEnableWindow        = user32.NewProc("EnableWindow")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procGetCursorPos        = user32.NewProc("GetCursorPos")
	procCreatePopupMenu     = user32.NewProc("CreatePopupMenu")
	procAppendMenu          = user32.NewProc("AppendMenuW")
	procTrackPopupMenu      = user32.NewProc("TrackPopupMenu")
	procDestroyMenu         = user32.NewProc("DestroyMenu")
	procMessageBox          = user32.NewProc("MessageBoxW")
	procOpenClipboard       = user32.NewProc("OpenClipboard")
	procCloseClipboard      = user32.NewProc("CloseClipboard")
	procEmptyClipboard      = user32.NewProc("EmptyClipboard")
	procSetClipboardData    = user32.NewProc("SetClipboardData")
	procLoadIcon            = user32.NewProc("LoadIconW")
	procLoadCursor          = user32.NewProc("LoadCursorW")
	procShellNotifyIcon     = shell32.NewProc("Shell_NotifyIconW")
	procGetModuleHandle     = kernel32.NewProc("GetModuleHandleW")
	procGlobalAlloc         = kernel32.NewProc("GlobalAlloc")
	procGlobalLock          = kernel32.NewProc("GlobalLock")
	procGlobalUnlock        = kernel32.NewProc("GlobalUnlock")
	procGlobalFree          = kernel32.NewProc("GlobalFree")
	procRtlMoveMemory       = kernel32.NewProc("RtlMoveMemory")
	procSetTextColor        = gdi32.NewProc("SetTextColor")
	procSetBkMode           = gdi32.NewProc("SetBkMode")
	procGetStockObject      = gdi32.NewProc("GetStockObject")
)

type nativePoint struct{ X, Y int32 }

type nativeMessage struct {
	Hwnd    windows.HWND
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Point   nativePoint
}

type nativeClass struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   uintptr
	ClassName  uintptr
	SmallIcon  uintptr
}

type notifyIconData struct {
	Size        uint32
	Window      windows.HWND
	ID          uint32
	Flags       uint32
	Callback    uint32
	Icon        uintptr
	Tip         [128]uint16
	State       uint32
	StateMask   uint32
	Info        [256]uint16
	Timeout     uint32
	InfoTitle   [64]uint16
	InfoFlags   uint32
	GUID        windows.GUID
	BalloonIcon uintptr
}

type trayResult struct {
	action string
	info   pipeInfo
	update updateCheckResult
	err    error
}

type trayApplication struct {
	hwnd     windows.HWND
	instance uintptr
	icon     notifyIconData
	font     uintptr
	theme    trayTheme
	status   windows.HWND
	code     windows.HWND
	lastCall windows.HWND
	details  windows.HWND
	version  windows.HWND
	copy     windows.HWND
	rotate   windows.HWND
	refresh  windows.HWND
	update   windows.HWND
	mu       sync.Mutex
	busy     bool
	pending  *trayResult
}

func runTray() error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	// Windows handles scaling when the window moves to a different monitor.
	previousDPI, _, _ := procSetThreadDpiAwarenessContext.Call(^uintptr(1))
	defer procSetThreadDpiAwarenessContext.Call(previousDPI)
	mutexName, _ := windows.UTF16PtrFromString(`Global\JonaHomelabCompanionTray`)
	mutex, mutexErr := windows.CreateMutex(nil, false, mutexName)
	if mutexErr == windows.ERROR_ALREADY_EXISTS {
		return nil
	}
	if mutexErr != nil {
		return fmt.Errorf("create tray mutex: %w", mutexErr)
	}
	defer windows.CloseHandle(mutex)
	tray := &trayApplication{}
	defer tray.theme.close()
	activeTray = tray
	defer func() { activeTray = nil }()
	if err := tray.create(); err != nil {
		return err
	}
	tray.refreshInfo()
	return tray.messageLoop()
}

var activeTray *trayApplication

func showTrayError(err error) {
	messageBox(0, err.Error(), displayName, mbOK|mbIconError)
}

func (t *trayApplication) create() error {
	t.theme.init()
	instance, _, _ := procGetModuleHandle.Call(0)
	t.instance = instance
	className := utf16(windowClassName)
	icon := loadSystemIcon()
	class := nativeClass{
		Size:      uint32(unsafe.Sizeof(nativeClass{})),
		WndProc:   windows.NewCallback(windowProc),
		Instance:  instance,
		Icon:      icon,
		Cursor:    loadSystemCursor(),
		ClassName: uintptr(unsafe.Pointer(className)),
		SmallIcon: icon,
	}
	if result, _, callErr := procRegisterClassEx.Call(uintptr(unsafe.Pointer(&class))); result == 0 && callErr != windows.ERROR_CLASS_ALREADY_EXISTS {
		return fmt.Errorf("register tray window: %w", callErr)
	}
	style := uint32(wsCaption | wsSysMenu | wsMinimizeBox | wsClipChildren)
	rect := nativeRect{Right: int32(t.theme.px(660)), Bottom: int32(t.theme.px(630))}
	procAdjustWindowRectExForDpi.Call(uintptr(unsafe.Pointer(&rect)), uintptr(style), 0, 0, uintptr(t.theme.dpi))
	t.hwnd = windows.HWND(createWindow(className, utf16(displayName), style, 0x80000000, 0x80000000, int(rect.Right-rect.Left), int(rect.Bottom-rect.Top), 0, t.instance))
	if t.hwnd == 0 {
		return errors.New("create tray window failed")
	}
	t.font = t.theme.body
	t.theme.applyCaption(t.hwnd)
	t.createControls()
	t.addIcon(icon)
	return nil
}

func (t *trayApplication) createControls() {
	t.label("JONA HOMELAB", 96, 28, 440, 18, t.theme.caption, trayMuted, trayBackground)
	t.label("Companion", 94, 49, 400, 36, t.theme.title, trayText, trayBackground)
	t.label("Your PC, connected to your homelab.", 32, 94, 460, 22, t.font, trayMuted, trayBackground)
	t.status = t.newControl("STATIC", "Connecting to service", wsChild|wsVisible|ssOwnerDraw, 0, 32, 124, 224, 30, 0)
	t.refresh = t.button("Refresh", 516, 120, 112, 38, idRefresh, trayBackground)

	t.label("Pair this PC", 56, 196, 520, 26, t.theme.heading, trayText, traySurface)
	t.label("Paste this code into your device's Companion settings.", 56, 228, 548, 20, t.font, trayMuted, traySurface)
	t.label("PAIRING CODE", 56, 261, 520, 16, t.theme.caption, trayMuted, traySurface)
	t.code = t.newControl("EDIT", "Waiting for service...", wsChild|wsVisible|wsTabStop|esReadOnly|esAutoHScroll, 0, 70, 295, 520, 22, idCode)
	t.theme.controls[t.code] = trayControlStyle{background: trayInput, foreground: trayText}
	procSendMessage.Call(uintptr(t.code), wmSetFont, t.theme.mono, 1)
	t.copy = t.button("Copy pairing code", 56, 338, 184, 40, idCopy, traySurface)
	t.rotate = t.button("Rotate code", 252, 338, 138, 40, idRotate, traySurface)
	t.label("Keep it private. This code grants control of this PC.", 56, 390, 548, 18, t.theme.small, trayMuted, traySurface)

	t.label("Server activity", 56, 452, 250, 24, t.theme.heading, trayText, traySurface)
	t.lastCall = t.label("Last server call: Never", 56, 486, 548, 22, t.font, trayText, traySurface)
	t.details = t.label("Discovering local network...", 56, 518, 548, 20, t.theme.small, trayMuted, traySurface)

	t.update = t.button("Check for updates", 32, 574, 176, 38, idUpdate, trayBackground)
	t.version = t.label("Version —", 224, 585, 260, 18, t.theme.small, trayMuted, trayBackground)
	t.button("Exit tray", 516, 574, 112, 38, idExit, trayBackground)
}

func (t *trayApplication) newControl(class, text string, style, extended uint32, x, y, width, height int, id int) windows.HWND {
	control := windows.HWND(createWindowEx(extended, utf16(class), utf16(text), style, t.theme.px(x), t.theme.px(y), t.theme.px(width), t.theme.px(height), t.hwnd, uintptr(id), t.instance))
	procSendMessage.Call(uintptr(control), wmSetFont, t.font, 1)
	return control
}

func (t *trayApplication) addIcon(icon uintptr) {
	t.icon = notifyIconData{Size: uint32(unsafe.Sizeof(notifyIconData{})), Window: t.hwnd, ID: 1, Flags: nifMessage | nifIcon | nifTip, Callback: wmTrayMessage, Icon: icon}
	tip, _ := windows.UTF16FromString(displayName)
	copy(t.icon.Tip[:], tip)
	procShellNotifyIcon.Call(nimAdd, uintptr(unsafe.Pointer(&t.icon)))
	t.icon.Timeout = notifyIconVersion4
	procShellNotifyIcon.Call(nimSetVersion, uintptr(unsafe.Pointer(&t.icon)))
}

func (t *trayApplication) removeIcon() {
	procShellNotifyIcon.Call(nimDelete, uintptr(unsafe.Pointer(&t.icon)))
}

func (t *trayApplication) messageLoop() error {
	var message nativeMessage
	for {
		result, _, err := procGetMessage.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0)
		if int32(result) == -1 {
			return fmt.Errorf("tray message loop: %w", err)
		}
		if result == 0 {
			return nil
		}
		if handled, _, _ := procIsDialogMessage.Call(uintptr(t.hwnd), uintptr(unsafe.Pointer(&message))); handled != 0 {
			continue
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&message)))
		procDispatchMessage.Call(uintptr(unsafe.Pointer(&message)))
	}
}

func (t *trayApplication) windowProc(hwnd windows.HWND, message uint32, wParam, lParam uintptr) uintptr {
	switch message {
	case wmCreate:
		return 0
	case wmCommand:
		if windows.HWND(lParam) == t.code && (highWord(wParam) == 0x100 || highWord(wParam) == 0x200) {
			procInvalidateRect.Call(uintptr(hwnd), 0, 0)
		}
		if highWord(wParam) == 0 {
			t.command(int(lowWord(wParam)))
		}
		return 0
	case wmTrayMessage:
		switch trayEventKindFor(lParam) {
		case trayEventShow:
			t.show()
		case trayEventMenu:
			t.showMenu()
		}
		return 0
	case wmAppResult:
		t.finishAction()
		return 0
	case wmPaint:
		t.paint()
		return 0
	case wmEraseBkgnd:
		return 1
	case wmDrawItem:
		var item nativeDrawItem
		procRtlMoveMemory.Call(uintptr(unsafe.Pointer(&item)), lParam, unsafe.Sizeof(item))
		t.drawControl(&item)
		return 1
	case wmCtlColorStatic, wmCtlColorEdit:
		style := t.theme.controls[windows.HWND(lParam)]
		procSetTextColor.Call(wParam, colorRef(style.foreground))
		procSetBkColor.Call(wParam, colorRef(style.background))
		return t.theme.brush(style.background)
	case wmClose:
		procShowWindow.Call(uintptr(hwnd), swHide)
		return 0
	case wmDestroy:
		t.removeIcon()
		procPostQuitMessage.Call(0)
		return 0
	}
	result, _, _ := procDefWindowProc.Call(uintptr(hwnd), uintptr(message), wParam, lParam)
	return result
}

func windowProc(hwnd windows.HWND, message uint32, wParam, lParam uintptr) uintptr {
	if activeTray != nil {
		return activeTray.windowProc(hwnd, message, wParam, lParam)
	}
	result, _, _ := procDefWindowProc.Call(uintptr(hwnd), uintptr(message), wParam, lParam)
	return result
}

func (t *trayApplication) command(id int) {
	switch id {
	case idCopy:
		t.startAction("copy")
	case idRotate:
		if messageBox(t.hwnd, "The old pairing code stops working immediately. Continue?", displayName, mbYesNo|mbIconWarning) == idYes {
			t.startAction("rotate")
		}
	case idRefresh:
		t.startAction("refresh")
	case idUpdate:
		t.startAction("update")
	case idExit:
		procDestroyWindow.Call(uintptr(t.hwnd))
	}
}

func (t *trayApplication) startAction(action string) {
	t.mu.Lock()
	if t.busy {
		t.mu.Unlock()
		return
	}
	t.busy = true
	t.mu.Unlock()
	t.setStatus("Connecting to service", trayStatusPending)
	for _, button := range []windows.HWND{t.copy, t.rotate, t.refresh, t.update} {
		procEnableWindow.Call(uintptr(button), 0)
	}
	go func() {
		result := trayResult{action: action}
		if action == "update" {
			response, err := callPipeRaw("check-update")
			result.err = err
			if err == nil {
				result.err = jsonUnmarshal(response, &result.update)
			}
		} else {
			result.info, result.err = callPipe(actionForPipe(action))
		}
		t.mu.Lock()
		t.pending = &result
		t.mu.Unlock()
		procPostMessage.Call(uintptr(t.hwnd), wmAppResult, 0, 0)
	}()
}

func actionForPipe(action string) string {
	if action == "copy" || action == "refresh" {
		return "get-info"
	}
	return action
}

func (t *trayApplication) finishAction() {
	t.mu.Lock()
	result := t.pending
	t.pending = nil
	t.busy = false
	t.mu.Unlock()
	for _, button := range []windows.HWND{t.copy, t.rotate, t.refresh, t.update} {
		procEnableWindow.Call(uintptr(button), 1)
	}
	if result == nil {
		return
	}
	if result.err != nil {
		text, status := trayServiceStatus(result.err)
		t.setStatus(text, status)
		messageBox(t.hwnd, result.err.Error(), displayName, mbOK|mbIconError)
		return
	}
	if result.action == "update" {
		t.setStatus("Service connected", trayStatusConnected)
		messageBox(t.hwnd, updateCheckMessage(result.update), displayName, mbOK)
		return
	}
	t.updateInfo(result.info)
	if result.action == "copy" || result.action == "rotate" {
		if err := setClipboardText(result.info.PairingCode); err != nil {
			messageBox(t.hwnd, err.Error(), displayName, mbOK|mbIconError)
			return
		}
		messageBox(t.hwnd, "Pairing code copied to the clipboard.", displayName, mbOK)
	}
}

func trayServiceStatus(err error) (string, trayStatus) {
	var operationError *pipeOperationError
	if err == nil || errors.As(err, &operationError) {
		return "Service connected", trayStatusConnected
	}
	return "Service unavailable", trayStatusError
}

func updateCheckMessage(result updateCheckResult) string {
	if result.LocalBuild {
		return "This is a local build. Updates are installed manually. Install a published release to enable automatic updates.\n\nThe Companion service remains available for device controls."
	}
	if result.Scheduled {
		return "Update scheduled. The service will restart shortly."
	}
	return "Already up to date."
}

func (t *trayApplication) refreshInfo() { t.startAction("refresh") }

func (t *trayApplication) updateInfo(info pipeInfo) {
	t.setStatus("Service connected", trayStatusConnected)
	setWindowText(t.code, info.PairingCode)
	lastCall := "Last server call: Never"
	if info.LastServerCall != "" {
		lastCall = "Last server call: " + formatServerCall(info.LastServerCall)
	}
	setWindowText(t.lastCall, lastCall)
	setWindowText(t.details, fmt.Sprintf("LOCAL NETWORK   %s   ·   PORT %d", localIPv4(), info.Port))
	setWindowText(t.version, "Version "+info.Version)
}

func (t *trayApplication) show() {
	procShowWindow.Call(uintptr(t.hwnd), swShow)
	procSetForegroundWindow.Call(uintptr(t.hwnd))
	procUpdateWindow.Call(uintptr(t.hwnd))
	if t.code == 0 {
		t.refreshInfo()
	}
}

func (t *trayApplication) showMenu() {
	menu, _, _ := procCreatePopupMenu.Call()
	if menu == 0 {
		return
	}
	appendMenu(menu, mfString, idCopy, "Copy pairing code")
	appendMenu(menu, mfString, idRefresh, "Refresh")
	appendMenu(menu, mfString, idUpdate, "Check for updates")
	appendMenu(menu, mfSeparator, 0, "")
	appendMenu(menu, mfString, idExit, "Exit tray")
	var point nativePoint
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&point)))
	procSetForegroundWindow.Call(uintptr(t.hwnd))
	procTrackPopupMenu.Call(menu, tpmRightButton, uintptr(point.X), uintptr(point.Y), 0, uintptr(t.hwnd), 0)
	procShellNotifyIcon.Call(nimSetFocus, uintptr(unsafe.Pointer(&t.icon)))
	procPostMessage.Call(uintptr(t.hwnd), wmNull, 0, 0)
	procDestroyMenu.Call(menu)
}

func trayEventKindFor(lParam uintptr) trayEventKind {
	// NOTIFYICON_VERSION_4 packs the notification in LOWORD(lParam) and the
	// icon ID in HIWORD(lParam). LOWORD also preserves legacy callback events.
	switch uint32(lowWord(lParam)) {
	case wmLButtonDown, wmLButtonDblClk, ninSelect, ninKeySelect:
		return trayEventShow
	case wmContextMenu, wmRButtonDown, wmRButtonUp:
		return trayEventMenu
	default:
		return trayEventIgnored
	}
}

func createWindowEx(extended uint32, class, title *uint16, style uint32, x, y, width, height int, parent windows.HWND, id uintptr, instance uintptr) uintptr {
	result, _, _ := procCreateWindowEx.Call(uintptr(extended), uintptr(unsafe.Pointer(class)), uintptr(unsafe.Pointer(title)), uintptr(style), uintptr(x), uintptr(y), uintptr(width), uintptr(height), uintptr(parent), id, instance, 0)
	return result
}

func createWindow(class, title *uint16, style uint32, x, y, width, height int, parent windows.HWND, instance uintptr) uintptr {
	return createWindowEx(0, class, title, style, x, y, width, height, parent, 0, instance)
}

func appendMenu(menu uintptr, flags uint32, id int, title string) {
	procAppendMenu.Call(menu, uintptr(flags), uintptr(id), uintptr(unsafe.Pointer(utf16(title))))
}

func utf16(value string) *uint16 {
	result, _ := windows.UTF16PtrFromString(value)
	return result
}

func loadSystemIcon() uintptr {
	result, _, _ := procLoadIcon.Call(0, uintptr(32512))
	return result
}

func loadSystemCursor() uintptr {
	result, _, _ := procLoadCursor.Call(0, uintptr(32512))
	return result
}

func stockFont() uintptr {
	result, _, _ := procGetStockObject.Call(defaultGuiFont)
	return result
}

func setWindowText(hwnd windows.HWND, value string) {
	if hwnd != 0 {
		procSetWindowText.Call(uintptr(hwnd), uintptr(unsafe.Pointer(utf16(value))))
	}
}

func messageBox(hwnd windows.HWND, text, caption string, flags uint32) int32 {
	result, _, _ := procMessageBox.Call(uintptr(hwnd), uintptr(unsafe.Pointer(utf16(text))), uintptr(unsafe.Pointer(utf16(caption))), uintptr(flags))
	return int32(result)
}

func lowWord(value uintptr) uint16  { return uint16(value & 0xffff) }
func highWord(value uintptr) uint16 { return uint16((value >> 16) & 0xffff) }

func setClipboardText(value string) error {
	if value == "" {
		return errors.New("pairing code is empty")
	}
	if result, _, _ := procOpenClipboard.Call(0); result == 0 {
		return errors.New("cannot open clipboard")
	}
	defer procCloseClipboard.Call()
	if result, _, _ := procEmptyClipboard.Call(); result == 0 {
		return errors.New("cannot clear clipboard")
	}
	data, err := windows.UTF16FromString(value)
	if err != nil {
		return err
	}
	handle, _, _ := procGlobalAlloc.Call(gmemMoveable, uintptr(len(data)*2))
	if handle == 0 {
		return errors.New("cannot allocate clipboard memory")
	}
	locked, _, _ := procGlobalLock.Call(handle)
	if locked == 0 {
		procGlobalFree.Call(handle)
		return errors.New("cannot lock clipboard memory")
	}
	procRtlMoveMemory.Call(locked, uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)*2))
	procGlobalUnlock.Call(handle)
	if result, _, _ := procSetClipboardData.Call(cfUnicodeText, handle); result == 0 {
		procGlobalFree.Call(handle)
		return errors.New("cannot set clipboard data")
	}
	return nil
}

func formatServerCall(value string) string {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return value
	}
	return parsed.Local().Format("2006-01-02 15:04:05")
}

func localIPv4() string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return "Unavailable"
	}
	addresses := make([]string, 0, 2)
	for _, network := range interfaces {
		if network.Flags&net.FlagUp == 0 || network.Flags&net.FlagLoopback != 0 {
			continue
		}
		items, _ := network.Addrs()
		for _, item := range items {
			var ip net.IP
			switch value := item.(type) {
			case *net.IPNet:
				ip = value.IP
			case *net.IPAddr:
				ip = value.IP
			}
			if ipv4 := ip.To4(); ipv4 != nil && !ipv4.IsLoopback() {
				candidate := ipv4.String()
				if !containsString(addresses, candidate) {
					addresses = append(addresses, candidate)
				}
			}
		}
	}
	if len(addresses) == 0 {
		return "Unavailable"
	}
	return strings.Join(addresses, ", ")
}

func containsString(values []string, candidate string) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func jsonUnmarshal(content []byte, target any) error {
	return json.Unmarshal(content, target)
}
