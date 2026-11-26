//go:build linux || windows

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const githubRepository = "jonatancheca/jona-homelab"
const checksumName = archiveName + ".sha256"

var releaseTagPattern = regexp.MustCompile(`^main-[0-9a-f]{12}$`)
var errUpdateChecking = errors.New("An update check is already in progress. Refresh status.")

type updateCheckResult struct {
	Scheduled     bool   `json:"scheduled"`
	LocalBuild    bool   `json:"localBuild,omitempty"`
	TargetVersion string `json:"targetVersion,omitempty"`
}

type updateCoordinator struct {
	config  *configStore
	mu      sync.Mutex
	client  *http.Client
	version func() string
	launch  func([]string) error
}

func newUpdateCoordinator(config *configStore) *updateCoordinator {
	return &updateCoordinator{config: config, client: &http.Client{Timeout: 30 * time.Second}, version: releaseVersion, launch: launchUpdater}
}

func (u *updateCoordinator) checkAutomatically(ctx context.Context) {
	state := readUpdateStatus()
	// Do not immediately retry a bad release after rollback restarts the service.
	if state.Phase == "failed" || state.Phase == "rolled-back" {
		checked, err := time.Parse(time.RFC3339Nano, state.UpdatedAt)
		if err == nil && time.Since(checked) < 24*time.Hour {
			return
		}
	}
	_, _ = u.checkAndSchedule(ctx)
}

func (u *updateCoordinator) checkAndSchedule(ctx context.Context) (result updateCheckResult, resultErr error) {
	if !u.mu.TryLock() {
		return updateCheckResult{}, errUpdateChecking
	}
	defer u.mu.Unlock()
	defer func() {
		fields := map[string]any{"scheduled": result.Scheduled, "localBuild": result.LocalBuild}
		if resultErr != nil {
			fields["error"] = resultErr.Error()
		}
		logEvent("update.check", fields)
	}()
	if !releaseTagPattern.MatchString(u.version()) && !allowFirstPublishedUpdate(u.version()) {
		return updateCheckResult{LocalBuild: true}, nil
	}
	if state := readUpdateStatus(); state.active() {
		if state.Phase == "checking" {
			return updateCheckResult{}, errUpdateChecking
		}
		return updateCheckResult{Scheduled: true, TargetVersion: state.TargetVersion}, nil
	}
	if err := writeUpdateStatus(updateStatus{Phase: "checking"}); err != nil {
		return result, err
	}
	defer func() {
		if resultErr != nil {
			_ = writeUpdateStatus(updateStatus{Phase: "failed", Error: resultErr.Error()})
		}
	}()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/"+githubRepository+"/releases/latest", nil)
	if err != nil {
		return result, err
	}
	request.Header.Set("User-Agent", "JonaHomelabCompanion/Go")
	response, err := u.client.Do(request)
	if err != nil {
		return result, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return result, fmt.Errorf("release check returned HTTP %d", response.StatusCode)
	}
	var release struct {
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
		TagName    string `json:"tag_name"`
		Assets     []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if json.NewDecoder(io.LimitReader(response.Body, 1024*1024)).Decode(&release) != nil || release.Draft || release.Prerelease || !releaseTagPattern.MatchString(release.TagName) {
		return result, errors.New("invalid release metadata")
	}
	if release.TagName == u.version() {
		_ = writeUpdateStatus(updateStatus{Phase: "idle"})
		return result, nil
	}
	assets := make(map[string]string, len(release.Assets))
	for _, asset := range release.Assets {
		assets[asset.Name] = asset.URL
	}
	archiveURL, archiveOK := assets[archiveName]
	checksumURL, checksumOK := assets[checksumName]
	if !archiveOK || !checksumOK || !validGithubDownload(archiveURL) || !validGithubDownload(checksumURL) {
		return result, errors.New("release assets missing or invalid")
	}
	prefix := "https://github.com/" + githubRepository + "/releases/download/" + release.TagName + "/"
	if archiveURL != prefix+archiveName || checksumURL != prefix+checksumName {
		return result, errors.New("assets do not belong to the selected release")
	}
	if err := writeUpdateStatus(updateStatus{Phase: "scheduled", TargetVersion: release.TagName}); err != nil {
		return result, err
	}
	if err := u.launch([]string{release.TagName, archiveURL, checksumURL, fmt.Sprint(os.Getpid())}); err != nil {
		return result, err
	}
	return updateCheckResult{Scheduled: true, TargetVersion: release.TagName}, nil
}

type updateStatus struct {
	Phase         string `json:"phase"`
	TargetVersion string `json:"targetVersion,omitempty"`
	Error         string `json:"error,omitempty"`
	UpdatedAt     string `json:"updatedAt,omitempty"`
	PID           uint32 `json:"pid,omitempty"`
}

func (s updateStatus) active() bool {
	switch s.Phase {
	case "checking", "scheduled", "downloading", "verifying", "installing", "restarting":
		return true
	}
	return false
}

func updateStatusPath() string { return filepath.Join(dataDirectory(), "update-status.json") }

func validGithubDownload(value string) bool {
	parsed, err := http.NewRequest(http.MethodGet, value, nil)
	return err == nil && parsed.URL.Scheme == "https" && parsed.URL.Host == "github.com" && parsed.URL.User == nil && strings.HasPrefix(parsed.URL.Path, "/"+githubRepository+"/releases/download/")
}

func download(url, destination string) error {
	client := &http.Client{Timeout: 2 * time.Minute}
	request, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", "JonaHomelabCompanionUpdater/Go")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned %s", response.Status)
	}
	file, err := os.Create(destination)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(file, io.LimitReader(response.Body, 512*1024*1024))
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func expectedChecksum(path string) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(content))
	if len(fields) == 0 || len(fields[0]) != sha256.Size*2 {
		return "", errors.New("invalid checksum")
	}
	if _, err := hex.DecodeString(fields[0]); err != nil {
		return "", errors.New("invalid checksum")
	}
	return strings.ToLower(fields[0]), nil
}

func fileChecksum(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return ""
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func healthy(expected string) bool {
	client := &http.Client{Timeout: 2 * time.Second}
	for attempt := 0; attempt < 30; attempt++ {
		response, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/health", companionPort))
		if err == nil {
			var body struct {
				Status    string `json:"status"`
				Version   string `json:"version"`
				Simulated bool   `json:"simulated"`
			}
			_ = json.NewDecoder(response.Body).Decode(&body)
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK && body.Status == "ok" && body.Version == expected && !body.Simulated {
				return true
			}
		}
		time.Sleep(2 * time.Second)
	}
	return false
}

func runUpdateLoop(ctx context.Context, updates *updateCoordinator) {
	// Local repair packages must not silently replace themselves with an older release.
	if !releaseTagPattern.MatchString(releaseVersion()) {
		logEvent("update.disabled", map[string]any{"reason": "local build"})
		return
	}
	select {
	case <-time.After(5 * time.Second):
		updates.checkAutomatically(ctx)
	case <-ctx.Done():
		return
	}
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			updates.checkAutomatically(ctx)
		case <-ctx.Done():
			return
		}
	}
}
