//go:build windows

package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unsafe"
)

func TestPowerEndpointAuthenticatesAndSignsUnsupportedState(t *testing.T) {
	store, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	secret, _ := store.secret()
	state := newRuntimeState(store)
	state.shutdown.prepare = func(powerAction) (func() error, error) { return nil, errPowerUnsupported }
	body := `{"action":"hibernate","force":false}`
	request := httptest.NewRequest(http.MethodPost, "/v1/power", strings.NewReader(body))
	request.RemoteAddr = "192.168.1.20:40000"
	denied := httptest.NewRecorder()
	state.handler().ServeHTTP(denied, request)
	if denied.Code != http.StatusUnauthorized {
		t.Fatalf("unsigned power accepted: %d", denied.Code)
	}
	nonce := "abcdefghijklmnopqrstuvwx"
	now := time.Now().Unix()
	request = httptest.NewRequest(http.MethodPost, "/v1/power", strings.NewReader(body))
	request.RemoteAddr = "192.168.1.20:40000"
	request.Header.Set(timestampHeader, formatInt(now))
	request.Header.Set(nonceHeader, nonce)
	request.Header.Set(requestSignatureHeader, signRequest(secret, http.MethodPost, "/v1/power", now, nonce, body))
	response := httptest.NewRecorder()
	state.handler().ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("unsupported response: %d", response.Code)
	}
	if !verifyResponse(secret, response.Code, nonce, response.Body.String(), response.Header().Get(responseSignatureHeader)) {
		t.Fatal("unsigned failure")
	}
}

func TestNativeCapabilitiesLayoutAndRead(t *testing.T) {
	var value systemPowerCapabilities
	if unsafe.Sizeof(value) != 76 || unsafe.Offsetof(value.AoAc) != 20 || unsafe.Offsetof(value.HiberFileType) != 22 {
		t.Fatal("Windows ABI mismatch")
	}
	if _, err := readPowerCapabilities(); err != nil {
		t.Fatal(err)
	}
	if !(systemPowerCapabilities{AoAc: 1}).allows(powerSleep) {
		t.Fatal("Modern Standby excluded")
	}
	if (systemPowerCapabilities{SystemS4: 1, HiberFilePresent: 1, HiberFileType: 1}).allows(powerHibernate) {
		t.Fatal("reduced hiberfile must not allow hibernation")
	}
	if !(systemPowerCapabilities{SystemS4: 1, HiberFilePresent: 1, HiberFileType: 2}).allows(powerHibernate) {
		t.Fatal("full hibernation rejected")
	}
}

func TestSuspendIsAsynchronousAndSharesShutdownCooldown(t *testing.T) {
	for _, action := range []powerAction{powerSleep, powerHibernate} {
		called := make(chan powerAction, 1)
		release := make(chan struct{})
		executor := newShutdownExecutor(func(bool) error { t.Error("unexpected shutdown"); return nil })
		executor.prepare = func(actual powerAction) (func() error, error) {
			return func() error { called <- actual; <-release; return nil }, nil
		}
		accepted, err := executor.trySuspend(action)
		if !accepted || err != nil {
			t.Fatalf("schedule: %v", err)
		}
		if accepted, _ := executor.trySchedule(false); accepted {
			t.Fatal("shutdown bypassed cooldown")
		}
		if accepted, _ := executor.trySuspend(action); accepted {
			t.Fatal("duplicate suspend scheduled")
		}
		select {
		case actual := <-called:
			if actual != action {
				t.Fatal("wrong action")
			}
		case <-time.After(2 * time.Second):
			t.Fatal("action not called")
		}
		close(release)
	}
}

func TestUnsupportedPowerStateFailsBeforeScheduling(t *testing.T) {
	executor := newShutdownExecutor(nil)
	executor.prepare = func(powerAction) (func() error, error) { return nil, errPowerUnsupported }
	if accepted, err := executor.trySuspend(powerHibernate); accepted || !errors.Is(err, errPowerUnsupported) {
		t.Fatalf("unsupported action accepted: %v", err)
	}
}

func TestPowerBodyRejectsUnknownActionsAndForcedSuspend(t *testing.T) {
	for _, body := range []string{`{"action":"sleep","force":false}`, `{"action":"hibernate","force":false}`, `{"action":"shutdown","force":true}`} {
		if _, _, valid := readPower(body); !valid {
			t.Fatalf("valid body rejected: %s", body)
		}
	}
	for _, body := range []string{`{"action":"sleep","force":true}`, `{"action":"hibernate","force":true}`, `{"action":"reboot","force":false}`, `{"action":"sleep","force":null}`, `{"action":"sleep"}`, `{"action":"sleep","force":false,"command":"x"}`, `{"action":"sleep","force":false} {}`} {
		if _, _, valid := readPower(body); valid {
			t.Fatalf("invalid body accepted: %s", body)
		}
	}
}
