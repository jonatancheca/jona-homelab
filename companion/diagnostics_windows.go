//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// SHELLEXECUTEINFOW, including the unused fields required by the Windows ABI.
type shellExecuteInfo struct {
	Size, Mask                        uint32
	Window                            windows.HWND
	Verb, File, Parameters, Directory *uint16
	Show                              int32
	Instance, IDList                  uintptr
	Class                             *uint16
	ClassKey                          windows.Handle
	HotKey                            uint32
	Icon, Process                     windows.Handle
}

var procShellExecuteEx = shell32.NewProc("ShellExecuteExW")

func generateDiagnostics(owner windows.HWND) (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate Companion: %w", err)
	}
	script := filepath.Join(filepath.Dir(executable), "diagnostics.ps1")
	directory := filepath.Join(filepath.Dir(script), "diagnostics")
	if info, err := os.Stat(script); err != nil || info.IsDir() {
		return directory, errors.New("diagnostics.ps1 is missing. Reinstall the complete Companion package")
	}
	system, err := windows.GetSystemDirectory()
	if err != nil {
		return directory, fmt.Errorf("locate Windows PowerShell: %w", err)
	}
	return directory, runDiagnosticsProcess(owner, filepath.Join(system, "WindowsPowerShell", "v1.0", "powershell.exe"), script, "runas")
}

func diagnosticsArguments(script string) string {
	args := []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-ExecutionPolicy", "Bypass", "-File", script}
	for index, arg := range args {
		args[index] = syscall.EscapeArg(arg)
	}
	return strings.Join(args, " ")
}

func runDiagnosticsProcess(owner windows.HWND, powershell, script, verb string) error {
	// The tray stays unprivileged. Elevate only this read-only collection, which
	// must also work while the service is stopped and write beside the executable.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED|windows.COINIT_DISABLE_OLE1DDE); err != nil && err != syscall.Errno(1) {
		return fmt.Errorf("initialize diagnostics launcher: %w", err)
	}
	defer windows.CoUninitialize()
	info := shellExecuteInfo{
		Mask:   0x40 | 0x100 | 0x400, // NOCLOSEPROCESS | NOASYNC | FLAG_NO_UI
		Window: owner, Verb: utf16(verb), File: utf16(powershell),
		Parameters: utf16(diagnosticsArguments(script)), Directory: utf16(filepath.Dir(script)), Show: swHide,
	}
	info.Size = uint32(unsafe.Sizeof(info))
	if ok, _, err := procShellExecuteEx.Call(uintptr(unsafe.Pointer(&info))); ok == 0 {
		return fmt.Errorf("start diagnostics: %w", err)
	}
	if info.Process == 0 {
		return errors.New("Windows did not return a diagnostics process handle")
	}
	defer windows.CloseHandle(info.Process)
	// Only this worker waits; the tray message loop remains responsive.
	if _, err := windows.WaitForSingleObject(info.Process, windows.INFINITE); err != nil {
		return fmt.Errorf("wait for diagnostics: %w", err)
	}
	var exitCode uint32
	if err := windows.GetExitCodeProcess(info.Process, &exitCode); err != nil {
		return fmt.Errorf("read diagnostics result: %w", err)
	}
	if exitCode != 0 {
		return fmt.Errorf("diagnostics.ps1 exited with code %d. Run the script as administrator to see the error", exitCode)
	}
	return nil
}

func diagnosticsDialogContent(directory string, err error) dialogContent {
	if errors.Is(err, windows.ERROR_CANCELLED) {
		return dialogContent{title: "Diagnostics cancelled", body: "Administrator permission was not granted. No diagnostic report was generated.", tone: dialogWarning}
	}
	if err != nil {
		return dialogContent{title: "Could not generate diagnostics", body: err.Error(), tone: dialogError}
	}
	return dialogContent{title: "Diagnostics ready", body: "The diagnostic ZIP was saved in:\n" + directory + "\n\nNothing was sent automatically."}
}
