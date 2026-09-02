package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	protocolVersion         = "1"
	pairingPrefix           = "jhcp1_"
	requestSignatureHeader  = "X-Jona-Signature"
	timestampHeader         = "X-Jona-Timestamp"
	nonceHeader             = "X-Jona-Nonce"
	responseSignatureHeader = "X-Jona-Response-Signature"
)

var errInvalidPairingCode = errors.New("invalid Companion pairing code")

func pairingCode(secret []byte) string {
	return pairingPrefix + base64.RawURLEncoding.EncodeToString(secret)
}

func parsePairingCode(code string) ([]byte, error) {
	if !strings.HasPrefix(code, pairingPrefix) || len(code) != len(pairingPrefix)+43 {
		return nil, errInvalidPairingCode
	}
	secret, err := base64.RawURLEncoding.DecodeString(code[len(pairingPrefix):])
	if err != nil || len(secret) != 32 {
		return nil, errInvalidPairingCode
	}
	return secret, nil
}

func signRequest(secret []byte, method, path string, timestamp int64, nonce, body string) string {
	canonical := strings.ToUpper(method) + "\n" + path + "\n" + formatInt(timestamp) + "\n" + nonce + "\n" + bodyHash(body)
	return sign(secret, canonical)
}

func signResponse(secret []byte, status int, nonce, body string) string {
	return sign(secret, formatInt(int64(status))+"\n"+nonce+"\n"+bodyHash(body))
}

func verifyRequest(secret []byte, method, path, body string, headers http.Header, now time.Time) (string, bool) {
	nonce := headers.Get(nonceHeader)
	timestampText := headers.Get(timestampHeader)
	signature := headers.Get(requestSignatureHeader)
	if !validNonce(nonce) || !validSignature(signature) {
		return nonce, false
	}
	timestamp, err := parseInt(timestampText)
	if err != nil {
		return nonce, false
	}
	current := now.Unix()
	if timestamp < current-60 || timestamp > current+60 {
		return nonce, false
	}
	expected := signRequest(secret, method, path, timestamp, nonce, body)
	return nonce, hmac.Equal([]byte(strings.ToLower(signature)), []byte(expected))
}

func verifyResponse(secret []byte, status int, nonce, body, signature string) bool {
	if !validSignature(signature) {
		return false
	}
	return hmac.Equal([]byte(strings.ToLower(signature)), []byte(signResponse(secret, status, nonce, body)))
}

func sign(secret []byte, canonical string) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(canonical))
	return hex.EncodeToString(mac.Sum(nil))
}

func bodyHash(body string) string {
	hash := sha256.Sum256([]byte(body))
	return hex.EncodeToString(hash[:])
}

func validNonce(nonce string) bool {
	if len(nonce) < 20 || len(nonce) > 24 {
		return false
	}
	for _, char := range nonce {
		if (char < 'A' || char > 'Z') && (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' && char != '_' {
			return false
		}
	}
	return true
}

func validSignature(signature string) bool {
	if len(signature) != 64 {
		return false
	}
	_, err := hex.DecodeString(signature)
	return err == nil
}

func formatInt(value int64) string {
	if value == 0 {
		return "0"
	}
	negative := value < 0
	if negative {
		value = -value
	}
	var digits [20]byte
	index := len(digits)
	for value > 0 {
		index--
		digits[index] = byte('0' + value%10)
		value /= 10
	}
	if negative {
		index--
		digits[index] = '-'
	}
	return string(digits[index:])
}

func parseInt(value string) (int64, error) {
	return strconv.ParseInt(value, 10, 64)
}
