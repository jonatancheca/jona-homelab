//go:build windows

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type pipeInfo struct {
	Ready          bool         `json:"ready"`
	Version        string       `json:"version"`
	DisplayVersion string       `json:"displayVersion,omitempty"`
	Port           int          `json:"port"`
	PairingCode    string       `json:"pairingCode"`
	LastServerCall string       `json:"lastServerCall,omitempty"`
	Update         updateStatus `json:"update"`
}

func runPipeServer(ctx context.Context, state *runtimeState) {
	for ctx.Err() == nil {
		handle, err := createPipe()
		if err != nil {
			writeServiceLog("pipe create: " + err.Error())
			time.Sleep(500 * time.Millisecond)
			continue
		}
		connected := connectPipe(ctx, handle)
		if connected {
			request, readErr := readPipeLine(handle, 64*1024)
			if readErr == nil {
				response := handlePipeRequest(ctx, state, request)
				if err := writePipeLine(handle, response); err == nil {
					// Wait for the client to consume the reply before disconnecting.
					// Older clients close after reading; new ones acknowledge it.
					_, _ = readPipeLine(handle, 16)
				} else {
					writeServiceLog("pipe write: " + err.Error())
				}
			}
		}
		_ = windows.DisconnectNamedPipe(handle)
		_ = windows.CloseHandle(handle)
	}
}

func createPipe() (windows.Handle, error) {
	// Only the service, administrators and interactive users can open it.
	// The server still validates every command and never logs or returns secrets
	// outside the local tray protocol.
	securityDescriptor, err := windows.SecurityDescriptorFromString("D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;GRGW;;;IU)")
	if err != nil {
		return windows.InvalidHandle, err
	}
	// SecurityDescriptorFromString returns a self-relative descriptor owned by
	// Go. Do not release it with LocalFree; that corrupts the Go heap.
	security := &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: securityDescriptor}
	name, err := windows.UTF16PtrFromString(pipeName)
	if err != nil {
		return windows.InvalidHandle, err
	}
	handle, err := windows.CreateNamedPipe(
		name,
		windows.PIPE_ACCESS_DUPLEX|windows.FILE_FLAG_OVERLAPPED,
		windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT|windows.PIPE_REJECT_REMOTE_CLIENTS,
		1,
		64*1024,
		64*1024,
		0,
		security,
	)
	runtime.KeepAlive(securityDescriptor)
	return handle, err
}

func connectPipe(ctx context.Context, handle windows.Handle) bool {
	_, err := pipeIO(ctx, handle, func(overlapped *windows.Overlapped) (uint32, error) {
		err := windows.ConnectNamedPipe(handle, overlapped)
		if errors.Is(err, windows.ERROR_PIPE_CONNECTED) {
			err = nil
		}
		return 0, err
	})
	return err == nil
}

// Keep buffers and OVERLAPPED alive until cancellation has completed.
func pipeIO(ctx context.Context, handle windows.Handle, start func(*windows.Overlapped) (uint32, error)) (uint32, error) {
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(event)
	overlapped := &windows.Overlapped{HEvent: event}
	count, err := start(overlapped)
	if !errors.Is(err, windows.ERROR_IO_PENDING) {
		return count, err
	}
	for {
		result, waitErr := windows.WaitForSingleObject(event, 100)
		if result == windows.WAIT_OBJECT_0 && waitErr == nil {
			err = windows.GetOverlappedResult(handle, overlapped, &count, false)
			return count, err
		}
		if waitErr != nil || ctx.Err() != nil {
			_ = windows.CancelIoEx(handle, overlapped)
			_ = windows.GetOverlappedResult(handle, overlapped, &count, true)
			if waitErr != nil {
				return count, waitErr
			}
			return count, ctx.Err()
		}
	}
}

func handlePipeRequest(ctx context.Context, state *runtimeState, request string) string {
	var command struct {
		Action string `json:"action"`
	}
	if json.Unmarshal([]byte(request), &command) != nil {
		return `{"error":"Invalid local request."}`
	}
	switch command.Action {
	case "get-info":
		code, err := state.config.pairingCode()
		if err != nil {
			return localError(err)
		}
		return marshalLocal(pipeInfo{Ready: true, Version: releaseVersion(), DisplayVersion: companionVersion(), Port: companionPort, PairingCode: code, LastServerCall: state.config.lastServerCall(), Update: readUpdateStatus()})
	case "rotate":
		logEvent("pairing.rotate", nil)
		code, err := state.config.rotateSecret()
		if err != nil {
			return localError(err)
		}
		return marshalLocal(pipeInfo{Ready: true, Version: releaseVersion(), DisplayVersion: companionVersion(), Port: companionPort, PairingCode: code, LastServerCall: state.config.lastServerCall(), Update: readUpdateStatus()})
	case "check-update":
		result, err := state.updates.checkAndSchedule(ctx)
		if err != nil {
			return localError(err)
		}
		return marshalLocal(result)
	default:
		return `{"error":"Unknown action."}`
	}
}

func localError(err error) string {
	if err == nil {
		return `{"error":"Companion operation failed."}`
	}
	return marshalLocal(struct {
		Error string `json:"error"`
	}{err.Error()})
}

func marshalLocal(value any) string {
	content, err := json.Marshal(value)
	if err != nil {
		return `{"error":"Companion operation failed."}`
	}
	return string(content)
}

func readPipeLine(handle windows.Handle, limit int) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	content := make([]byte, 0, 1024)
	buffer := make([]byte, 1024)
	for len(content) < limit {
		read, err := pipeIO(ctx, handle, func(overlapped *windows.Overlapped) (uint32, error) {
			var count uint32
			err := windows.ReadFile(handle, buffer, &count, overlapped)
			return count, err
		})
		if err != nil {
			return "", err
		}
		if read == 0 {
			return "", errors.New("pipe returned no data")
		}
		for _, value := range buffer[:read] {
			if value == '\n' {
				return string(content), nil
			}
			content = append(content, value)
			if len(content) == limit {
				return "", errors.New("pipe request too large")
			}
		}
	}
	return "", errors.New("pipe request too large")
}

func writePipeLine(handle windows.Handle, content string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	data := append([]byte(content), '\n')
	for len(data) > 0 {
		written, err := pipeIO(ctx, handle, func(overlapped *windows.Overlapped) (uint32, error) {
			var count uint32
			err := windows.WriteFile(handle, data, &count, overlapped)
			return count, err
		})
		if err != nil {
			return err
		}
		if written == 0 {
			return fmt.Errorf("pipe wrote no data")
		}
		data = data[written:]
	}
	return nil
}

func callPipe(action string) (pipeInfo, error) {
	response, err := callPipeRaw(action)
	if err != nil {
		return pipeInfo{}, err
	}
	var info pipeInfo
	if err := json.Unmarshal(response, &info); err != nil {
		return pipeInfo{}, errors.New("invalid Companion response")
	}
	return info, nil
}

// An operation error is a valid reply from a reachable service, not a connection failure.
type pipeOperationError struct{ message string }

func (e *pipeOperationError) Error() string { return e.message }

func callPipeRaw(action string) ([]byte, error) {
	request, err := json.Marshal(struct {
		Action string `json:"action"`
	}{action})
	if err != nil {
		return nil, err
	}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		handle, openErr := openPipe()
		if openErr != nil {
			lastErr = openErr
			time.Sleep(500 * time.Millisecond)
			continue
		}
		responseErr := writePipeLine(handle, string(request))
		if responseErr == nil {
			response, responseReadErr := readPipeLine(handle, 64*1024)
			if responseReadErr == nil {
				_ = writePipeLine(handle, "ack")
				var errorResponse struct {
					Error string `json:"error"`
				}
				if json.Unmarshal([]byte(response), &errorResponse) == nil && errorResponse.Error != "" {
					_ = windows.CloseHandle(handle)
					return nil, &pipeOperationError{message: errorResponse.Error}
				} else if json.Valid([]byte(response)) {
					_ = windows.CloseHandle(handle)
					return []byte(response), nil
				} else {
					responseErr = errors.New("invalid Companion response")
				}
			} else {
				responseErr = responseReadErr
			}
		}
		_ = windows.CloseHandle(handle)
		lastErr = responseErr
		time.Sleep(500 * time.Millisecond)
	}
	if lastErr == nil {
		lastErr = errors.New("pipe unavailable")
	}
	return nil, fmt.Errorf("Companion service unavailable: %w", lastErr)
}

func openPipe() (windows.Handle, error) {
	name, err := windows.UTF16PtrFromString(pipeName)
	if err != nil {
		return windows.InvalidHandle, err
	}
	return windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OVERLAPPED, 0)
}
