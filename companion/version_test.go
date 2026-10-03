package main

import (
	"regexp"
	"testing"
)

func TestCompanionVersionFormat(t *testing.T) {
	if !regexp.MustCompile(`^1\.(0[0-9]|[1-9][0-9]+)$`).MatchString(companionVersion()) {
		t.Fatalf("VERSION must contain 1.00, 1.01, ..., 1.100: %q", companionVersion())
	}
}
