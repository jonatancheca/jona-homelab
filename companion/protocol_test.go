//go:build windows

package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestProtocolVectorMatchesNode(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("..", "tests", "fixtures", "companion-protocol.json"))
	if err != nil {
		t.Fatal(err)
	}
	var vector struct {
		Secret            string `json:"secret"`
		Method            string `json:"method"`
		Path              string `json:"path"`
		Timestamp         int64  `json:"timestamp"`
		Nonce             string `json:"nonce"`
		Body              string `json:"body"`
		RequestSignature  string `json:"requestSignature"`
		ResponseStatus    int    `json:"responseStatus"`
		ResponseBody      string `json:"responseBody"`
		ResponseSignature string `json:"responseSignature"`
	}
	if err := json.Unmarshal(content, &vector); err != nil {
		t.Fatal(err)
	}
	secret, err := parseBase64URL(vector.Secret)
	if err != nil {
		t.Fatal(err)
	}
	if got := signRequest(secret, vector.Method, vector.Path, vector.Timestamp, vector.Nonce, vector.Body); got != vector.RequestSignature {
		t.Fatalf("request signature: got %s, want %s", got, vector.RequestSignature)
	}
	if got := signResponse(secret, vector.ResponseStatus, vector.Nonce, vector.ResponseBody); got != vector.ResponseSignature {
		t.Fatalf("response signature: got %s, want %s", got, vector.ResponseSignature)
	}
}

func TestProtocolRejectsTamperingClockAndReplayShape(t *testing.T) {
	secret := make([]byte, 32)
	now := time.Unix(1_777_777_777, 0)
	nonce := "abcdefghijklmnopqrstuv"
	body := `{"force":true}`
	headers := http.Header{
		timestampHeader:        []string{"1777777777"},
		nonceHeader:            []string{nonce},
		requestSignatureHeader: []string{signRequest(secret, "POST", "/v1/shutdown", now.Unix(), nonce, body)},
	}
	parsed, valid := verifyRequest(secret, "POST", "/v1/shutdown", body, headers, now)
	if !valid || parsed != nonce {
		t.Fatal("valid request rejected")
	}
	if _, valid := verifyRequest(secret, "POST", "/v1/shutdown", `{"force":false}`, headers, now); valid {
		t.Fatal("body tampering accepted")
	}
	if _, valid := verifyRequest(secret, "POST", "/v1/shutdown", body, headers, now.Add(61*time.Second)); valid {
		t.Fatal("expired request accepted")
	}
	if _, valid := verifyRequest(secret, "POST", "/v1/shutdown", body, http.Header{timestampHeader: []string{"1777777777"}, nonceHeader: []string{"short"}, requestSignatureHeader: []string{headers.Get(requestSignatureHeader)}}, now); valid {
		t.Fatal("short nonce accepted")
	}
}

func TestPairingCodeRoundTrip(t *testing.T) {
	secret := make([]byte, 32)
	code := pairingCode(secret)
	if len(code) != 49 {
		t.Fatalf("pairing code length: %d", len(code))
	}
	parsed, err := parsePairingCode(code)
	if err != nil || string(parsed) != string(secret) {
		t.Fatalf("pairing code did not round-trip: %v", err)
	}
	if _, err := parsePairingCode(code[:len(code)-1] + "!"); err == nil {
		t.Fatal("invalid pairing code accepted")
	}
}

func parseBase64URL(value string) ([]byte, error) {
	return parsePairingCode(pairingPrefix + value)
}
