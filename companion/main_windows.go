//go:build windows

package main

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows/svc"
)

func main() {
	args := os.Args[1:]
	mode, err := companionLaunchMode(args, svc.IsWindowsService)
	if err != nil {
		showTrayError(err)
		os.Exit(1)
	}
	switch mode {
	case "--console":
		if err := runConsole(args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "--tray", "--show":
		if err := runTray(mode == "--show"); err != nil {
			showTrayError(err)
			os.Exit(1)
		}
	case "--update":
		os.Exit(runUpdater(args[1:]))
	case "--service":
		captureCrashLog()
		if err := runService(); err != nil {
			writeServiceLog("dispatcher: " + err.Error())
			os.Exit(1)
		}
	default:
		fmt.Fprintln(os.Stderr, "Usage: JonaHomelab.Companion.exe [--service|--tray|--show|--update|--console --simulate-shutdown <directory>]")
		os.Exit(2)
	}
}

func companionLaunchMode(args []string, isService func() (bool, error)) (string, error) {
	if mode := firstArg(args); mode != "" {
		return mode, nil
	}
	service, err := isService()
	if err != nil {
		return "", fmt.Errorf("detect Companion launch context: %w", err)
	}
	if service {
		return "--service", nil
	}
	return "--show", nil
}

func firstArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}
