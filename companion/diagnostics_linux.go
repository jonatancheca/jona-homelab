package main

import (
	"encoding/json"
	"os"
	"time"
)

// Collect only fixed, local sources. The pairing file and authentication headers
// never enter the report or the journal.
func diagnostics() error {
	report := map[string]any{"platform": companionPlatform, "version": releaseVersion(), "displayVersion": companionVersion(), "update": readUpdateStatus()}
	for _, section := range []struct {
		name, binary string
		args         []string
	}{
		{"service", "/usr/bin/systemctl", []string{"show", serviceUnit, "--property=ActiveState,SubState,Result"}},
		{"journal", "/usr/bin/journalctl", []string{"--no-pager", "-n", "100", "-u", serviceUnit, "-o", "short-iso"}},
		{"network", "/usr/bin/ip", []string{"-brief", "-4", "address"}},
	} {
		output, err := systemCommand(5*time.Second, section.binary, section.args...)
		if err != nil {
			report[section.name] = err.Error()
		} else {
			report[section.name] = output
		}
	}
	return json.NewEncoder(os.Stdout).Encode(report)
}
