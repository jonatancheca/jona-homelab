//go:build windows

package main

import (
	"sync"
	"testing"
	"time"
)

func TestShutdownExecutorCooldownAndForce(t *testing.T) {
	var mu sync.Mutex
	var calls []bool
	executor := newShutdownExecutor(func(force bool) {
		mu.Lock()
		calls = append(calls, force)
		mu.Unlock()
	})
	if !executor.trySchedule(false) {
		t.Fatal("safe shutdown was not scheduled")
	}
	if executor.trySchedule(true) {
		t.Fatal("cooldown did not reject second request")
	}
	time.Sleep(450 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 1 || calls[0] {
		t.Fatalf("unexpected shutdown calls: %#v", calls)
	}
}
