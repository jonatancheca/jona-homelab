//go:build windows

package main

import (
	"os"
	"runtime"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Exercise real Win32 menu stacking and input without connecting to the service.
func TestCompanionNativeTrayMenu(t *testing.T) {
	if os.Getenv("COMPANION_UI_TEST") != "1" {
		t.Skip("set COMPANION_UI_TEST=1 to exercise native windows")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	instance, _, _ := procGetModuleHandle.Call(0)
	main := createWindow(utf16("STATIC"), utf16("Companion menu test"), wsCaption|wsSysMenu, 0, 0, 300, 200, 0, instance)
	if main == 0 {
		t.Fatal("create hidden main window")
	}
	defer procDestroyWindow.Call(main)
	tray := trayApplication{hwnd: windows.HWND(main), instance: instance}
	var point nativePoint
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&point)))
	// Model the topmost notification-area overflow panel behind the menu.
	panel := createWindowEx(0x88, utf16("STATIC"), utf16("Test notification area"), 0x90000000, int(point.X), int(point.Y), 300, 200, 0, 0, instance)
	if panel == 0 {
		t.Fatal("create topmost notification area")
	}
	defer procDestroyWindow.Call(panel)
	setTimer, killTimer := user32.NewProc("SetTimer"), user32.NewProc("KillTimer")
	endMenu := user32.NewProc("EndMenu")
	isVisible, isWindow := user32.NewProc("IsWindowVisible"), user32.NewProc("IsWindow")
	getWindow, getForeground := user32.NewProc("GetWindow"), user32.NewProc("GetForegroundWindow")
	getThreadInfo := user32.NewProc("GetGUIThreadInfo")
	findWindow := user32.NewProc("FindWindowExW")
	for _, action := range []string{"escape", "escape-again", "cancel-mode", "exit"} {
		t.Logf("action: %s", action)
		// Keep creation, menu tracking and destruction on this locked UI thread.
		func() {
			procSetForegroundWindow.Call(panel)
			var owner uintptr
			checked := false
			started := time.Now()
			callback := windows.NewCallback(func(_ windows.HWND, _ uint32, _ uintptr, _ uint32) uintptr {
				if time.Since(started) > 3*time.Second {
					t.Error("tray menu did not close")
					endMenu.Call()
					return 0
				}
				if checked {
					return 0
				}
				info := struct {
					Size, Flags                       uint32
					Active, Focus, Capture, MenuOwner uintptr
					MoveSize, Caret                   uintptr
					CaretRect                         nativeRect
				}{}
				info.Size = uint32(unsafe.Sizeof(info))
				getThreadInfo.Call(uintptr(windows.GetCurrentThreadId()), uintptr(unsafe.Pointer(&info)))
				owner = info.MenuOwner
				if owner == 0 {
					return 0
				}
				checked = true
				if visible, _, _ := isVisible.Call(owner); visible == 0 {
					t.Error("menu owner is hidden; Explorer can cover the menu")
				}
				if foreground, _, _ := getForeground.Call(); foreground != owner {
					t.Error("menu owner did not acquire foreground")
				}
				if visible, _, _ := isVisible.Call(main); visible != 0 {
					t.Error("right click revealed the main window")
				}
				menu, _, _ := findWindow.Call(0, 0, uintptr(unsafe.Pointer(utf16("#32768"))), 0)
				if menu == 0 {
					t.Error("native popup menu missing")
				} else {
					for above, _, _ := getWindow.Call(menu, 3); above != 0; above, _, _ = getWindow.Call(above, 3) {
						if above == panel {
							t.Error("notification area covers the menu")
							break
						}
					}
				}
				switch action {
				case "cancel-mode":
					procPostMessage.Call(owner, 0x001f, 0, 0) // WM_CANCELMODE cancels menu tracking.
				case "exit":
					procPostMessage.Call(owner, 0x102, 'e', 1) // Select Exit tray by its initial.
				default:
					procPostMessage.Call(owner, 0x100, 0x1b, 0)
				}
				return 0
			})
			timer, _, _ := setTimer.Call(0, 0, 80, callback)
			if timer == 0 {
				t.Fatal("create menu watchdog")
			}
			tray.showMenu()
			killTimer.Call(0, timer)
			if !checked {
				t.Fatal("menu never entered its native loop")
			}
			if alive, _, _ := isWindow.Call(owner); alive != 0 {
				t.Error("temporary menu owner leaked")
			}
			alive, _, _ := isWindow.Call(main)
			if (alive != 0) != (action != "exit") {
				t.Error("menu command did not preserve or close the main window as requested")
			}
		}()
	}
}
