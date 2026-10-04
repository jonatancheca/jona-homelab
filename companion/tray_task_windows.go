//go:build windows

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

// Older updaters only switch the release junction. The new service repairs an
// existing tray task itself so the first upgrade also receives the new settings.
func refreshInstalledTrayTask(ctx context.Context) {
	executable, err := os.Executable()
	if err != nil {
		return
	}
	root := installationRoot(executable)
	if root == "" {
		return
	}
	current := filepath.Join(root, "current")
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", filepath.Join(current, "tray-task.ps1"), "-ExecutablePath", filepath.Join(current, "JonaHomelab.Companion.exe"), "-OnlyIfPresent")
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := command.Run(); err != nil {
		logEvent("tray.task_failed", map[string]any{"error": err.Error()})
	}
}
