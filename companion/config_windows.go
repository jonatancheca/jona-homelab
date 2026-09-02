//go:build windows

package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	companionPort        = 47654
	serviceName          = "JonaHomelabCompanion"
	displayName          = "Jona Homelab Companion"
	pipeName             = `\\.\pipe\JonaHomelabCompanion`
	dataDirectoryName    = "JonaHomelabCompanion"
	configFileName       = "config.json"
	dpapiEntropy         = "jona-homelab-companion"
	dpapiScopeMachine    = "machine"
	configFilePermission = 0o600
)

type companionConfig struct {
	EncryptedSecret string `json:"encryptedSecret"`
	Port            int    `json:"port"`
	LastServerCall  string `json:"lastServerCall,omitempty"`
	DPAPIScope      string `json:"dpapiScope,omitempty"`
}

type configStore struct {
	mu   sync.RWMutex
	path string
	cfg  companionConfig
}

func dataDirectory() string {
	root := os.Getenv("ProgramData")
	if root == "" {
		root = `C:\ProgramData`
	}
	return filepath.Join(root, dataDirectoryName)
}

func loadConfig() (*configStore, error) {
	path := filepath.Join(dataDirectory(), configFileName)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create Companion data directory: %w", err)
	}
	store := &configStore{path: path}
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		secret := make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			return nil, fmt.Errorf("generate Companion secret: %w", err)
		}
		encrypted, err := protectData(secret)
		if err != nil {
			return nil, err
		}
		store.cfg = companionConfig{
			EncryptedSecret: base64.StdEncoding.EncodeToString(encrypted),
			Port:            companionPort,
			DPAPIScope:      dpapiScopeMachine,
		}
		if err := store.saveLocked(); err != nil {
			return nil, err
		}
		return store, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read Companion configuration: %w", err)
	}
	if err := json.Unmarshal(content, &store.cfg); err != nil {
		return nil, fmt.Errorf("parse Companion configuration: %w", err)
	}
	if store.cfg.Port != companionPort || store.cfg.EncryptedSecret == "" {
		return nil, errors.New("invalid Companion configuration")
	}
	if _, err := store.secretLocked(); err != nil {
		return nil, err
	}
	if store.cfg.DPAPIScope != dpapiScopeMachine {
		// Older Companion builds used user-scoped DPAPI. Re-protect the existing
		// secret once so a LocalSystem service and the interactive tray share it.
		secret, err := store.secretLocked()
		if err != nil {
			return nil, err
		}
		encrypted, err := protectData(secret)
		if err != nil {
			return nil, err
		}
		store.cfg.EncryptedSecret = base64.StdEncoding.EncodeToString(encrypted)
		store.cfg.DPAPIScope = dpapiScopeMachine
		if err := store.saveLocked(); err != nil {
			return nil, err
		}
	}
	return store, nil
}

func (s *configStore) secret() ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.secretLocked()
}

func (s *configStore) secretLocked() ([]byte, error) {
	encrypted, err := base64.StdEncoding.DecodeString(s.cfg.EncryptedSecret)
	if err != nil {
		return nil, errors.New("Companion secret cannot be decrypted")
	}
	secret, err := unprotectData(encrypted)
	if err != nil || len(secret) != 32 {
		return nil, errors.New("Companion secret cannot be decrypted")
	}
	return secret, nil
}

func (s *configStore) pairingCode() (string, error) {
	secret, err := s.secret()
	if err != nil {
		return "", err
	}
	return pairingCode(secret), nil
}

func (s *configStore) rotateSecret() (string, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return "", fmt.Errorf("generate Companion secret: %w", err)
	}
	encrypted, err := protectData(secret)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	s.cfg.EncryptedSecret = base64.StdEncoding.EncodeToString(encrypted)
	err = s.saveLocked()
	s.mu.Unlock()
	if err != nil {
		return "", err
	}
	return pairingCode(secret), nil
}

func (s *configStore) recordServerCall(now time.Time) {
	s.mu.Lock()
	s.cfg.LastServerCall = now.UTC().Format(time.RFC3339Nano)
	_ = s.saveLocked()
	s.mu.Unlock()
}

func (s *configStore) lastServerCall() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg.LastServerCall
}

func (s *configStore) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("create Companion data directory: %w", err)
	}
	content, err := json.MarshalIndent(s.cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("serialize Companion configuration: %w", err)
	}
	content = append(content, '\n')
	temporary := s.path + ".tmp"
	if err := os.WriteFile(temporary, content, configFilePermission); err != nil {
		return fmt.Errorf("write Companion configuration: %w", err)
	}
	name, err := windows.UTF16PtrFromString(temporary)
	if err != nil {
		return err
	}
	destination, err := windows.UTF16PtrFromString(s.path)
	if err != nil {
		return err
	}
	if err := windows.MoveFileEx(name, destination, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("activate Companion configuration: %w", err)
	}
	return nil
}

func releaseVersion() string {
	executable, err := os.Executable()
	if err != nil {
		return "dev"
	}
	content, err := os.ReadFile(filepath.Join(filepath.Dir(executable), "RELEASE_VERSION"))
	if err != nil {
		return "dev"
	}
	version := string(content)
	for len(version) > 0 && (version[len(version)-1] == '\r' || version[len(version)-1] == '\n' || version[len(version)-1] == ' ' || version[len(version)-1] == '\t') {
		version = version[:len(version)-1]
	}
	if version == "" {
		return "dev"
	}
	return version
}

func protectData(value []byte) ([]byte, error) {
	return cryptData(value, true)
}

func unprotectData(value []byte) ([]byte, error) {
	return cryptData(value, false)
}

func cryptData(value []byte, protect bool) ([]byte, error) {
	if len(value) == 0 {
		return nil, errors.New("empty DPAPI value")
	}
	input := windows.DataBlob{Size: uint32(len(value)), Data: &value[0]}
	entropyBytes := []byte(dpapiEntropy)
	entropy := windows.DataBlob{Size: uint32(len(entropyBytes)), Data: &entropyBytes[0]}
	var output windows.DataBlob
	var err error
	var flags uint32
	if protect {
		// The service runs as LocalSystem. Machine scope is required so the
		// interactive tray can read the same protected secret.
		flags = windows.CRYPTPROTECT_LOCAL_MACHINE
		err = windows.CryptProtectData(&input, nil, &entropy, 0, nil, flags, &output)
	} else {
		err = windows.CryptUnprotectData(&input, nil, &entropy, 0, nil, flags, &output)
	}
	if err != nil {
		return nil, fmt.Errorf("DPAPI operation failed: %w", err)
	}
	if output.Data == nil || output.Size == 0 {
		return nil, errors.New("DPAPI returned no data")
	}
	defer windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(output.Data))))
	result := make([]byte, output.Size)
	copy(result, unsafe.Slice((*byte)(unsafe.Pointer(output.Data)), output.Size))
	return result, nil
}
