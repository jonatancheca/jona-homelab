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
	Ready          bool   `json:"ready"`
	Version        string `json:"version"`
	Port           int    `json:"port"`
	PairingCode    string `json:"pairingCode"`
	LastServerCall string `json:"lastServerCall,omitempty"`
}

func runPipeServer(ctx context.Context, state *runtimeState) {
	for ctx.Err() == nil {
		handle, err := createPipe()
		if err != nil {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		connected := connectPipe(ctx, handle)
		if connected {
			request, readErr := readPipeLine(handle, 64*1024)
			if readErr == nil {
				response := handlePipeRequest(ctx, state, request)
				_ = writePipeLine(handle, response)
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
		windows.PIPE_ACCESS_DUPLEX,
		windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT,
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
	result := make(chan error, 1)
	go func() {
		err := windows.ConnectNamedPipe(handle, nil)
		if errors.Is(err, windows.ERROR_PIPE_CONNECTED) {
			err = nil
		}
		result <- err
	}()
	select {
	case err := <-result:
		return err == nil
	case <-ctx.Done():
		_ = windows.CloseHandle(handle)
		return false
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
		return marshalLocal(pipeInfo{Ready: true, Version: releaseVersion(), Port: companionPort, PairingCode: code, LastServerCall: state.config.lastServerCall()})
	case "rotate":
		code, err := state.config.rotateSecret()
		if err != nil {
			return localError(err)
		}
		return marshalLocal(pipeInfo{Ready: true, Version: releaseVersion(), Port: companionPort, PairingCode: code, LastServerCall: state.config.lastServerCall()})
	case "check-update":
		scheduled := state.updates.checkAndSchedule(ctx)
		return marshalLocal(struct {
			Scheduled bool `json:"scheduled"`
		}{scheduled})
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
	content := make([]byte, 0, 1024)
	buffer := make([]byte, 1024)
	for len(content) < limit {
		var read uint32
		if err := windows.ReadFile(handle, buffer, &read, nil); err != nil {
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
	data := append([]byte(content), '\n')
	for len(data) > 0 {
		var written uint32
		if err := windows.WriteFile(handle, data, &written, nil); err != nil {
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
				var errorResponse struct {
					Error string `json:"error"`
				}
				if json.Unmarshal([]byte(response), &errorResponse) == nil && errorResponse.Error != "" {
					responseErr = errors.New(errorResponse.Error)
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
	return windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, 0, 0)
}
