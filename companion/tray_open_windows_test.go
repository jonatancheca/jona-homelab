//go:build windows

package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestOpeningTrayChecksUpdatesOnceAndQueuesDuringRefresh(t *testing.T) {
	if os.Getenv("COMPANION_UI_TEST") != "1" {
		t.Skip("set COMPANION_UI_TEST=1 to exercise native windows")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	tray := &trayApplication{}
	activeTray = tray
	defer func() { tray.close(); activeTray = nil }()
	if err := tray.create(); err != nil {
		t.Fatal(err)
	}
	var checks atomic.Int32
	var infos atomic.Int32
	tray.request = func(action string) ([]byte, error) {
		if action == "get-info" {
			infos.Add(1)
			return []byte(marshalLocal(pipeInfo{Ready: true, Version: "main-000000000000", DisplayVersion: "1.00", PairingCode: "TEST CODE", Port: companionPort})), nil
		}
		if action != "check-update" {
			return nil, errors.New("unexpected pipe action: " + action)
		}
		switch checks.Add(1) {
		case 1:
			return []byte(`{"scheduled":false}`), nil
		case 2:
			return []byte(`{"scheduled":true,"targetVersion":"main-111111111111"}`), nil
		case 3:
			return nil, &pipeOperationError{message: "release check returned HTTP 503"}
		default:
			return []byte(`{"scheduled":false,"localBuild":true}`), nil
		}
	}

	// Opening while the startup refresh is running must queue the check.
	tray.refreshInfo()
	tray.show()
	tray.show() // Windows can send both click and selection notifications.
	waitForTrayAction(t, tray)
	if checks.Load() != 1 || infos.Load() != 1 {
		t.Fatalf("opening issued %d checks and %d refreshes", checks.Load(), infos.Load())
	}
	if text := windowText(tray.version); text != "Version 1.00 · Up to date" {
		t.Fatalf("installed version/check result: %q", text)
	}
	tray.show()
	if tray.busy || checks.Load() != 1 {
		t.Fatal("focusing a visible window repeated the check")
	}
	for i, want := range []string{"Update requested", "Update check failed", "Local build; manual updates"} {
		tray.windowProc(tray.hwnd, wmClose, 0, 0)
		tray.show()
		waitForTrayAction(t, tray)
		if checks.Load() != int32(i+2) || !strings.Contains(windowText(tray.version), want) {
			t.Fatalf("reopen result: checks=%d text=%q", checks.Load(), windowText(tray.version))
		}
		if tray.dialog != 0 {
			t.Fatal("automatic check opened an unsolicited modal")
		}
		if i == 1 && windowText(tray.status) != "Service connected" {
			t.Fatal("GitHub error reported as service outage")
		}
	}
	// Legacy service versions keep their real identifier.
	tray.updateInfo(pipeInfo{Version: "main-000000000000"})
	if windowText(tray.version) != "Version main-000000000000" {
		t.Fatal("legacy service version was fabricated")
	}
	tray.updateInfo(pipeInfo{DisplayVersion: "1.00", PairingCode: "TEST CODE", Port: companionPort})
	if directory := os.Getenv("COMPANION_UI_SNAPSHOTS"); directory != "" {
		if err := captureDialog(tray.hwnd, filepath.Join(directory, "version-1.00.png")); err != nil {
			t.Fatal(err)
		}
	}
}

func waitForTrayAction(t *testing.T, tray *trayApplication) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	peek := user32.NewProc("PeekMessageW")
	for time.Now().Before(deadline) {
		var message nativeMessage
		for {
			found, _, _ := peek.Call(uintptr(unsafe.Pointer(&message)), uintptr(tray.hwnd), 0, 0, 1)
			if found == 0 {
				break
			}
			procTranslateMessage.Call(uintptr(unsafe.Pointer(&message)))
			procDispatchMessage.Call(uintptr(unsafe.Pointer(&message)))
		}
		tray.mu.Lock()
		busy := tray.busy
		tray.mu.Unlock()
		if !busy {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("tray action did not finish")
}

func windowText(hwnd windows.HWND) string {
	var text [1024]uint16
	user32.NewProc("GetWindowTextW").Call(uintptr(hwnd), uintptr(unsafe.Pointer(&text[0])), uintptr(len(text)))
	return windows.UTF16ToString(text[:])
}
