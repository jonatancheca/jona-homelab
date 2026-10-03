//go:build windows

package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStatusRequiresPrivateSignedRequestAndRejectsReplay(t *testing.T) {
	secret := []byte("01234567890123456789012345678901")
	encrypted, err := protectData(secret)
	if err != nil {
		t.Fatal(err)
	}
	store := &configStore{
		path: filepath.Join(t.TempDir(), "config.json"),
		cfg:  companionConfig{EncryptedSecret: base64.StdEncoding.EncodeToString(encrypted), Port: companionPort},
	}
	state := newRuntimeState(store)
	now := time.Now().Unix()
	nonce := "abcdefghijklmnopqrstuv"
	request := httptest.NewRequest(http.MethodGet, "http://192.168.1.20/v1/status", nil)
	request.RemoteAddr = "192.168.1.20:47600"
	request.Header.Set(timestampHeader, formatInt(now))
	request.Header.Set(nonceHeader, nonce)
	request.Header.Set(requestSignatureHeader, signRequest(secret, http.MethodGet, "/v1/status", now, nonce, ""))
	response := httptest.NewRecorder()
	state.handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status response: %d", response.Code)
	}
	if !verifyResponse(secret, response.Code, nonce, response.Body.String(), response.Header().Get(responseSignatureHeader)) {
		t.Fatal("response signature rejected")
	}
	var versions struct {
		Version        string `json:"version"`
		DisplayVersion string `json:"displayVersion"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &versions); err != nil || versions.Version != releaseVersion() || versions.DisplayVersion != companionVersion() {
		t.Fatalf("signed status lost release or display version: %+v, %v", versions, err)
	}
	var info pipeInfo
	if err := json.Unmarshal([]byte(handlePipeRequest(request.Context(), state, `{"action":"get-info"}`)), &info); err != nil || info.DisplayVersion != companionVersion() || info.Version != versions.Version {
		t.Fatalf("local tray and signed API versions differ: %+v, %v", info, err)
	}
	replay := httptest.NewRecorder()
	state.handler().ServeHTTP(replay, request)
	if replay.Code != http.StatusConflict {
		t.Fatalf("replayed request response: %d", replay.Code)
	}

	privateRequest := httptest.NewRequest(http.MethodGet, "http://8.8.8.8/v1/status", nil)
	privateRequest.RemoteAddr = "8.8.8.8:47600"
	denied := httptest.NewRecorder()
	state.handler().ServeHTTP(denied, privateRequest)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("public client response: %d", denied.Code)
	}
}

func TestShutdownBodyRequiresOnlyBooleanForce(t *testing.T) {
	for _, test := range []struct {
		body  string
		valid bool
	}{
		{`{"force":true}`, true},
		{`{"force":false}`, true},
		{`{"force":true,"extra":false}`, false},
		{`{"force":1}`, false},
		{`{"force":null}`, false},
		{`{"force":true} {}`, false},
	} {
		if force, valid := readForce(test.body); valid != test.valid || (valid && force != (test.body == `{"force":true}`)) {
			t.Fatalf("readForce(%q) = %v, %v", test.body, force, valid)
		}
	}
}

func TestShutdownHTTPFailureAndCooldownAreSigned(t *testing.T) {
	for _, fails := range []bool{false, true} {
		store, err := loadConfig()
		if err != nil {
			t.Fatal(err)
		}
		secret, _ := store.secret()
		state := newRuntimeState(store)
		calls := 0
		state.shutdown = newShutdownExecutor(func(force bool) error {
			calls++
			if !force {
				t.Error("force flag lost")
			}
			if fails {
				return errors.New("Windows rejected shutdown")
			}
			return nil
		})
		server := httptest.NewServer(state.handler())
		for attempt := 0; attempt < 2; attempt++ {
			body := `{"force":true}`
			nonce := "abcdefghijklmnopqrstuv" + formatInt(int64(attempt))
			request, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/shutdown", strings.NewReader(body))
			now := time.Now().Unix()
			request.Header.Set(timestampHeader, formatInt(now))
			request.Header.Set(nonceHeader, nonce)
			request.Header.Set(requestSignatureHeader, signRequest(secret, http.MethodPost, "/v1/shutdown", now, nonce, body))
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			content := new(strings.Builder)
			_, _ = io.Copy(content, response.Body)
			response.Body.Close()
			expected := http.StatusAccepted
			if fails {
				expected = http.StatusInternalServerError
			}
			if attempt == 1 {
				expected = http.StatusTooManyRequests
			}
			if response.StatusCode != expected {
				t.Fatalf("got %d want %d", response.StatusCode, expected)
			}
			if !verifyResponse(secret, response.StatusCode, nonce, content.String(), response.Header.Get(responseSignatureHeader)) {
				t.Fatal("invalid response signature")
			}
		}
		server.Close()
		if calls != 1 {
			t.Fatalf("executed %d shutdowns", calls)
		}
	}
}
