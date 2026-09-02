//go:build windows

package main

import "testing"

func TestDPAPIRoundTrip(t *testing.T) {
	plain := []byte("companion-test-secret")
	protected, err := protectData(plain)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := unprotectData(protected)
	if err != nil {
		t.Fatal(err)
	}
	if string(restored) != string(plain) {
		t.Fatalf("DPAPI round-trip mismatch: %q", restored)
	}
}
