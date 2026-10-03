//go:build windows

package main

import (
	"fmt"
	"os"
)

func main() {
	args := os.Args[1:]
	switch firstArg(args) {
	case "--console":
		if err := runConsole(args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "--tray":
		if err := runTray(); err != nil {
			showTrayError(err)
			os.Exit(1)
		}
	case "--update":
		os.Exit(runUpdater(args[1:]))
	case "--service", "":
		captureCrashLog()
		if err := runService(); err != nil {
			writeServiceLog("dispatcher: " + err.Error())
			os.Exit(1)
		}
	default:
		fmt.Fprintln(os.Stderr, "Usage: JonaHomelab.Companion.exe [--service|--tray|--update|--console --simulate-shutdown <directory>]")
		os.Exit(2)
	}
}

func firstArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}
