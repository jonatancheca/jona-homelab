package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

var errPowerUnsupported = errors.New("power state unavailable in logind")

func systemCommand(timeout time.Duration, binary string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	command := exec.CommandContext(ctx, binary, args...)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	out, err := command.Output()
	message := strings.TrimSpace(string(out))
	if err != nil {
		return "", fmt.Errorf("%s: %w: %.400s", binary, err, strings.TrimSpace(stderr.String()))
	}
	return message, nil
}

func changePower(verb string, force bool) error {
	policy := "--check-inhibitors=yes"
	if force {
		policy = "--check-inhibitors=no"
	}
	_, err := systemCommand(5*time.Second, "/usr/bin/systemctl", "--no-ask-password", "--no-block", policy, verb)
	return err
}

func executeShutdown(force bool) error { return changePower("poweroff", force) }
func prepareSuspend(action powerAction) (func() error, error) {
	verb, method := "suspend", "CanSuspend"
	switch action {
	case powerSleep:
	case powerHibernate:
		verb, method = "hibernate", "CanHibernate"
	default:
		return nil, errPowerUnsupported
	}
	out, err := systemCommand(4*time.Second, "/usr/bin/busctl", "--system", "call", "org.freedesktop.login1", "/org/freedesktop/login1", "org.freedesktop.login1.Manager", method)
	if err != nil {
		return nil, err
	}
	if out != "s \"yes\"" {
		return nil, fmt.Errorf("%w: %s", errPowerUnsupported, action)
	}
	return func() error { return changePower(verb, false) }, nil
}
