//go:build windows

package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
)

// Console mode is deliberately simulation-only and never starts the updater or tray pipe.
func runConsole(args []string) error {
	if len(args) != 2 || args[0] != "--simulate-shutdown" || !filepath.IsAbs(args[1]) {
		return errors.New("usage: --console --simulate-shutdown <absolute isolated data directory>")
	}
	if err := os.Setenv("ProgramData", args[1]); err != nil {
		return err
	}
	config, err := loadConfig()
	if err != nil {
		return err
	}
	state := newRuntimeState(config)
	state.simulated = true
	state.shutdown = newShutdownExecutor(func(force bool) error {
		logEvent("shutdown.simulated", map[string]any{"force": force})
		return nil
	})
	state.shutdown.prepare = func(action powerAction) (func() error, error) {
		return func() error {
			logEvent("power.simulated", map[string]any{"action": action})
			return nil
		}, nil
	}
	code, err := config.pairingCode()
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dataDirectory(), "pairing-code.txt"), []byte(code), 0o600); err != nil {
		return err
	}
	listener, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", companionPort))
	if err != nil {
		return err
	}
	defer listener.Close()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	logEvent("console.simulation", map[string]any{"port": companionPort})
	return runHTTP(ctx, state, listener)
}
