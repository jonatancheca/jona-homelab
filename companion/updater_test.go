//go:build windows

package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestUpdaterValidation(t *testing.T) {
	if !validGithubDownload("https://github.com/jonatancheca/jona-homelab/releases/download/main-0123456789ab/file.zip") {
		t.Fatal("valid GitHub download rejected")
	}
	for _, value := range []string{"http://github.com/file", "https://example.com/file", "https://github.com.evil.test/file"} {
		if validGithubDownload(value) {
			t.Fatalf("unsafe download accepted: %s", value)
		}
	}
	if !releaseTagPattern.MatchString("main-0123456789ab") || releaseTagPattern.MatchString("main-0123456789abc") {
		t.Fatal("release tag validation failed")
	}
}

func TestUpdaterRejectsUnsafeArchivePaths(t *testing.T) {
	temporary := t.TempDir()
	archivePath := filepath.Join(temporary, "unsafe.zip")
	archive, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(archive)
	entry, err := writer.Create("../escape.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = entry.Write([]byte("no"))
	_ = writer.Close()
	_ = archive.Close()
	if err := extractArchive(archivePath, filepath.Join(temporary, "out")); err == nil {
		t.Fatal("unsafe archive extracted")
	}
}

func TestChecksumFixture(t *testing.T) {
	temporary := t.TempDir()
	archive := filepath.Join(temporary, "file.zip")
	if err := os.WriteFile(archive, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte("fixture"))
	checksum := filepath.Join(temporary, "file.zip.sha256")
	if err := os.WriteFile(checksum, []byte(hex.EncodeToString(hash[:])+"  file.zip\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	expected, err := expectedChecksum(checksum)
	if err != nil || expected != fileChecksum(archive) {
		t.Fatalf("checksum mismatch: %v", err)
	}
}
