//go:build windows

package main

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	githubRepository = "jonatancheca/jona-homelab"
	archiveName      = "jona-homelab-companion-win-x64.zip"
	checksumName     = archiveName + ".sha256"
)

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
	if !releaseTagPattern.MatchString(u.version()) {
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
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if json.NewDecoder(io.LimitReader(response.Body, 1024*1024)).Decode(&release) != nil || !releaseTagPattern.MatchString(release.TagName) {
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

func launchUpdater(args []string) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	// Pin the worker to the old physical release, not the mutable current junction.
	directory, err := finalDirectory(filepath.Dir(executable))
	if err != nil {
		return err
	}
	command := exec.Command(filepath.Join(directory, filepath.Base(executable)), append([]string{"--update"}, args...)...)
	command.Dir = directory
	if err := command.Start(); err != nil {
		return err
	}
	go func() {
		if err := command.Wait(); err != nil {
			state := readUpdateStatus()
			if state.active() {
				_ = writeUpdateStatus(updateStatus{Phase: "failed", TargetVersion: args[0], Error: "Updater exited before completing. Check diagnostics."})
			}
			logEvent("update.worker-exit", map[string]any{"error": err.Error()})
		}
	}()
	return nil
}

func runUpdater(args []string) (result int) {
	logEvent("update.starting", nil)
	defer func() { logEvent("update.finished", map[string]any{"exitCode": result}) }()
	lock, lockErr := acquireUpdateLock()
	if lockErr != nil {
		logEvent("update.locked", map[string]any{"error": lockErr.Error()})
		return 2
	}
	defer windows.CloseHandle(lock)
	targetVersion := ""
	if len(args) > 0 {
		targetVersion = args[0]
	}
	stage := func(phase string) { _ = writeUpdateStatus(updateStatus{Phase: phase, TargetVersion: targetVersion}) }
	fail := func(code int, step string, err error) int {
		message := step
		if err != nil {
			message += ": " + err.Error()
		}
		_ = writeUpdateStatus(updateStatus{Phase: "failed", TargetVersion: targetVersion, Error: message})
		logEvent("update.failed", map[string]any{"step": step, "error": message})
		return code
	}
	if len(args) != 4 || !releaseTagPattern.MatchString(args[0]) || !validGithubDownload(args[1]) || !validGithubDownload(args[2]) {
		return fail(2, "Invalid updater arguments", nil)
	}
	parentPID, err := parseInt(args[3])
	if err != nil || parentPID <= 0 || parentPID > int64(^uint32(0)) {
		return fail(2, "Invalid service process", err)
	}
	executable, err := os.Executable()
	if err != nil {
		return fail(2, "Cannot locate executable", err)
	}
	root := installationRoot(executable)
	if root == "" {
		return fail(2, "Cannot locate installation", nil)
	}
	current := filepath.Join(root, "current")
	releases := filepath.Join(root, "releases")
	oldTarget, err := finalDirectory(current)
	if err != nil || !withinDirectory(releases, oldTarget) {
		return fail(2, "Cannot resolve installed release junction", err)
	}
	// Stage on the installation volume so activation never requires a cross-volume rename.
	staging, err := os.MkdirTemp(releases, ".update-")
	if err != nil {
		return fail(2, "Cannot create update staging directory", err)
	}
	defer os.RemoveAll(staging)
	stage("downloading")
	archivePath := filepath.Join(staging, archiveName)
	checksumPath := filepath.Join(staging, checksumName)
	if err := download(args[1], archivePath); err != nil {
		return fail(3, "Cannot download package", err)
	}
	if err := download(args[2], checksumPath); err != nil {
		return fail(3, "Cannot download checksum", err)
	}
	stage("verifying")
	expected, err := expectedChecksum(checksumPath)
	if err != nil || expected != fileChecksum(archivePath) {
		return fail(3, "Package checksum does not match", err)
	}
	extracted := filepath.Join(staging, "extracted")
	if err := extractArchive(archivePath, extracted); err != nil {
		return fail(4, "Cannot extract package", err)
	}
	if err := validatePackage(extracted, args[0]); err != nil {
		return fail(4, "Invalid Companion package", err)
	}
	target := filepath.Join(releases, args[0])
	if !withinDirectory(releases, target) || strings.EqualFold(oldTarget, target) || isReparsePoint(target) {
		return fail(2, "Refusing to replace the running release", nil)
	}
	stage("installing")
	if err := os.RemoveAll(target); err != nil {
		return fail(6, "Cannot prepare target directory", err)
	}
	if err := os.Rename(extracted, target); err != nil {
		return fail(6, "Cannot stage new release", err)
	}
	stage("restarting")
	rolledBack, err := activateUpdate(current, oldTarget, target, args[0], activationOperations{
		stop:          stopCompanionService,
		wait:          func() bool { return waitForProcessExit(uint32(parentPID), 30*time.Second) },
		start:         func() bool { return runCommand("sc.exe", "start", serviceName) },
		switchRelease: replaceJunction, healthy: healthy,
		stopTray:  func() { _ = runCommand("schtasks.exe", "/End", "/TN", "JonaHomelabCompanionTray") },
		startTray: startTrayTask,
	})
	if err != nil {
		fail(5, "Cannot activate update", err)
		if rolledBack {
			_ = writeUpdateStatus(updateStatus{Phase: "rolled-back", TargetVersion: targetVersion, Error: err.Error()})
		}
		return 5
	}
	stage("succeeded")
	return 0
}

type activationOperations struct {
	stop, wait, start   func() bool
	switchRelease       func(string, string) error
	healthy             func(string) bool
	stopTray, startTray func()
}

func activateUpdate(current, oldTarget, target, version string, ops activationOperations) (bool, error) {
	if !ops.stop() {
		return false, errors.New("Windows rejected service stop")
	}
	if !ops.wait() {
		_ = ops.start()
		return false, errors.New("service did not stop")
	}
	ops.stopTray()
	err := ops.switchRelease(current, target)
	if err == nil {
		if ops.start() && ops.healthy(version) {
			ops.startTray()
			return false, nil
		}
		err = errors.New("new service did not pass its health check")
		// Stop the new service before restoring the old junction.
		if !ops.stop() {
			return false, errors.New("new service failed; rollback could not stop it")
		}
	}
	if restoreErr := ops.switchRelease(current, oldTarget); restoreErr != nil {
		return false, fmt.Errorf("%v; rollback: %w", err, restoreErr)
	}
	if !ops.start() || !ops.healthy(filepath.Base(oldTarget)) {
		return false, fmt.Errorf("%v; old service could not be restored", err)
	}
	ops.startTray()
	return true, err
}

func stopCompanionService() bool {
	manager, err := mgr.Connect()
	if err != nil {
		return false
	}
	defer manager.Disconnect()
	service, err := manager.OpenService(serviceName)
	if err != nil {
		return false
	}
	defer service.Close()
	status, err := service.Query()
	if err != nil {
		return false
	}
	if status.State == svc.Stopped {
		return true
	}
	if status.State != svc.StopPending {
		if _, err := service.Control(svc.Stop); err != nil {
			return false
		}
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		status, err = service.Query()
		if err != nil {
			return false
		}
		if status.State == svc.Stopped {
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false
}

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

func extractArchive(archivePath, destination string) error {
	if err := os.RemoveAll(destination); err != nil {
		return err
	}
	if err := os.MkdirAll(destination, 0o700); err != nil {
		return err
	}
	archive, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer archive.Close()
	root := filepath.Clean(destination) + string(os.PathSeparator)
	for _, entry := range archive.File {
		name := filepath.Clean(filepath.FromSlash(entry.Name))
		if name == "." || filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, ".."+string(os.PathSeparator)) {
			return errors.New("unsafe archive path")
		}
		target := filepath.Join(destination, name)
		if !strings.HasPrefix(filepath.Clean(target)+string(os.PathSeparator), root) {
			return errors.New("unsafe archive path")
		}
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o700); err != nil {
				return err
			}
			continue
		}
		if entry.Mode()&os.ModeSymlink != 0 {
			return errors.New("archive links are not allowed")
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		reader, err := entry.Open()
		if err != nil {
			return err
		}
		file, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
		if err == nil {
			_, err = io.Copy(file, io.LimitReader(reader, 256*1024*1024))
			closeErr := file.Close()
			if err == nil {
				err = closeErr
			}
		}
		_ = reader.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func validatePackage(root, version string) error {
	for _, name := range []string{"JonaHomelab.Companion.exe", "install.ps1", "uninstall.ps1", "diagnostics.ps1", "README.md", "RELEASE_VERSION"} {
		if info, err := os.Stat(filepath.Join(root, name)); err != nil || info.IsDir() {
			return errors.New("incomplete Companion package")
		}
	}
	content, err := os.ReadFile(filepath.Join(root, "RELEASE_VERSION"))
	if err != nil || strings.TrimSpace(string(content)) != version {
		return errors.New("invalid Companion package version")
	}
	return nil
}

func installationRoot(executable string) string {
	directory := filepath.Dir(executable)
	if strings.EqualFold(filepath.Base(directory), "current") {
		return filepath.Dir(directory)
	}
	if strings.EqualFold(filepath.Base(filepath.Dir(directory)), "releases") {
		return filepath.Dir(filepath.Dir(directory))
	}
	return filepath.Dir(directory)
}

func withinDirectory(directory, candidate string) bool {
	root, rootErr := filepath.Abs(directory)
	path, pathErr := filepath.Abs(candidate)
	if rootErr != nil || pathErr != nil {
		return false
	}
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, `..\`) && !filepath.IsAbs(relative)
}

func replaceJunction(path, target string) error {
	if !withinDirectory(filepath.Join(filepath.Dir(path), "releases"), target) {
		return errors.New("invalid Companion release path")
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink == 0 && !isReparsePoint(path) {
			return errors.New("refusing to replace a non-link installation path")
		}
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if runCommand("cmd.exe", "/c", "mklink", "/J", path, target) {
		return nil
	}
	return errors.New("could not activate Companion release")
}

func isReparsePoint(path string) bool {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return false
	}
	attributes, err := windows.GetFileAttributes(name)
	return err == nil && attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0
}

func runCommand(name string, args ...string) bool {
	command := exec.Command(name, args...)
	command.Dir = filepath.Dir(os.Args[0])
	return command.Run() == nil
}

func waitForProcessExit(pid uint32, timeout time.Duration) bool {
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, pid)
	if err != nil {
		return true
	}
	defer windows.CloseHandle(handle)
	milliseconds := uint32(timeout / time.Millisecond)
	result, err := windows.WaitForSingleObject(handle, milliseconds)
	return err == nil && result == windows.WAIT_OBJECT_0
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

func startTrayTask() {
	_ = runCommand("schtasks.exe", "/Run", "/TN", "JonaHomelabCompanionTray")
}
