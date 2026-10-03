//go:build windows

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime/debug"
	"sync"
	"time"
)

const logMaxBytes = 2 * 1024 * 1024

var logMu sync.Mutex

// Logs contain only explicitly selected fields, never bodies, headers or config.
func logEvent(event string, fields map[string]any) {
	entry := map[string]any{"time": time.Now().UTC().Format(time.RFC3339Nano), "event": event, "pid": os.Getpid()}
	for key, value := range fields {
		entry[key] = value
	}
	content, err := json.Marshal(entry)
	if err != nil {
		return
	}
	logMu.Lock()
	defer logMu.Unlock()
	_ = appendLog(filepath.Join(dataDirectory(), "service.log"), append(content, '\n'), logMaxBytes)
}

func appendLog(path string, content []byte, limit int64) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if info, err := os.Stat(path); err == nil && info.Size()+int64(len(content)) > limit {
		if err := os.Remove(path + ".1"); err != nil && !os.IsNotExist(err) {
			return err
		}
		if err := os.Rename(path, path+".1"); err != nil {
			return err
		}
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.Write(content)
	return err
}

func writeServiceLog(message string) {
	logEvent("service.error", map[string]any{"error": message})
}

func captureCrashLog() {
	if err := os.MkdirAll(dataDirectory(), 0o700); err != nil {
		return
	}
	path := filepath.Join(dataDirectory(), "crash.log")
	if info, err := os.Stat(path); err == nil && info.Size() > 0 {
		_ = os.Remove(path + ".1")
		_ = os.Rename(path, path+".1")
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer file.Close()
	_ = debug.SetCrashOutput(file, debug.CrashOptions{})
}
