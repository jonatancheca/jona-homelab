//go:build windows

package main

import (
	"testing"

	"golang.org/x/sys/windows"
)

func TestCreatePipeDoesNotCorruptHeap(t *testing.T) {
	handle, err := createPipe()
	if err != nil {
		t.Fatal(err)
	}
	if handle == windows.InvalidHandle {
		t.Fatal("createPipe returned an invalid handle")
	}
	if err := windows.CloseHandle(handle); err != nil {
		t.Fatal(err)
	}
}
