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
	wmCreate         = 0x0001
	wmDestroy        = 0x0002
	wmClose          = 0x0010
	wmContextMenu    = 0x007B
	wmCommand        = 0x0111
	wmSetFont        = 0x0030
	wmCtlColorStatic = 0x0138
	wmAppResult      = 0x8001
	wmTrayMessage    = 0x8002
	wmShowCompanion  = 0x8003
	wmTimer          = 0x0113
	trayIconTimer    = 1
	trayServiceTimer = 2
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

	swHide    = 0
	swShow    = 5
	swRestore = 9

	mbOK        = 0x00000000
	mbIconError = 0x00000010
	idYes       = 6

	transparent    = 1
	defaultGuiFont = 17

	nifMessage         = 0x00000001
	nifIcon            = 0x00000002
	nifTip             = 0x00000004
	nimAdd             = 0x00000000
	nimDelete          = 0x00000002
	nimSetVersion      = 0x00000004
	notifyIconVersion4 = 4

	cfUnicodeText = 13
	gmemMoveable  = 0x00000002

	idCopy        = 1001
	idRotate      = 1002
	idRefresh     = 1003
	idUpdate      = 1004
	idExit        = 1005
	idCode        = 1006
	idDiagnostics = 1007
)

type trayEventKind uint8

const (
	trayEventIgnored trayEventKind = iota
	trayEventShow
)

var (
	windowClassName         = "JonaHomelabCompanionTrayWindow"
	trayMutexName           = `Local\JonaHomelabCompanionTray`
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
	procMessageBox          = user32.NewProc("MessageBoxW")
	procOpenClipboard       = user32.NewProc("OpenClipboard")
	procCloseClipboard      = user32.NewProc("CloseClipboard")
	procEmptyClipboard      = user32.NewProc("EmptyClipboard")
	procSetClipboardData    = user32.NewProc("SetClipboardData")
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
	action               string
	info                 pipeInfo
	update               updateCheckResult
	diagnosticsDirectory string
	err                  error
}

type trayApplication struct {
	hwnd           windows.HWND
	dialog         windows.HWND
	instance       uintptr
	icon           notifyIconData
	icons          companionIcons
	taskbarCreated uint32
	font           uintptr
	theme          trayTheme
	status         windows.HWND
	code           windows.HWND
	lastCall       windows.HWND
	details        windows.HWND
	version        windows.HWND
	copy           windows.HWND
	rotate         windows.HWND
	refresh        windows.HWND
	update         windows.HWND
	diagnostics    windows.HWND
	mu             sync.Mutex
	busy           bool
	pending        *trayResult
	updateOnOpen   bool
	versionText    string
	request        func(string) ([]byte, error)
	iconRetry      bool
	serviceRetry   bool
	openFolder     func(windows.HWND, string) error
}

func runTray(showWindow bool) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	// Windows handles scaling when the window moves to a different monitor.
	previousDPI, _, _ := procSetThreadDpiAwarenessContext.Call(^uintptr(1))
	defer procSetThreadDpiAwarenessContext.Call(previousDPI)
	// Each interactive session needs its own icon (including fast user switching).
	mutexName, _ := windows.UTF16PtrFromString(trayMutexName)
	mutex, mutexErr := windows.CreateMutex(nil, false, mutexName)
	if mutex != 0 {
		defer windows.CloseHandle(mutex)
	}
	if mutexErr == windows.ERROR_ALREADY_EXISTS {
		if showWindow {
			return showExistingTray()
		}
		return nil
	}
	if mutexErr != nil {
		return fmt.Errorf("create tray mutex: %w", mutexErr)
	}
	tray := &trayApplication{}
	activeTray = tray
	defer func() { tray.close(); activeTray = nil }()
	if err := tray.create(); err != nil {
		return err
	}
	if showWindow {
		tray.show()
	} else {
		tray.refreshInfo()
	}
	return tray.messageLoop()
}

func showExistingTray() error {
	// A concurrent logon launch may own the mutex before creating its window.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		hwnd, _, _ := user32.NewProc("FindWindowW").Call(uintptr(unsafe.Pointer(utf16(windowClassName))), 0)
		if hwnd != 0 {
			var pid uint32
			user32.NewProc("GetWindowThreadProcessId").Call(hwnd, uintptr(unsafe.Pointer(&pid)))
			user32.NewProc("AllowSetForegroundWindow").Call(uintptr(pid))
			if ok, _, err := procPostMessage.Call(hwnd, wmShowCompanion, 0, 0); ok == 0 {
				return fmt.Errorf("show existing Companion window: %w", err)
			}
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return errors.New("Companion is already starting but its window is not available. Try again in a moment.")
}

var activeTray *trayApplication

func showTrayError(err error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	tray := &trayApplication{}
	tray.instance, _, _ = procGetModuleHandle.Call(0)
	tray.theme.init()
	defer tray.theme.close()
	_ = tray.icons.init(tray.theme.dpi)
	defer tray.icons.close()
	tray.showDialog(dialogContent{title: "Companion could not start", body: err.Error(), tone: dialogError})
}

func (t *trayApplication) create() error {
	t.theme.init()
	instance, _, _ := procGetModuleHandle.Call(0)
	t.instance = instance
	className := utf16(windowClassName)
	if err := t.icons.init(t.theme.dpi); err != nil {
		return err
	}
	class := nativeClass{
		Size:      uint32(unsafe.Sizeof(nativeClass{})),
		WndProc:   windows.NewCallback(windowProc),
		Instance:  instance,
		Icon:      t.icons.large,
		Cursor:    loadSystemCursor(),
		ClassName: uintptr(unsafe.Pointer(className)),
		SmallIcon: t.icons.small,
	}
	if result, _, callErr := procRegisterClassEx.Call(uintptr(unsafe.Pointer(&class))); result == 0 && callErr != windows.ERROR_CLASS_ALREADY_EXISTS {
		return fmt.Errorf("register tray window: %w", callErr)
	}
	style := uint32(wsCaption | wsSysMenu | wsMinimizeBox | wsClipChildren)
	rect := nativeRect{Right: int32(t.theme.px(660)), Bottom: int32(t.theme.px(660))}
	procAdjustWindowRectExForDpi.Call(uintptr(unsafe.Pointer(&rect)), uintptr(style), 0, 0, uintptr(t.theme.dpi))
	t.hwnd = windows.HWND(createWindow(className, utf16(displayName), style, 0x80000000, 0x80000000, int(rect.Right-rect.Left), int(rect.Bottom-rect.Top), 0, t.instance))
	if t.hwnd == 0 {
		return errors.New("create tray window failed")
	}
	t.font = t.theme.body
	t.theme.applyCaption(t.hwnd)
	t.createControls()
	taskbarCreated, _, _ := procRegisterWindowMessage.Call(uintptr(unsafe.Pointer(utf16("TaskbarCreated"))))
	t.taskbarCreated = uint32(taskbarCreated)
	t.ensureIcon()
	return nil
}

func (t *trayApplication) close() {
	if t.hwnd != 0 {
		procDestroyWindow.Call(uintptr(t.hwnd))
	}
	procUnregisterClass.Call(uintptr(unsafe.Pointer(utf16(windowClassName))), t.instance)
	t.icons.close()
	t.theme.close()
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
	t.diagnostics = t.button("Generate diagnostics", 220, 574, 204, 38, idDiagnostics, trayBackground)
	t.versionText = "Version " + companionVersion()
	t.version = t.label(t.versionText, 32, 626, 596, 18, t.theme.small, trayMuted, trayBackground)
	t.button("Exit tray", 516, 574, 112, 38, idExit, trayBackground)
}

func (t *trayApplication) newControl(class, text string, style, extended uint32, x, y, width, height int, id int) windows.HWND {
	control := windows.HWND(createWindowEx(extended, utf16(class), utf16(text), style, t.theme.px(x), t.theme.px(y), t.theme.px(width), t.theme.px(height), t.hwnd, uintptr(id), t.instance))
	procSendMessage.Call(uintptr(control), wmSetFont, t.font, 1)
	return control
}

func (t *trayApplication) addIcon() error {
	t.icon = notifyIconData{Size: uint32(unsafe.Sizeof(notifyIconData{})), Window: t.hwnd, ID: 1, Flags: nifMessage | nifIcon | nifTip | 0x80, Callback: wmTrayMessage, Icon: t.icons.small}
	tip, _ := windows.UTF16FromString(displayName)
	copy(t.icon.Tip[:], tip)
	if ok, err := notifyTrayIcon(nimAdd, &t.icon); ok == 0 {
		return fmt.Errorf("add Companion tray icon: %w", err)
	}
	t.icon.Timeout = notifyIconVersion4
	notifyTrayIcon(nimSetVersion, &t.icon)
	return nil
}

func (t *trayApplication) removeIcon() {
	notifyTrayIcon(nimDelete, &t.icon)
}

var notifyTrayIcon = func(message uintptr, icon *notifyIconData) (uintptr, error) {
	ok, _, err := procShellNotifyIcon.Call(message, uintptr(unsafe.Pointer(icon)))
	return ok, err
}

func (t *trayApplication) ensureIcon() {
	if err := t.addIcon(); err != nil {
		if !t.iconRetry {
			logEvent("tray.icon_failed", map[string]any{"error": err.Error()})
			// Explorer may broadcast TaskbarCreated before its tray is ready.
			timer, _, _ := user32.NewProc("SetTimer").Call(uintptr(t.hwnd), trayIconTimer, 2000, 0)
			t.iconRetry = timer != 0
		}
		return
	}
	user32.NewProc("KillTimer").Call(uintptr(t.hwnd), trayIconTimer)
	t.iconRetry = false
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
	if message == t.taskbarCreated && t.taskbarCreated != 0 {
		t.ensureIcon()
		return 0
	}
	switch message {
	case wmShowCompanion:
		t.show()
		return 0
	case wmTimer:
		if wParam == trayIconTimer {
			t.ensureIcon()
		}
		if wParam == trayServiceTimer && t.serviceRetry && t.dialog == 0 {
			t.refreshInfo()
		}
		return 0
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
		if trayEventKindFor(lParam) == trayEventShow {
			t.show()
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
		user32.NewProc("KillTimer").Call(uintptr(hwnd), trayIconTimer)
		t.setServiceRetry(false)
		t.removeIcon()
		t.hwnd = 0
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
		if t.showDialog(dialogContent{title: "Rotate pairing code?", body: "The current code will stop working immediately.\nPair this PC again in Jona Homelab with the new code.", confirm: "Rotate code", tone: dialogWarning}) {
			t.startAction("rotate")
		}
	case idRefresh:
		t.startAction("refresh")
	case idUpdate:
		t.startAction("update")
	case idDiagnostics:
		t.startAction("diagnostics")
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
	if action == "diagnostics" {
		setWindowText(t.diagnostics, "Generating...")
	} else if action == "open-update" {
		setWindowText(t.version, t.versionText+" · Checking for updates...")
	} else if action != "auto-refresh" || !t.serviceRetry {
		t.setStatus("Connecting to service", trayStatusPending)
	}
	for _, button := range []windows.HWND{t.copy, t.rotate, t.refresh, t.update, t.diagnostics} {
		procEnableWindow.Call(uintptr(button), 0)
	}
	hwnd := t.hwnd
	request := t.request
	if request == nil {
		request = callPipeRaw
	}
	go func() {
		result := trayResult{action: action}
		if action == "diagnostics" {
			result.diagnosticsDirectory, result.err = generateDiagnostics(hwnd)
		} else if action == "update" || action == "open-update" {
			response, err := request("check-update")
			result.err = err
			if err == nil {
				result.err = jsonUnmarshal(response, &result.update)
			}
		} else {
			response, err := request(actionForPipe(action))
			result.err = err
			if err == nil {
				result.err = jsonUnmarshal(response, &result.info)
			}
		}
		t.mu.Lock()
		t.pending = &result
		t.mu.Unlock()
		procPostMessage.Call(uintptr(hwnd), wmAppResult, 0, 0)
	}()
}

func actionForPipe(action string) string {
	if action == "copy" || action == "refresh" || action == "auto-refresh" {
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
	defer func() {
		// An opening queued during the startup refresh must still check once.
		if t.updateOnOpen && result != nil && result.err == nil && actionForPipe(result.action) == "get-info" {
			t.updateOnOpen = false
			t.startAction("open-update")
		}
	}()
	for _, button := range []windows.HWND{t.copy, t.rotate, t.refresh, t.update, t.diagnostics} {
		procEnableWindow.Call(uintptr(button), 1)
	}
	setWindowText(t.diagnostics, "Generate diagnostics")
	if result == nil {
		return
	}
	if result.action == "diagnostics" {
		// Diagnostics does not use the service pipe. Preserve its last known status.
		t.showDiagnosticsResult(result.diagnosticsDirectory, result.err)
		return
	}
	if _, status := trayServiceStatus(result.err); status == trayStatusError {
		t.setServiceRetry(true)
	}
	if result.action == "open-update" {
		text, status := trayServiceStatus(result.err)
		if result.err == nil && result.update.Scheduled {
			text, status = "Update requested", trayStatusPending
		}
		t.setStatus(text, status)
		setWindowText(t.version, t.versionText+" · "+automaticUpdateMessage(result.update, result.err))
		return
	}
	if result.err != nil {
		text, status := trayServiceStatus(result.err)
		t.setStatus(text, status)
		if result.action == "auto-refresh" {
			return // Logon and reconnection never block the tray with an error modal.
		}
		title := "Service unavailable"
		if status == trayStatusConnected {
			title = "Action could not be completed"
		}
		if result.action == "update" && status == trayStatusConnected {
			title = "Could not check for updates"
		}
		t.showDialog(dialogContent{title: title, body: result.err.Error(), tone: dialogError})
		return
	}
	if result.action == "update" {
		t.setStatus("Service connected", trayStatusConnected)
		t.showDialog(updateDialogContent(result.update))
		return
	}
	t.updateInfo(result.info, result.action != "auto-refresh")
	if result.action == "copy" || result.action == "rotate" {
		if err := setClipboardText(result.info.PairingCode); err != nil {
			t.showDialog(dialogContent{title: "Clipboard unavailable", body: err.Error(), tone: dialogError})
			return
		}
		t.showDialog(dialogContent{title: "Pairing code copied", body: "Paste it into this device's Companion settings in Jona Homelab.\nKeep the code private: it grants control of this PC."})
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
		return "Update requested. Use Refresh to check progress or errors."
	}
	return "Already up to date."
}

func automaticUpdateMessage(result updateCheckResult, err error) string {
	if err != nil {
		return "Update check failed. Use Check for updates to retry."
	}
	if result.LocalBuild {
		return "Local build; manual updates"
	}
	if result.Scheduled {
		return "Update requested"
	}
	return "Up to date"
}

func (t *trayApplication) refreshInfo() { t.startAction("auto-refresh") }

func (t *trayApplication) setServiceRetry(enabled bool) {
	if enabled == t.serviceRetry {
		return
	}
	t.serviceRetry = enabled
	if enabled {
		// The delayed-start service may appear minutes after the logon task.
		// Keep retrying read-only get-info until it answers, including while hidden.
		user32.NewProc("SetTimer").Call(uintptr(t.hwnd), trayServiceTimer, 5000, 0)
	} else {
		user32.NewProc("KillTimer").Call(uintptr(t.hwnd), trayServiceTimer)
	}
}

func (t *trayApplication) updateInfo(info pipeInfo, notifyUpdateFailure bool) {
	t.setServiceRetry(false)
	t.setStatus("Service connected", trayStatusConnected)
	if info.Update.active() {
		t.setStatus("Updating: "+info.Update.Phase, trayStatusPending)
	}
	if info.Update.Phase == "failed" || info.Update.Phase == "rolled-back" {
		t.setStatus("Update failed; service connected", trayStatusConnected)
		if notifyUpdateFailure {
			t.showDialog(dialogContent{title: "Update failed", body: info.Update.Error, tone: dialogError})
		}
	}
	setWindowText(t.code, info.PairingCode)
	lastCall := "Last server call: Never"
	if info.LastServerCall != "" {
		lastCall = "Last server call: " + formatServerCall(info.LastServerCall)
	}
	setWindowText(t.lastCall, lastCall)
	setWindowText(t.details, fmt.Sprintf("LOCAL NETWORK   %s   ·   PORT %d", localIPv4(), info.Port))
	version := info.DisplayVersion
	if version == "" {
		version = info.Version
	}
	t.versionText = "Version " + version
	setWindowText(t.version, t.versionText)
}

func (t *trayApplication) show() {
	if t.dialog != 0 {
		procSetForegroundWindow.Call(uintptr(t.dialog))
		return
	}
	visible, _, _ := user32.NewProc("IsWindowVisible").Call(uintptr(t.hwnd))
	show := uintptr(swShow)
	if minimized, _, _ := user32.NewProc("IsIconic").Call(uintptr(t.hwnd)); minimized != 0 {
		show = swRestore
	}
	procShowWindow.Call(uintptr(t.hwnd), show)
	procSetForegroundWindow.Call(uintptr(t.hwnd))
	procUpdateWindow.Call(uintptr(t.hwnd))
	if visible == 0 {
		t.updateOnOpen = true
		t.refreshInfo()
	}
}

func trayEventKindFor(lParam uintptr) trayEventKind {
	// NOTIFYICON_VERSION_4 packs the notification in LOWORD(lParam) and the
	// icon ID in HIWORD(lParam). LOWORD also preserves legacy callback events.
	switch uint32(lowWord(lParam)) {
	case wmLButtonDown, wmLButtonDblClk, ninSelect, ninKeySelect, wmContextMenu, wmRButtonDown, wmRButtonUp:
		return trayEventShow
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

func utf16(value string) *uint16 {
	result, _ := windows.UTF16PtrFromString(value)
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
