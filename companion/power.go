//go:build linux || windows

package main

import (
	"sync"
	"time"
)

type powerAction string

const (
	powerShutdown  powerAction = "shutdown"
	powerSleep     powerAction = "sleep"
	powerHibernate powerAction = "hibernate"
)

const shutdownCooldown = 10 * time.Second

type shutdownExecutor struct {
	mu      sync.Mutex
	last    time.Time
	execute func(bool) error
	prepare func(powerAction) (func() error, error)
	pending bool
}

func newShutdownExecutor(execute func(bool) error) *shutdownExecutor {
	if execute == nil {
		execute = executeShutdown
	}
	return &shutdownExecutor{execute: execute, prepare: prepareSuspend}
}

// A 202 means the OS accepted the command, not merely that a goroutine started.
func (s *shutdownExecutor) trySchedule(force bool) (bool, error) {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending || (!s.last.IsZero() && now.Sub(s.last) < shutdownCooldown) {
		return false, nil
	}
	s.last = now
	logEvent("shutdown.executing", map[string]any{"force": force})
	if err := s.execute(force); err != nil {
		logEvent("shutdown.failed", map[string]any{"force": force, "error": err.Error()})
		return false, err
	}
	logEvent("shutdown.accepted", map[string]any{"force": force})
	return true, nil
}

func (s *shutdownExecutor) trySuspend(action powerAction) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending || (!s.last.IsZero() && time.Since(s.last) < shutdownCooldown) {
		return false, nil
	}
	execute, err := s.prepare(action)
	if err != nil {
		logEvent("power.rejected", map[string]any{"action": action, "error": err.Error()})
		return false, err
	}
	s.last, s.pending = time.Now(), true
	logEvent("power.scheduled", map[string]any{"action": action})
	go func() {
		// The system may suspend before the command returns. Reply before entering sleep.
		time.Sleep(300 * time.Millisecond)
		logEvent("power.executing", map[string]any{"action": action})
		if err := execute(); err != nil {
			logEvent("power.failed", map[string]any{"action": action, "error": err.Error()})
		} else {
			logEvent("power.completed", map[string]any{"action": action})
		}
		s.mu.Lock()
		s.pending = false
		s.last = time.Now()
		s.mu.Unlock()
	}()
	return true, nil
}
