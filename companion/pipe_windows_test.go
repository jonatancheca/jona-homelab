//go:build windows

package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

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

func TestPipeReplySurvivesDisconnectAndServerStops(t *testing.T) {
	store, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { runPipeServer(ctx, newRuntimeState(store)); close(done) }()
	for i := 0; i < 8; i++ {
		info, err := callPipe("get-info")
		if err != nil || !info.Ready || info.PairingCode == "" {
			t.Fatalf("reply %d: %v", i, err)
		}
	}
	response, err := callPipeRaw("check-update")
	if err != nil {
		t.Fatalf("local update check failed over pipe: %v", err)
	}
	var result updateCheckResult
	if err := json.Unmarshal(response, &result); err != nil || !result.LocalBuild || result.Scheduled {
		t.Fatalf("unexpected local update reply: %s", response)
	}
	_, err = callPipeRaw("unknown-action")
	var operationError *pipeOperationError
	if !errors.As(err, &operationError) || operationError.Error() != "Unknown action." {
		t.Fatalf("operation error reported as connection failure: %v", err)
	}
	if info, err := callPipe("get-info"); err != nil || !info.Ready {
		t.Fatalf("service unavailable after operation error: %v", err)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("pipe server did not stop")
	}
}
