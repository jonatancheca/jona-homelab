//go:build windows

package main

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestDiagnosticsExportsBesideScriptAndHonorsExplicitDestination(t *testing.T) {
	content, err := os.ReadFile("diagnostics.ps1")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	app := filepath.Join(root, "Companion's app & test")
	data := filepath.Join(root, "data")
	for _, directory := range []string{app, data} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	script := filepath.Join(app, "diagnostics.ps1")
	if err := os.WriteFile(script, content, 0o600); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{
		"config.json":      `{"port":47654,"dpapiScope":"machine","encryptedSecret":"SECRET-MUST-NOT-APPEAR"}`,
		"pairing-code.txt": "PAIRING-MUST-NOT-APPEAR",
		"service.log":      "test-service-event",
	} {
		if err := os.WriteFile(filepath.Join(data, name), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, explicit := range []bool{false, true} {
		t.Run(fmt.Sprintf("explicit=%v", explicit), func(t *testing.T) {
			destination := filepath.Join(app, "diagnostics")
			args := []string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", script, "-DataDirectory", data}
			if explicit {
				destination = filepath.Join(root, "custom reports")
				args = append(args, "-OutputDirectory", destination)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, "powershell.exe", args...)
			command.Dir = root // The caller's working directory must not affect the default.
			command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("diagnostics script: %v; %s", err, output)
			}
			archives, err := filepath.Glob(filepath.Join(destination, "*.zip"))
			if err != nil || len(archives) != 1 {
				t.Fatalf("expected one ZIP in %s, got %v: %v", destination, archives, err)
			}
			archive, err := zip.OpenReader(archives[0])
			if err != nil {
				t.Fatal(err)
			}
			defer archive.Close()
			entries := make(map[string]string)
			for _, file := range archive.File {
				reader, err := file.Open()
				if err != nil {
					t.Fatal(err)
				}
				value, err := io.ReadAll(reader)
				reader.Close()
				if err != nil {
					t.Fatal(err)
				}
				entries[file.Name] = string(value)
				if strings.Contains(string(value), "MUST-NOT-APPEAR") || file.Name == "config.json" || file.Name == "pairing-code.txt" {
					t.Fatalf("private data included in %s", file.Name)
				}
			}
			if !strings.Contains(entries["service.log.txt"], "test-service-event") {
				t.Fatal("service log missing from archive")
			}
			for _, name := range []string{"LEEME.txt", "service.txt", "health.txt", "configuration-summary.txt", "power-states.txt", "windows-events.txt", "firewall.txt", "network.txt", "tray-task.txt", "tray-processes.txt", "tray-events.txt"} {
				if _, found := entries[name]; !found {
					t.Errorf("missing section %s", name)
				}
			}
		})
	}
}

func TestDiagnosticsArgumentsTreatScriptPathLiterally(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "Companion's $app & test")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(directory, "diagnostics.ps1")
	if err := os.WriteFile(script, []byte("Write-Output $PSCommandPath"), 0o600); err != nil {
		t.Fatal(err)
	}
	system, err := windows.GetSystemDirectory()
	if err != nil {
		t.Fatal(err)
	}
	powershell := filepath.Join(system, "WindowsPowerShell", "v1.0", "powershell.exe")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, powershell)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CmdLine: syscall.EscapeArg(powershell) + " " + diagnosticsArguments(script)}
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("diagnostics script: %v; %s", err, output)
	}
	// PowerShell expands short (8.3) paths, including the runner's TEMP path.
	// Compare file identity while still requiring the literal script to run.
	want, err := os.Stat(script)
	if err != nil {
		t.Fatal(err)
	}
	actual := strings.TrimSpace(string(output))
	got, err := os.Stat(actual)
	if err != nil || !os.SameFile(want, got) {
		t.Fatalf("script path not preserved: got %q, want %q: %v", actual, script, err)
	}
}

func TestDiagnosticsDialogReportsCompletionCancellationAndFailure(t *testing.T) {
	directory := `C:\Program Files\JonaHomelabCompanion\current\diagnostics`
	success := diagnosticsDialogContent(directory, nil)
	if success.title != "Diagnostics ready" || !strings.Contains(success.body, directory) || success.confirm != "Open folder" || success.dismiss != "Close" {
		t.Fatal("completion must identify the saved report directory")
	}
	cancelled := diagnosticsDialogContent(directory, fmt.Errorf("start: %w", windows.ERROR_CANCELLED))
	if cancelled.title != "Diagnostics cancelled" || cancelled.tone != dialogWarning || cancelled.confirm != "" {
		t.Fatal("UAC cancellation must not claim success or a service outage")
	}
	failure := diagnosticsDialogContent(directory, fmt.Errorf("script exited with code 1"))
	if failure.title != "Could not generate diagnostics" || failure.tone != dialogError || !strings.Contains(failure.body, "code 1") || failure.confirm != "" {
		t.Fatal("generation failure must be reported")
	}
}

func TestOpenDiagnosticsFolderRejectsMissingDirectory(t *testing.T) {
	if err := openDiagnosticsFolder(0, filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing diagnostics directory reported as opened")
	}
}

func TestDiagnosticsNativeLauncherWaitsForProcessResult(t *testing.T) {
	system, err := windows.GetSystemDirectory()
	if err != nil {
		t.Fatal(err)
	}
	powershell := filepath.Join(system, "WindowsPowerShell", "v1.0", "powershell.exe")
	for _, exitCode := range []int{0, 17} {
		script := filepath.Join(t.TempDir(), "diagnostics test.ps1")
		if err := os.WriteFile(script, []byte(fmt.Sprintf("exit %d", exitCode)), 0o600); err != nil {
			t.Fatal(err)
		}
		// Exercise the actual ShellExecuteEx ABI and process wait without a UAC
		// prompt or any elevated collection. Production always passes runas.
		err := runDiagnosticsProcess(0, powershell, script, "open")
		if exitCode == 0 && err != nil {
			t.Fatalf("successful collector rejected: %v", err)
		}
		if exitCode != 0 && (err == nil || !strings.Contains(err.Error(), "code 17")) {
			t.Fatalf("collector failure hidden: %v", err)
		}
	}
}

func TestCompanionPackageRequiresDiagnostics(t *testing.T) {
	directory := t.TempDir()
	version := "main-0123456789ab"
	for _, name := range []string{"JonaHomelab.Companion.exe", "install.ps1", "uninstall.ps1", "tray-task.ps1", "README.md", "RELEASE_VERSION"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(version), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := validatePackage(directory, version); err == nil {
		t.Fatal("an update without the diagnostics script must be rejected")
	}
	if err := os.WriteFile(filepath.Join(directory, "diagnostics.ps1"), []byte("# collector"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validatePackage(directory, version); err != nil {
		t.Fatalf("complete package rejected: %v", err)
	}
	if err := os.Remove(filepath.Join(directory, "tray-task.ps1")); err != nil {
		t.Fatal(err)
	}
	if err := validatePackage(directory, version); err == nil {
		t.Fatal("an update without tray task repair must be rejected")
	}
}
