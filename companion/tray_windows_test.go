//go:build windows

package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestUpdateResultsDoNotReportAServiceOutage(t *testing.T) {
	message := updateCheckMessage(updateCheckResult{LocalBuild: true})
	if !strings.Contains(message, "local build") || !strings.Contains(message, "manually") || strings.Contains(message, "up to date") {
		t.Fatalf("local build presented as an update check success: %s", message)
	}
	for _, err := range []error{nil, &pipeOperationError{message: "release check returned HTTP 503"}, fmt.Errorf("update: %w", &pipeOperationError{message: "invalid release metadata"})} {
		if _, status := trayServiceStatus(err); status != trayStatusConnected {
			t.Fatalf("reachable service marked offline: %v", err)
		}
	}
	if _, status := trayServiceStatus(errors.New("pipe unavailable")); status != trayStatusError {
		t.Fatal("actual service connection failure hidden")
	}
}

func TestTrayEventKindUsesLowWordWithIconID(t *testing.T) {
	tests := []struct {
		name   string
		event  uint32
		iconID uint16
		want   trayEventKind
	}{
		{name: "single left click", event: wmLButtonDown, iconID: 1, want: trayEventShow},
		{name: "double left click", event: wmLButtonDblClk, iconID: 9, want: trayEventShow},
		{name: "enter after mouse selection", event: ninSelect, iconID: 1, want: trayEventShow},
		{name: "keyboard activation", event: ninKeySelect, iconID: 7, want: trayEventShow},
		{name: "context menu", event: wmContextMenu, iconID: 1, want: trayEventMenu},
		{name: "legacy right button down", event: wmRButtonDown, iconID: 2, want: trayEventMenu},
		{name: "legacy right click", event: wmRButtonUp, iconID: 3, want: trayEventMenu},
		{name: "hover", event: 0x0200, iconID: 1, want: trayEventIgnored},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			lParam := uintptr(test.event) | uintptr(test.iconID)<<16
			if got := trayEventKindFor(lParam); got != test.want {
				t.Fatalf("tray event kind: got %d, want %d", got, test.want)
			}
		})
	}
}
