//go:build windows

package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

type updateStatus struct {
	Phase         string `json:"phase"`
	TargetVersion string `json:"targetVersion,omitempty"`
	Error         string `json:"error,omitempty"`
	UpdatedAt     string `json:"updatedAt,omitempty"`
	PID           uint32 `json:"pid,omitempty"`
}

func (s updateStatus) active() bool {
	switch s.Phase {
	case "checking", "scheduled", "downloading", "verifying", "installing", "restarting":
		return true
	}
	return false
}

func updateStatusPath() string { return filepath.Join(dataDirectory(), "update-status.json") }

// The handle stays exclusively open across service restarts and is released by
// Windows if the updater crashes. It does not depend on a particular Go thread.
func acquireUpdateLock() (windows.Handle, error) {
	if err := os.MkdirAll(dataDirectory(), 0o700); err != nil {
		return windows.InvalidHandle, err
	}
	name, err := windows.UTF16PtrFromString(filepath.Join(dataDirectory(), "update.lock"))
	if err != nil {
		return windows.InvalidHandle, err
	}
	return windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_ALWAYS, windows.FILE_ATTRIBUTE_NORMAL, 0)
}

func readUpdateStatus() updateStatus {
	var state updateStatus
	content, err := os.ReadFile(updateStatusPath())
	if err != nil || json.Unmarshal(content, &state) != nil {
		return updateStatus{Phase: "idle"}
	}
	if state.active() {
		started, err := time.Parse(time.RFC3339Nano, state.UpdatedAt)
		if err != nil || time.Since(started) > 15*time.Minute || !updateProcessRunning(state.PID) {
			state.Phase, state.Error = "failed", "Update interrupted. Check diagnostics and retry."
		}
	}
	return state
}

func updateProcessRunning(pid uint32) bool {
	if pid == 0 {
		return false
	}
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, pid)
	if err != nil {
		return errors.Is(err, windows.ERROR_ACCESS_DENIED)
	}
	defer windows.CloseHandle(handle)
	result, err := windows.WaitForSingleObject(handle, 0)
	return err == nil && result == uint32(windows.WAIT_TIMEOUT)
}

func writeUpdateStatus(state updateStatus) error {
	state.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	state.PID = uint32(os.Getpid())
	content, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dataDirectory(), 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(dataDirectory(), "update-status-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write(content)
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	from, _ := windows.UTF16PtrFromString(file.Name())
	to, _ := windows.UTF16PtrFromString(updateStatusPath())
	return windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}

// Resolve junctions through Windows itself. EvalSymlinks can leave a junction
// unchanged (notably with winsymlink=0), which previously caused exit code 2.
func finalDirectory(path string) (string, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	handle, err := windows.CreateFile(name, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(handle)
	buffer := make([]uint16, 32768)
	n, err := windows.GetFinalPathNameByHandle(handle, &buffer[0], uint32(len(buffer)), 0)
	if err != nil {
		return "", err
	}
	if n >= uint32(len(buffer)) {
		return "", errors.New("installation path is too long")
	}
	result := windows.UTF16ToString(buffer[:n])
	if strings.HasPrefix(result, `\\?\UNC\`) {
		return `\\` + result[8:], nil
	}
	return strings.TrimPrefix(result, `\\?\`), nil
}
