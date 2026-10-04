//go:build windows

package main

import (
	"context"
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

func TestTrayRecoversWhenServiceStartsAfterLogon(t *testing.T) {
	if os.Getenv("COMPANION_UI_TEST") != "1" {
		t.Skip("set COMPANION_UI_TEST=1 to exercise native windows")
	}
	for _, opened := range []bool{false, true} {
		name := "hidden"
		if opened {
			name = "opened"
		}
		t.Run(name, func(t *testing.T) {
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			tray := &trayApplication{}
			activeTray = tray
			defer closeTestTray(tray)
			if err := tray.create(); err != nil {
				t.Fatal(err)
			}
			// Close unexpected modals so a regression fails rather than hanging.
			watchdog := windows.NewCallback(func(_ windows.HWND, _ uint32, _ uintptr, _ uint32) uintptr {
				if tray.dialog != 0 {
					t.Error("automatic reconnect displayed an unsolicited modal")
					procEndDialog.Call(uintptr(tray.dialog), idDialogCancel)
				}
				return 0
			})
			timer, _, _ := user32.NewProc("SetTimer").Call(0, 0, 50, watchdog)
			defer user32.NewProc("KillTimer").Call(0, timer)
			var infos, checks atomic.Int32
			tray.request = func(action string) ([]byte, error) {
				switch action {
				case "get-info":
					infos.Add(1)
				case "check-update":
					checks.Add(1)
				default:
					t.Errorf("reconnect attempted a mutating action: %s", action)
				}
				return callPipeRaw(action) // TestMain isolates the real named pipe.
			}
			tray.refreshInfo()
			if opened {
				tray.show()
			}
			waitForTrayAction(t, tray)
			if !tray.serviceRetry || windowText(tray.status) != "Service unavailable" || checks.Load() != 0 {
				t.Fatal("missing silent recovery while the delayed service is absent")
			}
			// A second failed refresh must keep retrying and retain the opening check.
			tray.windowProc(tray.hwnd, wmTimer, trayServiceTimer, 0)
			tray.windowProc(tray.hwnd, wmTimer, trayServiceTimer, 0)
			waitForTrayAction(t, tray)
			if !tray.serviceRetry || infos.Load() != 2 || tray.updateOnOpen != opened {
				t.Fatal("retry stopped, overlapped, or lost the queued opening check")
			}
			tray.dialog = tray.hwnd
			tray.windowProc(tray.hwnd, wmTimer, trayServiceTimer, 0)
			tray.dialog = 0
			if tray.busy || infos.Load() != 2 {
				t.Fatal("reconnect started during a modal action")
			}
			store, err := loadConfig()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() { runPipeServer(ctx, newRuntimeState(store)); close(done) }()
			defer func() {
				cancel()
				select {
				case <-done:
				case <-time.After(2 * time.Second):
					t.Error("isolated pipe did not stop")
				}
			}()
			// Let the actual Win32 timer reconnect; no manual refresh or reopening.
			deadline := time.Now().Add(8 * time.Second)
			for tray.serviceRetry && time.Now().Before(deadline) {
				waitForTrayAction(t, tray)
				time.Sleep(10 * time.Millisecond)
			}
			if tray.serviceRetry || windowText(tray.status) != "Service connected" || windowText(tray.code) == "" {
				t.Fatal("late service did not restore connected status and pairing data")
			}
			wantChecks := int32(0)
			if opened {
				wantChecks = 1
			}
			if checks.Load() != wantChecks || tray.updateOnOpen {
				t.Fatal("opening update check was lost or duplicated after recovery")
			}
			before := infos.Load()
			tray.windowProc(tray.hwnd, wmTimer, trayServiceTimer, 0)
			if tray.busy || infos.Load() != before {
				t.Fatal("recovery timer kept polling after connection succeeded")
			}
			visible, _, _ := user32.NewProc("IsWindowVisible").Call(uintptr(tray.hwnd))
			if (visible != 0) != opened {
				t.Fatal("recovery changed window visibility")
			}
		})
	}
}

func closeTestTray(tray *trayApplication) {
	tray.close()
	activeTray = nil
	// Destroying the tray posts WM_QUIT. Do not leak it into the next test's
	// modal message loop when Go reuses this Windows thread.
	var message nativeMessage
	for {
		found, _, _ := user32.NewProc("PeekMessageW").Call(uintptr(unsafe.Pointer(&message)), 0, 0x12, 0x12, 1)
		if found == 0 {
			return
		}
	}
}

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
	tray.updateInfo(pipeInfo{Version: "main-000000000000"}, false)
	if windowText(tray.version) != "Version main-000000000000" {
		t.Fatal("legacy service version was fabricated")
	}
	tray.updateInfo(pipeInfo{DisplayVersion: "1.00", PairingCode: "TEST CODE", Port: companionPort}, false)
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
