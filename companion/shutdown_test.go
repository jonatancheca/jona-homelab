//go:build windows

package main

import (
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestShutdownExecutorCooldownAndForce(t *testing.T) {
	var mu sync.Mutex
	var calls []bool
	executor := newShutdownExecutor(func(force bool) error {
		mu.Lock()
		calls = append(calls, force)
		mu.Unlock()
		return nil
	})
	if accepted, err := executor.trySchedule(false); !accepted || err != nil {
		t.Fatal("safe shutdown was not scheduled")
	}
	if accepted, _ := executor.trySchedule(true); accepted {
		t.Fatal("cooldown did not reject second request")
	}
	time.Sleep(450 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 1 || calls[0] {
		t.Fatalf("unexpected shutdown calls: %#v", calls)
	}
}

func TestShutdownFailureIsNotAccepted(t *testing.T) {
	executor := newShutdownExecutor(func(bool) error { return errors.New("access denied") })
	if accepted, err := executor.trySchedule(false); accepted || err == nil {
		t.Fatalf("failed command accepted: %v, %v", accepted, err)
	}
}

func TestSafeShutdownNeverImplicitlyForcesApps(t *testing.T) {
	if !reflect.DeepEqual(shutdownArguments(false), []string{"/s", "/t", "0"}) {
		t.Fatal("unsafe nonzero timeout or force flag")
	}
	if !reflect.DeepEqual(shutdownArguments(true), []string{"/s", "/t", "0", "/f"}) {
		t.Fatal("forced shutdown arguments")
	}
}
