//go:build windows

package main

import (
	"fmt"
	"os"
)

func main() {
	args := os.Args[1:]
	switch firstArg(args) {
	case "--tray":
		if err := runTray(); err != nil {
			showTrayError(err)
			os.Exit(1)
		}
	case "--update":
		os.Exit(runUpdater(args[1:]))
	case "--service", "":
		if err := runService(); err != nil {
			writeServiceLog("dispatcher: " + err.Error())
			os.Exit(1)
		}
	default:
		fmt.Fprintln(os.Stderr, "Usage: JonaHomelab.Companion.exe [--service|--tray|--update]")
		os.Exit(2)
	}
}

func firstArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}
