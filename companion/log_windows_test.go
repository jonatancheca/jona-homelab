//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	directory, err := os.MkdirTemp("", "companion-tests-")
	if err != nil {
		panic(err)
	}
	_ = os.Setenv("ProgramData", directory)
	pipeName = fmt.Sprintf(`\\.\pipe\JonaHomelabCompanion-test-%d`, os.Getpid())
	code := m.Run()
	_ = os.RemoveAll(directory)
	os.Exit(code)
}

func TestLogRotationBoundsDiskUsage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "service.log")
	for i := 0; i < 10; i++ {
		if err := appendLog(path, []byte(strings.Repeat("x", 32)), 64); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{path, path + ".1"} {
		info, err := os.Stat(file)
		if err != nil || info.Size() > 64 {
			t.Fatalf("unbounded log: %v", err)
		}
	}
}
