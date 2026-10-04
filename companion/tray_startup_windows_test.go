//go:build windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestCompanionLaunchMode(t *testing.T) {
	for _, test := range []struct {
		args    []string
		service bool
		want    string
	}{
		{nil, false, "--show"},
		{nil, true, "--service"},
		{[]string{"--tray"}, false, "--tray"},
		{[]string{"--service"}, false, "--service"},
		{[]string{"--show"}, false, "--show"},
		{[]string{"--console", "--simulate-shutdown", "test"}, false, "--console"},
		{[]string{"--update"}, false, "--update"},
		{[]string{"--unknown"}, false, "--unknown"},
	} {
		got, err := companionLaunchMode(test.args, func() (bool, error) {
			if len(test.args) != 0 {
				t.Fatal("explicit mode must not depend on service detection")
			}
			return test.service, nil
		})
		if err != nil || got != test.want {
			t.Fatalf("launch %v, service=%v: got %q, %v; want %q", test.args, test.service, got, err, test.want)
		}
	}
	want := errors.New("token unavailable")
	if _, err := companionLaunchMode(nil, func() (bool, error) { return false, want }); !errors.Is(err, want) {
		t.Fatal("failed detection must not silently launch a service")
	}
}

func TestTrayTaskDefinitionSupportsPersistentInteractiveSessions(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// Build the actual task with Windows cmdlets, without registering or starting it.
	script := `$task = & '.\tray-task.ps1' -DefinitionOnly -OnlyIfPresent -ExecutablePath 'C:\Test installation\current\JonaHomelab.Companion.exe';
@{ Action = $task.Actions[0].Execute; Arguments = $task.Actions[0].Arguments;
Delay = $task.Triggers[0].Delay; AnyUser = [string]$task.Triggers[0].UserId;
Limited = [int]$task.Principal.RunLevel; Group = [string]$task.Principal.GroupId;
BatteryBlocked = $task.Settings.DisallowStartIfOnBatteries; BatteryStops = $task.Settings.StopIfGoingOnBatteries;
Limit = $task.Settings.ExecutionTimeLimit; Available = $task.Settings.StartWhenAvailable;
Instances = [int]$task.Settings.MultipleInstances; Retries = $task.Settings.RestartCount } | ConvertTo-Json -Compress`
	command := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("task definition: %v; %s", err, output)
	}
	var task struct {
		Action, Arguments, Delay, AnyUser, Group, Limit string
		Limited, Instances, Retries                     int
		BatteryBlocked, BatteryStops, Available         bool
	}
	if err := json.Unmarshal(output, &task); err != nil {
		t.Fatalf("task definition JSON: %v; %s", err, output)
	}
	if task.Action != `C:\Test installation\current\JonaHomelab.Companion.exe` || task.Arguments != "--tray" || task.Delay != "PT10S" || task.AnyUser != "" || task.Limited != 0 || task.Group == "" {
		t.Fatalf("non-interactive or incorrect logon action: %+v", task)
	}
	if task.BatteryBlocked || task.BatteryStops || task.Limit != "PT0S" || !task.Available || task.Instances != 0 || task.Retries != 3 {
		t.Fatalf("tray can be suppressed or killed by task settings: %+v", task)
	}
}

// A separate real Win32 process exercises main(), its message loop, the mutex
// and cross-process activation. TestMain isolates both UI names and the pipe.
func TestTrayLaunchProcess(t *testing.T) {
	if mode := os.Getenv("COMPANION_TEST_LAUNCH"); mode != "" {
		os.Args = os.Args[:1]
		if mode == "hidden" {
			os.Args = append(os.Args, "--tray")
		}
		main()
		return
	}
	if os.Getenv("COMPANION_UI_TEST") != "1" {
		t.Skip("set COMPANION_UI_TEST=1 to exercise native windows")
	}
	find := func() uintptr {
		hwnd, _, _ := user32.NewProc("FindWindowW").Call(uintptr(unsafe.Pointer(utf16(windowClassName))), 0)
		return hwnd
	}
	wait := func(t *testing.T, description string, condition func() bool) {
		t.Helper()
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			if condition() {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal(description)
	}
	visible := func(hwnd uintptr) bool {
		value, _, _ := user32.NewProc("IsWindowVisible").Call(hwnd)
		return value != 0
	}
	for _, mode := range []string{"hidden", "show"} {
		t.Run(mode, func(t *testing.T) {
			start := func(mode string) (*exec.Cmd, <-chan error) {
				t.Helper()
				command := exec.Command(os.Args[0], "-test.run=^TestTrayLaunchProcess$")
				command.Env = append(os.Environ(), "COMPANION_TEST_LAUNCH="+mode, fmt.Sprintf("COMPANION_TEST_INSTANCE=%d", os.Getpid()))
				// Suppress the test console without setting STARTF_USESHOWWINDOW:
				// SW_HIDE there overrides the application's first ShowWindow call.
				command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
				var output bytes.Buffer
				command.Stdout, command.Stderr = &output, &output
				if err := command.Start(); err != nil {
					t.Fatal(err)
				}
				done := make(chan error, 1)
				go func() {
					err := command.Wait()
					if err != nil {
						err = fmt.Errorf("%w: %s", err, output.String())
					}
					done <- err
				}()
				t.Cleanup(func() { _ = command.Process.Kill() })
				return command, done
			}
			first, done := start(mode)
			wait(t, "tray process never created its window", func() bool { return find() != 0 })
			hwnd := find()
			if mode == "show" {
				wait(t, "double click did not show Companion", func() bool { return visible(hwnd) })
			}
			if mode == "hidden" && visible(hwnd) {
				t.Fatal("logon unexpectedly opened the window")
			}
			var pid uint32
			user32.NewProc("GetWindowThreadProcessId").Call(hwnd, uintptr(unsafe.Pointer(&pid)))
			if int(pid) != first.Process.Pid {
				t.Fatal("found another process's window")
			}
			_, silentDuplicate := start("hidden")
			select {
			case err := <-silentDuplicate:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("duplicate logon process did not exit")
			}
			if visible(hwnd) != (mode == "show") {
				t.Fatal("duplicate logon changed window visibility")
			}
			for _, state := range []string{"hidden", "minimized"} {
				if state == "hidden" {
					procPostMessage.Call(hwnd, wmClose, 0, 0)
					wait(t, "window did not hide", func() bool { return !visible(hwnd) })
				} else {
					procPostMessage.Call(hwnd, 0x0112, 0xF020, 0) // WM_SYSCOMMAND / SC_MINIMIZE.
					wait(t, "window did not minimize", func() bool {
						value, _, _ := user32.NewProc("IsIconic").Call(hwnd)
						return value != 0
					})
				}
				_, duplicate := start("show")
				select {
				case err := <-duplicate:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(8 * time.Second):
					t.Fatal("duplicate process did not exit")
				}
				wait(t, "existing window was not restored", func() bool {
					minimized, _, _ := user32.NewProc("IsIconic").Call(hwnd)
					return visible(hwnd) && minimized == 0
				})
				if find() != hwnd {
					t.Fatal("second launch replaced the original window")
				}
			}
			procPostMessage.Call(hwnd, wmCommand, idExit, 0)
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("tray did not exit")
			}
		})
	}
}

func TestTrayRetriesBeforeExplorerIsReady(t *testing.T) {
	if os.Getenv("COMPANION_UI_TEST") != "1" {
		t.Skip("set COMPANION_UI_TEST=1 to exercise native windows")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	original := notifyTrayIcon
	defer func() { notifyTrayIcon = original }()
	attempts := 0
	notifyTrayIcon = func(operation uintptr, icon *notifyIconData) (uintptr, error) {
		if operation == nimAdd {
			attempts++
			if attempts <= 2 {
				return 0, windows.ERROR_NOT_READY
			}
		}
		return original(operation, icon)
	}
	tray := &trayApplication{}
	activeTray = tray
	defer func() { tray.close(); activeTray = nil }()
	if err := tray.create(); err != nil {
		t.Fatal(err)
	}
	if !tray.iconRetry {
		t.Fatal("missing retry after Explorer was unavailable at logon")
	}
	// Explorer's broadcast is not proof its notification area is ready.
	tray.windowProc(tray.hwnd, tray.taskbarCreated, 0, 0)
	deadline := time.Now().Add(5 * time.Second)
	for tray.iconRetry && time.Now().Before(deadline) {
		var message nativeMessage
		found, _, _ := user32.NewProc("PeekMessageW").Call(uintptr(unsafe.Pointer(&message)), uintptr(tray.hwnd), 0, 0, 1)
		if found != 0 {
			procDispatchMessage.Call(uintptr(unsafe.Pointer(&message)))
		}
		time.Sleep(time.Millisecond)
	}
	if tray.iconRetry || attempts != 3 {
		t.Fatalf("icon not restored by timer: attempts=%d", attempts)
	}
	if visible, _, _ := user32.NewProc("IsWindowVisible").Call(uintptr(tray.hwnd)); visible != 0 {
		t.Fatal("logon recovery opened an unsolicited window")
	}
	tray.removeIcon()
	tray.windowProc(tray.hwnd, tray.taskbarCreated, 0, 0)
	if attempts != 4 || tray.iconRetry {
		t.Fatal("Explorer restart did not restore the icon")
	}
}
