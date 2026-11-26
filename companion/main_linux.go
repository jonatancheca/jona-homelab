package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"time"
)

const companionPort = 47654
const companionPlatform = "cachyos-x64"
const archiveName = "jona-homelab-companion-cachyos-x86_64.pkg.tar.zst"

var buildRelease = "dev"

func releaseVersion() string                        { return buildRelease }
func allowFirstPublishedUpdate(version string) bool { return version == "local-cachyos" }
func dataDirectory() string                         { return "/var/lib/jona-homelab-companion" }

type configStore struct {
	path     string
	lastCall atomic.Int64
}

func (s *configStore) recordServerCall(t time.Time) { s.lastCall.Store(t.Unix()) }
func (s *configStore) lastServerCall() string {
	if stamp := s.lastCall.Load(); stamp != 0 {
		return time.Unix(stamp, 0).UTC().Format(time.RFC3339)
	}
	return ""
}

func (s *configStore) secret() ([]byte, error) {
	b, err := os.ReadFile(s.path)
	if err == nil && len(b) != 32 {
		return nil, errors.New("invalid pairing key on disk")
	}
	return b, err
}

func writePrivate(path string, value []byte, replace bool) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".companion-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(value); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if replace {
		return os.Rename(f.Name(), path)
	}
	return os.Link(f.Name(), path)
}

func (s *configStore) newSecret(rotate bool) error {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return err
	}
	err := writePrivate(s.path, key, rotate)
	if !rotate && errors.Is(err, os.ErrExist) {
		return nil
	}
	return err
}

func logEvent(event string, fields map[string]any) {
	if fields == nil {
		fields = map[string]any{}
	}
	fields["event"] = event
	_ = json.NewEncoder(os.Stderr).Encode(fields)
}

func run(args []string) error {
	if len(args) == 1 && args[0] == "version" {
		fmt.Println(companionVersion(), releaseVersion())
		return nil
	}
	if len(args) == 2 && args[0] == "apply-update" {
		return applyUpdate(args[1])
	}
	if len(args) != 1 {
		return errors.New("expected serve, pair, rotate, diagnostics or version")
	}
	store := &configStore{path: filepath.Join(dataDirectory(), "pairing.key")}
	switch args[0] {
	case "diagnostics":
		return diagnostics()
	case "pair", "rotate":
		if args[0] == "rotate" {
			if err := store.newSecret(true); err != nil {
				return err
			}
		}
		key, err := store.secret()
		if err != nil {
			return err
		}
		fmt.Println(pairingCode(key))
		return nil
	case "serve":
		if err := os.MkdirAll(dataDirectory(), 0700); err != nil {
			return err
		}
		if err := store.newSecret(false); err != nil {
			return err
		}
		if _, err := store.secret(); err != nil {
			return err
		}
		state := newRuntimeState(store)
		listener, err := net.Listen("tcp4", fmt.Sprintf(":%d", companionPort))
		if err != nil {
			return err
		}
		defer listener.Close()
		ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
		defer cancel()
		go runUpdateLoop(ctx, state.updates)
		return runHTTP(ctx, state, listener)
	default:
		return errors.New("unknown Companion command")
	}
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
