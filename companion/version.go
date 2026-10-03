package main

import (
	_ "embed"
	"strings"
)

//go:embed VERSION
var embeddedVersion string

func companionVersion() string { return strings.TrimSpace(embeddedVersion) }
