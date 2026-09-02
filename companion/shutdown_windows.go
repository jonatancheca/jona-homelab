//go:build windows

package main

import (
	"os/exec"
	"sync"
	"time"
)

const shutdownCooldown = 10 * time.Second

type shutdownExecutor struct {
	mu      sync.Mutex
	last    time.Time
	execute func(bool)
}

func newShutdownExecutor(execute func(bool)) *shutdownExecutor {
	if execute == nil {
		execute = executeShutdown
	}
	return &shutdownExecutor{execute: execute}
}

func (s *shutdownExecutor) trySchedule(force bool) bool {
	now := time.Now()
	s.mu.Lock()
	if !s.last.IsZero() && now.Sub(s.last) < shutdownCooldown {
		s.mu.Unlock()
		return false
	}
	s.last = now
	s.mu.Unlock()
	go func() {
		time.Sleep(300 * time.Millisecond)
		s.execute(force)
	}()
	return true
}

func executeShutdown(force bool) {
	args := []string{"/s", "/t", "0"}
	if force {
		args = append(args, "/f")
	}
	command := exec.Command(`C:\Windows\System32\shutdown.exe`, args...)
	_ = command.Start()
}
