//go:build windows

package main

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Opt-in native smoke tests: real modal loop, native keyboard handling, DPI and
// optional screenshots. No service, pairing data or system power calls.
func TestCompanionNativeDialogs(t *testing.T) {
	if os.Getenv("COMPANION_UI_TEST") != "1" {
		t.Skip("set COMPANION_UI_TEST=1 to exercise native windows")
	}
	setTimer, killTimer := user32.NewProc("SetTimer"), user32.NewProc("KillTimer")
	getDlgItem, isEnabled := user32.NewProc("GetDlgItem"), user32.NewProc("IsWindowEnabled")
	confirmation := dialogContent{title: "Rotate pairing code?", body: "The current code will stop working immediately.\nPair this PC again in Jona Homelab with the new code.", confirm: "Rotate code", tone: dialogWarning}
	tests := []struct {
		name    string
		dpi     int
		content dialogContent
		action  string
		want    int32
	}{
		{"up-to-date-100", 96, updateDialogContent(updateCheckResult{}), "enter", idDialogOK},
		{"up-to-date-150", 144, updateDialogContent(updateCheckResult{}), "escape", idDialogCancel},
		{"up-to-date-200", 192, updateDialogContent(updateCheckResult{}), "close", idDialogCancel},
		{"rotate-default-cancel", 96, confirmation, "enter", idDialogCancel},
		{"rotate-confirm", 96, confirmation, "confirm", idYes},
		{"local-build", 96, updateDialogContent(updateCheckResult{LocalBuild: true}), "escape", idDialogCancel},
		{"long-error", 96, dialogContent{title: "Could not check for updates", body: strings.Repeat("Connection error: the server did not respond.\n", 50), tone: dialogError}, "escape", idDialogCancel},
		{"diagnostics-ready", 96, diagnosticsDialogContent(`C:\Program Files\JonaHomelabCompanion\current\diagnostics`, nil), "enter", idDialogOK},
		{"diagnostics-cancelled", 144, diagnosticsDialogContent("", windows.ERROR_CANCELLED), "escape", idDialogCancel},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			previous, _, _ := procSetThreadDpiAwarenessContext.Call(^uintptr(3))
			defer procSetThreadDpiAwarenessContext.Call(previous)
			instance, _, _ := procGetModuleHandle.Call(0)
			owner := trayApplication{instance: instance}
			owner.hwnd = windows.HWND(createWindow(utf16("STATIC"), utf16("Companion test owner"), 0, 0, 0, 660, 630, 0, instance))
			if owner.hwnd == 0 {
				t.Fatal("create test owner")
			}
			defer procDestroyWindow.Call(uintptr(owner.hwnd))
			if err := owner.icons.init(test.dpi); err != nil {
				t.Fatal(err)
			}
			defer owner.icons.close()
			owner.theme.dpi = test.dpi
			d := companionDialog{owner: &owner, content: test.content}
			acted := false
			started := time.Now()
			callback := windows.NewCallback(func(_ windows.HWND, _ uint32, _ uintptr, _ uint32) uintptr {
				if owner.dialog == 0 {
					return 0
				}
				if time.Since(started) > 3*time.Second {
					procEndDialog.Call(uintptr(owner.dialog), 99)
					return 0
				}
				if acted {
					return 0
				}
				acted = true
				if enabled, _, _ := isEnabled.Call(uintptr(owner.hwnd)); enabled != 0 {
					t.Error("modal owner remains enabled")
				}
				if directory := os.Getenv("COMPANION_UI_SNAPSHOTS"); directory != "" {
					if err := captureDialog(owner.dialog, filepath.Join(directory, test.name+".png")); err != nil {
						t.Error(err)
					}
				}
				focus, _, _ := procGetFocus.Call()
				switch test.action {
				case "close":
					procPostMessage.Call(uintptr(owner.dialog), wmClose, 0, 0)
				case "escape":
					procPostMessage.Call(focus, 0x100, 0x1b, 0)
				case "confirm":
					button, _, _ := getDlgItem.Call(uintptr(owner.dialog), idYes)
					procSetFocus.Call(button)
					procPostMessage.Call(button, 0x100, 0x0d, 0)
				default:
					procPostMessage.Call(focus, 0x100, 0x0d, 0)
				}
				return 0
			})
			timer, _, _ := setTimer.Call(0, 0, 80, callback)
			if timer == 0 {
				t.Fatal("create native dialog watchdog")
			}
			result, err := d.run()
			killTimer.Call(0, timer)
			if err != nil {
				t.Fatal(err)
			}
			if result != test.want {
				t.Fatalf("dialog result = %d, want %d", result, test.want)
			}
			if owner.dialog != 0 || len(dialogWindows) != 0 || len(openingDialogs) != 0 || len(themedButtons) != 0 {
				t.Fatal("dialog resources retained after close")
			}
			if enabled, _, _ := isEnabled.Call(uintptr(owner.hwnd)); enabled == 0 {
				t.Fatal("modal owner not re-enabled")
			}
		})
	}
}

func TestCompanionNativeIcons(t *testing.T) {
	for _, dpi := range []int{96, 144, 192} {
		var icons companionIcons
		if err := icons.init(dpi); err != nil {
			t.Fatal(err)
		}
		if icons.small == 0 || icons.large == 0 || icons.small == icons.large {
			t.Fatal("missing independent icon handles")
		}
		icons.close()
	}
	if directory := os.Getenv("COMPANION_UI_SNAPSHOTS"); directory != "" {
		for _, size := range []int{16, 24, 32, 48} {
			if err := writePreview(filepath.Join(directory, fmt.Sprintf("icon-%d.png", size)), companionIconImage(size)); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestCompanionDiagnosticsControls(t *testing.T) {
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
	// Do not connect to the installed service or display a real pairing code.
	tray.setStatus("Service unavailable", trayStatusError)
	procShowWindow.Call(uintptr(tray.hwnd), swShow)
	getDlgItem := user32.NewProc("GetDlgItem")
	button, _, _ := getDlgItem.Call(uintptr(tray.hwnd), idDiagnostics)
	if button == 0 || windows.HWND(button) != tray.diagnostics {
		t.Fatal("diagnostics button missing")
	}
	enabled, _, _ := user32.NewProc("IsWindowEnabled").Call(button)
	if enabled == 0 {
		t.Fatal("diagnostics must remain available when the service is offline")
	}
	var client, bounds nativeRect
	user32.NewProc("GetClientRect").Call(uintptr(tray.hwnd), uintptr(unsafe.Pointer(&client)))
	for _, control := range []windows.HWND{tray.update, tray.diagnostics, tray.version} {
		procGetWindowRect.Call(uintptr(control), uintptr(unsafe.Pointer(&bounds)))
		user32.NewProc("MapWindowPoints").Call(0, uintptr(tray.hwnd), uintptr(unsafe.Pointer(&bounds)), 2)
		if bounds.Left < 0 || bounds.Top < 0 || bounds.Right > client.Right || bounds.Bottom > client.Bottom {
			t.Fatalf("control outside client area: %+v, client %+v", bounds, client)
		}
	}
	if directory := os.Getenv("COMPANION_UI_SNAPSHOTS"); directory != "" {
		if err := captureDialog(tray.hwnd, filepath.Join(directory, "diagnostics-controls.png")); err != nil {
			t.Fatal(err)
		}
	}
}

func writePreview(path string, img image.Image) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}

func captureDialog(hwnd windows.HWND, path string) error {
	var rect nativeRect
	procGetWindowRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&rect)))
	width, height := rect.Right-rect.Left, rect.Bottom-rect.Top
	dc, _, _ := procGetDC.Call(uintptr(hwnd))
	defer procReleaseDC.Call(uintptr(hwnd), dc)
	memory, _, _ := gdi32.NewProc("CreateCompatibleDC").Call(dc)
	defer gdi32.NewProc("DeleteDC").Call(memory)
	info := struct {
		Size                   uint32
		Width, Height          int32
		Planes, Bits           uint16
		Compression, SizeImage uint32
		XPels, YPels           int32
		Used, Important        uint32
	}{Size: 40, Width: width, Height: -height, Planes: 1, Bits: 32}
	var pixels uintptr
	bitmap, _, _ := gdi32.NewProc("CreateDIBSection").Call(dc, uintptr(unsafe.Pointer(&info)), 0, uintptr(unsafe.Pointer(&pixels)), 0, 0)
	if bitmap == 0 {
		return fmt.Errorf("create preview bitmap")
	}
	defer procDeleteObject.Call(bitmap)
	old, _, _ := procSelectObject.Call(memory, bitmap)
	defer procSelectObject.Call(memory, old)
	if ok, _, _ := user32.NewProc("PrintWindow").Call(uintptr(hwnd), memory, 0); ok == 0 {
		return fmt.Errorf("render native dialog")
	}
	img := image.NewRGBA(image.Rect(0, 0, int(width), int(height)))
	procRtlMoveMemory.Call(uintptr(unsafe.Pointer(&img.Pix[0])), pixels, uintptr(len(img.Pix)))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+2] = img.Pix[i+2], img.Pix[i]
		img.Pix[i+3] = 255
	}
	return writePreview(path, img)
}
