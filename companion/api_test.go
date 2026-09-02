//go:build windows

package main

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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
		{`{"force":true} {}`, false},
	} {
		if force, valid := readForce(test.body); valid != test.valid || (valid && force != (test.body == `{"force":true}`)) {
			t.Fatalf("readForce(%q) = %v, %v", test.body, force, valid)
		}
	}
}
