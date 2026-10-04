//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"unsafe"
)

func TestPairingCodeRequiresExplicitReveal(t *testing.T) {
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
	isEnabled := user32.NewProc("IsWindowEnabled")
	if enabled, _, _ := isEnabled.Call(uintptr(tray.toggleCode)); enabled != 0 {
		t.Fatal("reveal is available before a pairing code arrives")
	}
	info := pipeInfo{Ready: true, DisplayVersion: companionVersion(), PairingCode: "jhcp1_TEST_ONLY_PRIVATE_CODE", Port: companionPort}
	tray.request = func(action string) ([]byte, error) {
		switch action {
		case "get-info":
			return []byte(marshalLocal(info)), nil
		case "check-update":
			return []byte(`{"localBuild":true}`), nil
		default:
			return nil, fmt.Errorf("unexpected pipe action: %s", action)
		}
	}
	assertCode := func(text, button string) {
		t.Helper()
		if windowText(tray.code) != text || windowText(tray.toggleCode) != button {
			t.Fatal("pairing code visibility or button label does not match the user's choice")
		}
	}
	clickToggle := func() {
		procSendMessage.Call(uintptr(tray.toggleCode), 0x00f5, 0, 0) // BM_CLICK
	}
	snapshot := func(name string) {
		t.Helper()
		if directory := os.Getenv("COMPANION_UI_SNAPSHOTS"); directory != "" {
			procUpdateWindow.Call(uintptr(tray.toggleCode))
			if err := captureDialog(tray.hwnd, filepath.Join(directory, name+".png")); err != nil {
				t.Fatal(err)
			}
		}
	}
	tray.show()
	waitForTrayAction(t, tray)
	assertCode("Pairing code hidden", "Show code")
	if enabled, _, _ := isEnabled.Call(uintptr(tray.toggleCode)); enabled == 0 {
		t.Fatal("reveal remains disabled after connecting")
	}
	snapshot("pairing-hidden")
	clickToggle()
	assertCode(info.PairingCode, "Hide code")
	snapshot("pairing-visible")
	// Enter on the focused native button must also toggle visibility.
	procSetFocus.Call(uintptr(tray.toggleCode))
	message := nativeMessage{Hwnd: tray.toggleCode, Message: 0x100, WParam: 0x0d}
	if handled, _, _ := procIsDialogMessage.Call(uintptr(tray.hwnd), uintptr(unsafe.Pointer(&message))); handled == 0 {
		t.Fatal("Enter did not activate the focused reveal button")
	}
	assertCode("Pairing code hidden", "Show code")
	tray.startAction("refresh")
	waitForTrayAction(t, tray)
	assertCode("Pairing code hidden", "Show code")
	clickToggle()
	tray.refreshInfo()
	waitForTrayAction(t, tray)
	assertCode(info.PairingCode, "Hide code")
	// Rotation or an external code change must require another explicit reveal.
	info.PairingCode = "jhcp1_TEST_ONLY_REPLACEMENT"
	tray.refreshInfo()
	waitForTrayAction(t, tray)
	assertCode("Pairing code hidden", "Show code")
	clickToggle()
	assertCode(info.PairingCode, "Hide code")
	// An outstanding response must not reveal the code after closing/reopening.
	tray.refreshInfo()
	procSendMessage.Call(uintptr(tray.hwnd), wmClose, 0, 0)
	assertCode("Pairing code hidden", "Show code")
	waitForTrayAction(t, tray)
	tray.show()
	assertCode("Pairing code hidden", "Show code")
	waitForTrayAction(t, tray)
	assertCode("Pairing code hidden", "Show code")
	tray.updateInfo(pipeInfo{}, false)
	clickToggle()
	assertCode("Waiting for service...", "Show code")
	if enabled, _, _ := isEnabled.Call(uintptr(tray.toggleCode)); enabled != 0 {
		t.Fatal("reveal remains enabled without a code")
	}
}
