package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Use an isolated key, synthetic private client address and injected power operations.
func TestLinuxPairingAndPower(t *testing.T) {
	store := &configStore{path: filepath.Join(t.TempDir(), "pairing.key")}
	if err := store.newSecret(false); err != nil {
		t.Fatal(err)
	}
	key, err := store.secret()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.newSecret(false); err != nil {
		t.Fatal(err)
	}
	same, err := store.secret()
	if err != nil || !bytes.Equal(key, same) {
		t.Fatal("restart changed the key")
	}
	info, err := os.Stat(store.path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("pairing file is not private")
	}
	state := newRuntimeState(store)
	didShutdown := false
	state.shutdown = newShutdownExecutor(func(force bool) error { didShutdown = force; return nil })
	state.shutdown.prepare = func(action powerAction) (func() error, error) { return nil, errPowerUnsupported }
	send := func(path, body, nonce string, secret []byte) int {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
		req.RemoteAddr = "192.168.1.2:1234"
		stamp := time.Now().Unix()
		req.Header.Set(timestampHeader, formatInt(stamp))
		req.Header.Set(nonceHeader, nonce)
		req.Header.Set(requestSignatureHeader, signRequest(secret, http.MethodPost, path, stamp, nonce, body))
		res := httptest.NewRecorder()
		state.handler().ServeHTTP(res, req)
		if res.Code != http.StatusUnauthorized && !verifyResponse(secret, res.Code, nonce, res.Body.String(), res.Header().Get(responseSignatureHeader)) {
			t.Fatal("response did not authenticate")
		}
		return res.Code
	}
	if got := send("/v1/power", "{\"action\":\"hibernate\",\"force\":false}", "abcdefghijklmnopqrstu1", key); got != 409 {
		t.Fatalf("unavailable hibernation: %d", got)
	}
	if got := send("/v1/shutdown", "{\"force\":true}", "abcdefghijklmnopqrstu2", key); got != 202 || !didShutdown {
		t.Fatalf("simulated shutdown: %d", got)
	}
	if err := store.newSecret(true); err != nil {
		t.Fatal(err)
	}
	if got := send("/v1/shutdown", "{\"force\":true}", "abcdefghijklmnopqrstu3", key); got != 401 {
		t.Fatal("old key still works after rotation")
	}
}
