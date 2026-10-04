//go:build windows

package main

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
	"testing"
)

// Exercise tray callbacks against a real window without connecting to the service.
func TestCompanionNativeTrayClicksOpenWindow(t *testing.T) {
	if os.Getenv("COMPANION_UI_TEST") != "1" {
		t.Skip("set COMPANION_UI_TEST=1 to exercise native windows")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	tray := &trayApplication{}
	activeTray = tray
	defer closeTestTray(tray)
	if err := tray.create(); err != nil {
		t.Fatal(err)
	}
	var infos, checks atomic.Int32
	tray.request = func(action string) ([]byte, error) {
		switch action {
		case "get-info":
			infos.Add(1)
			return []byte(marshalLocal(pipeInfo{Ready: true, DisplayVersion: companionVersion(), PairingCode: "TEST CODE", Port: companionPort})), nil
		case "check-update":
			checks.Add(1)
			return []byte(`{"scheduled":false,"localBuild":true}`), nil
		default:
			return nil, fmt.Errorf("unexpected pipe action: %s", action)
		}
	}
	hwnd := uintptr(tray.hwnd)
	for _, test := range []struct {
		name   string
		events []uint32
		iconID uintptr
	}{
		{"left click", []uint32{wmLButtonDown, ninSelect}, 1},
		{"right click", []uint32{wmRButtonDown, wmRButtonUp, wmContextMenu}, 1},
		{"legacy right click", []uint32{wmRButtonDown, wmRButtonUp}, 0},
		{"keyboard context menu", []uint32{wmContextMenu}, 1},
		{"keyboard activation", []uint32{ninKeySelect}, 1},
	} {
		t.Logf("activation: %s", test.name)
		// Keep Win32 calls on the thread that owns the window.
		{
			procShowWindow.Call(hwnd, swHide)
			beforeInfos, beforeChecks := infos.Load(), checks.Load()
			for _, event := range test.events {
				procSendMessage.Call(hwnd, wmTrayMessage, 0, uintptr(event)|test.iconID<<16)
			}
			waitForTrayAction(t, tray)
			if visible, _, _ := user32.NewProc("IsWindowVisible").Call(hwnd); visible == 0 {
				t.Fatal("tray activation did not show the window")
			}
			if infos.Load() != beforeInfos+1 || checks.Load() != beforeChecks+1 {
				t.Fatal("tray activation did not refresh and check updates exactly once")
			}
			procShowWindow.Call(hwnd, 6) // SW_MINIMIZE
			if minimized, _, _ := user32.NewProc("IsIconic").Call(hwnd); minimized == 0 {
				t.Fatal("test window did not minimize")
			}
			for _, event := range test.events {
				procSendMessage.Call(hwnd, wmTrayMessage, 0, uintptr(event)|test.iconID<<16)
			}
			if minimized, _, _ := user32.NewProc("IsIconic").Call(hwnd); minimized != 0 {
				t.Fatal("tray activation did not restore the minimized window")
			}
			waitForTrayAction(t, tray)
			if infos.Load() != beforeInfos+1 || checks.Load() != beforeChecks+1 {
				t.Fatal("restoring the window repeated the opening requests")
			}
		}
	}
}
