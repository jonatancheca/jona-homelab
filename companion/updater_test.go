//go:build windows

package main

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"golang.org/x/sys/windows"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type failUpdateTransport struct{ t *testing.T }

func (f failUpdateTransport) RoundTrip(*http.Request) (*http.Response, error) {
	f.t.Fatal("local build must not contact GitHub or launch an update")
	return nil, nil
}

func TestLocalUpdateCheckIsInformationalAndOffline(t *testing.T) {
	updater := newUpdateCoordinator(nil)
	updater.client = &http.Client{Transport: failUpdateTransport{t}}
	result, err := updater.checkAndSchedule(context.Background())
	if err != nil || !result.LocalBuild || result.Scheduled {
		t.Fatalf("local check must be a disabled result, not an error: %+v, %v", result, err)
	}
}

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

func TestInstalledReleaseIsInsideReleasesDirectory(t *testing.T) {
	root := `C:\Program Files\JonaHomelabCompanion\releases`
	if !withinDirectory(root, root+`\main-062265854248`) {
		t.Fatal("installed release rejected: updater would exit with code 2 before downloading")
	}
	for _, path := range []string{root, root + `-other\main-062265854248`, root + `\..\outside`, `D:\releases\main-062265854248`} {
		if withinDirectory(root, path) {
			t.Fatalf("unsafe path accepted: %s", path)
		}
	}
}

func TestFinalDirectoryResolvesInstalledJunction(t *testing.T) {
	// Reproduce the real installer's junction, without touching an installed service.
	// Match the long path returned by Windows when TEMP uses an 8.3 alias.
	temporary, err := finalDirectory(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(temporary, "installation with spaces")
	target := filepath.Join(root, "releases", "main-062265854248")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	current := filepath.Join(root, "current")
	if output, err := exec.Command("cmd.exe", "/c", "mklink", "/J", current, target).CombinedOutput(); err != nil {
		t.Fatalf("junction: %v %s", err, output)
	}
	t.Cleanup(func() { _ = os.Remove(current) })
	t.Setenv("GODEBUG", "winsymlink=0")
	legacy, err := filepath.EvalSymlinks(current)
	if err != nil {
		t.Fatal(err)
	}
	if withinDirectory(filepath.Join(root, "releases"), legacy) {
		t.Log("this Go version follows junctions even in compatibility mode")
	}
	resolved, err := finalDirectory(current)
	if err != nil || !strings.EqualFold(resolved, target) || !withinDirectory(filepath.Join(root, "releases"), resolved) {
		t.Fatalf("real junction was rejected: %q %v", resolved, err)
	}
	next := filepath.Join(root, "releases", "main-111111111111")
	if err := os.MkdirAll(next, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, destination := range []string{next, target} {
		if err := replaceJunction(current, destination); err != nil {
			t.Fatal(err)
		}
		resolved, err := finalDirectory(current)
		if err != nil || !strings.EqualFold(resolved, destination) {
			t.Fatalf("junction switch failed: %q %v", resolved, err)
		}
	}
}

func TestUpdaterLockSurvivesCoordinatorReplacement(t *testing.T) {
	lock, err := acquireUpdateLock()
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(lock)
	duplicate, err := acquireUpdateLock()
	if err == nil {
		windows.CloseHandle(duplicate)
		t.Fatal("second updater acquired the installation lock")
	}
	if !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		t.Fatal(err)
	}
}

type updateTransport func(*http.Request) (*http.Response, error)

func (f updateTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func releaseResponse(version string) *http.Response {
	content, _ := json.Marshal(map[string]any{"tag_name": version, "assets": []map[string]string{
		{"name": archiveName, "browser_download_url": "https://github.com/" + githubRepository + "/releases/download/" + version + "/" + archiveName},
		{"name": checksumName, "browser_download_url": "https://github.com/" + githubRepository + "/releases/download/" + version + "/" + checksumName},
	}})
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(content)))}
}

func resetUpdateState(t *testing.T) {
	t.Helper()
	if err := writeUpdateStatus(updateStatus{Phase: "idle"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(updateStatusPath()) })
}

func TestConcurrentUpdatesLaunchOnlyOneWorker(t *testing.T) {
	resetUpdateState(t)
	u := newUpdateCoordinator(nil)
	u.version = func() string { return "main-000000000000" }
	entered, release := make(chan struct{}), make(chan struct{})
	u.client.Transport = updateTransport(func(*http.Request) (*http.Response, error) {
		close(entered)
		<-release
		return releaseResponse("main-111111111111"), nil
	})
	launches := 0
	u.launch = func(args []string) error { launches++; return nil }
	done := make(chan error, 1)
	go func() { _, err := u.checkAndSchedule(context.Background()); done <- err }()
	<-entered
	duplicate, err := u.checkAndSchedule(context.Background())
	if !errors.Is(err, errUpdateChecking) || duplicate.Scheduled {
		t.Fatalf("duplicate: %+v %v", duplicate, err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	_, err = u.checkAndSchedule(context.Background())
	if err != nil || launches != 1 {
		t.Fatalf("launched %d workers: %v", launches, err)
	}
}

func TestLaunchFailureIsPersistedAndCanBeRetried(t *testing.T) {
	resetUpdateState(t)
	u := newUpdateCoordinator(nil)
	u.version = func() string { return "main-000000000000" }
	u.client.Transport = updateTransport(func(*http.Request) (*http.Response, error) { return releaseResponse("main-111111111111"), nil })
	u.launch = func([]string) error { return errors.New("launch denied") }
	result, err := u.checkAndSchedule(context.Background())
	if err == nil || result.Scheduled || readUpdateStatus().Phase != "failed" || readUpdateStatus().Error != "launch denied" {
		t.Fatalf("failure concealed: %+v %v %+v", result, err, readUpdateStatus())
	}
	u.launch = func([]string) error { return nil }
	result, err = u.checkAndSchedule(context.Background())
	if err != nil || !result.Scheduled {
		t.Fatalf("retry failed: %+v %v", result, err)
	}
}

func TestInterruptedUpdateDoesNotRemainScheduled(t *testing.T) {
	resetUpdateState(t)
	if err := os.WriteFile(updateStatusPath(), []byte(`{"phase":"scheduled","pid":0,"updatedAt":"2026-01-01T00:00:00Z"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if state := readUpdateStatus(); state.Phase != "failed" || state.Error == "" {
		t.Fatalf("interrupted job: %+v", state)
	}
}

func TestActivationConfirmsHealthAndRestoresOldVersionOnFailure(t *testing.T) {
	for _, fails := range []bool{false, true} {
		var calls []string
		ops := activationOperations{
			stop:          func() bool { calls = append(calls, "stop"); return true },
			wait:          func() bool { calls = append(calls, "wait"); return true },
			start:         func() bool { calls = append(calls, "start"); return true },
			switchRelease: func(_, target string) error { calls = append(calls, "switch:"+target); return nil },
			healthy:       func(version string) bool { calls = append(calls, "health:"+version); return !fails || version == "old" },
			stopTray:      func() { calls = append(calls, "stop-tray") }, startTray: func() { calls = append(calls, "start-tray") },
		}
		rolledBack, err := activateUpdate("current", "old", "new", "new", ops)
		if rolledBack != fails || (err != nil) != fails {
			t.Fatalf("activation: %v %v", rolledBack, err)
		}
		want := "stop,wait,stop-tray,switch:new,start,health:new,start-tray"
		if fails {
			want = "stop,wait,stop-tray,switch:new,start,health:new,stop,switch:old,start,health:old,start-tray"
		}
		if strings.Join(calls, ",") != want {
			t.Fatalf("unsafe activation order: %v", calls)
		}
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
