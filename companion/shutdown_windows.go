//go:build windows

package main

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

func shutdownArguments(force bool) []string {
	args := []string{"/s", "/t", "0"}
	if force {
		args = append(args, "/f")
	}
	return args
}

func executeShutdown(force bool) error {
	directory, err := windows.GetSystemDirectory()
	if err != nil {
		return fmt.Errorf("system directory: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, filepath.Join(directory, "shutdown.exe"), shutdownArguments(force)...)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("shutdown.exe: %w; %s", err, output)
	}
	return nil
}
