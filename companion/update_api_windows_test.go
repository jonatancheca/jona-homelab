//go:build windows

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestUpdateEndpointAuthenticatesAndSharesCoordinator(t *testing.T) {
	resetUpdateState(t)
	config, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	secret, _ := config.secret()
	state := newRuntimeState(config)
	state.updates.version = func() string { return "main-000000000000" }
	state.updates.client.Transport = updateTransport(func(*http.Request) (*http.Response, error) { return releaseResponse("main-111111111111"), nil })
	launches := 0
	state.updates.launch = func([]string) error { launches++; return nil }
	request := func(body, nonce string, signed bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/v1/update", strings.NewReader(body))
		r.RemoteAddr = "192.168.1.2:40000"
		now := time.Now().Unix()
		if signed {
			r.Header.Set(timestampHeader, formatInt(now))
			r.Header.Set(nonceHeader, nonce)
			r.Header.Set(requestSignatureHeader, signRequest(secret, http.MethodPost, "/v1/update", now, nonce, body))
		}
		w := httptest.NewRecorder()
		state.handler().ServeHTTP(w, r)
		if signed && w.Code != http.StatusConflict && !verifyResponse(secret, w.Code, nonce, w.Body.String(), w.Header().Get(responseSignatureHeader)) {
			t.Fatal("unsigned response")
		}
		return w
	}
	if w := request("{}", "", false); w.Code != http.StatusUnauthorized {
		t.Fatalf("unsigned update accepted: %d", w.Code)
	}
	if w := request(`{"url":"https://example.com"}`, "invalidbody1234567890", true); w.Code != http.StatusBadRequest {
		t.Fatalf("arbitrary update accepted: %d", w.Code)
	}
	for _, nonce := range []string{"abcdefghijklmnopqrstuv", "abcdefghijklmnopqrstuw"} {
		w := request("{}", nonce, true)
		var result updateCheckResult
		if w.Code != http.StatusAccepted || json.Unmarshal(w.Body.Bytes(), &result) != nil || !result.Scheduled {
			t.Fatalf("update: %d %s", w.Code, w.Body.String())
		}
	}
	if launches != 1 {
		t.Fatalf("launched %d workers", launches)
	}
	if w := request("{}", "abcdefghijklmnopqrstuv", true); w.Code != http.StatusConflict {
		t.Fatal("replayed update accepted")
	}
	state.simulated = true
	if w := request("{}", "simulation12345678901", true); w.Code != http.StatusConflict {
		t.Fatal("simulation launched update")
	}
}
