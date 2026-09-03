//go:build windows

package main

import "testing"

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
