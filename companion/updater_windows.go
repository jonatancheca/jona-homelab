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

type updateCoordinator struct {
	config *configStore
	mu     sync.Mutex
	client *http.Client
}

func newUpdateCoordinator(config *configStore) *updateCoordinator {
	return &updateCoordinator{config: config, client: &http.Client{Timeout: 30 * time.Second}}
}

func (u *updateCoordinator) checkAndSchedule(ctx context.Context) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/"+githubRepository+"/releases/latest", nil)
	if err != nil {
		return false
	}
	request.Header.Set("User-Agent", "JonaHomelabCompanion/Go")
	response, err := u.client.Do(request)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return false
	}
	var release struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if json.NewDecoder(response.Body).Decode(&release) != nil || !releaseTagPattern.MatchString(release.TagName) || release.TagName == releaseVersion() {
		return false
	}
	assets := make(map[string]string, len(release.Assets))
	for _, asset := range release.Assets {
		assets[asset.Name] = asset.URL
	}
	archiveURL, archiveOK := assets[archiveName]
	checksumURL, checksumOK := assets[checksumName]
	if !archiveOK || !checksumOK || !validGithubDownload(archiveURL) || !validGithubDownload(checksumURL) {
		return false
	}
	executable, err := os.Executable()
	if err != nil {
		return false
	}
	command := exec.Command(executable, "--update", release.TagName, archiveURL, checksumURL, fmt.Sprint(os.Getpid()))
	command.Dir = filepath.Dir(executable)
	if err := command.Start(); err != nil {
		return false
	}
	return true
}

func runUpdater(args []string) int {
	if len(args) != 4 || !releaseTagPattern.MatchString(args[0]) || !validGithubDownload(args[1]) || !validGithubDownload(args[2]) {
		return 2
	}
	parentPID, err := parseInt(args[3])
	if err != nil || parentPID <= 0 || parentPID > int64(^uint32(0)) {
		return 2
	}
	executable, err := os.Executable()
	if err != nil {
		return 2
	}
	root := installationRoot(executable)
	if root == "" {
		return 2
	}
	current := filepath.Join(root, "current")
	releases := filepath.Join(root, "releases")
	oldTarget, err := filepath.EvalSymlinks(current)
	if err != nil || !withinDirectory(releases, oldTarget) {
		return 2
	}
	staging := filepath.Join(dataDirectory(), "update", args[0])
	if err := os.MkdirAll(staging, 0o700); err != nil {
		return 2
	}
	defer os.RemoveAll(staging)
	archivePath := filepath.Join(staging, archiveName)
	checksumPath := filepath.Join(staging, checksumName)
	if err := download(args[1], archivePath); err != nil {
		return 3
	}
	if err := download(args[2], checksumPath); err != nil {
		return 3
	}
	expected, err := expectedChecksum(checksumPath)
	if err != nil || expected != fileChecksum(archivePath) {
		return 3
	}
	extracted := filepath.Join(staging, "extracted")
	if err := extractArchive(archivePath, extracted); err != nil {
		return 4
	}
	if err := validatePackage(extracted, args[0]); err != nil {
		return 4
	}
	_ = runCommand("sc.exe", "stop", serviceName)
	if !waitForProcessExit(uint32(parentPID), 30*time.Second) {
		_ = runCommand("sc.exe", "start", serviceName)
		return 5
	}
	_ = runCommand("taskkill.exe", "/IM", filepath.Base(executable), "/FI", fmt.Sprintf("PID ne %d", os.Getpid()), "/T", "/F")
	target := filepath.Join(releases, args[0])
	if !withinDirectory(releases, target) {
		return 2
	}
	if err := os.MkdirAll(releases, 0o700); err != nil {
		return 6
	}
	if err := os.RemoveAll(target); err != nil {
		return 6
	}
	if err := os.Rename(extracted, target); err != nil {
		return 6
	}
	if err := replaceJunction(current, target); err != nil {
		_ = os.RemoveAll(target)
		_ = replaceJunction(current, oldTarget)
		_ = runCommand("sc.exe", "start", serviceName)
		return 6
	}
	_ = runCommand("sc.exe", "start", serviceName)
	if healthy(args[0]) {
		startTrayTask()
		return 0
	}
	_ = replaceJunction(current, oldTarget)
	_ = runCommand("sc.exe", "start", serviceName)
	return 5
}

func validGithubDownload(value string) bool {
	parsed, err := http.NewRequest(http.MethodGet, value, nil)
	return err == nil && parsed.URL.Scheme == "https" && (strings.EqualFold(parsed.URL.Hostname(), "github.com") || strings.EqualFold(parsed.URL.Hostname(), "objects.githubusercontent.com"))
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
	for _, name := range []string{"JonaHomelab.Companion.exe", "install.ps1", "uninstall.ps1", "README.md", "RELEASE_VERSION"} {
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
	root = strings.TrimRight(root, `\`) + `\`
	path = strings.TrimRight(path, `\`) + `\`
	return strings.HasPrefix(strings.ToLower(path), strings.ToLower(root))
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
				Status  string `json:"status"`
				Version string `json:"version"`
			}
			_ = json.NewDecoder(response.Body).Decode(&body)
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK && body.Status == "ok" && body.Version == expected {
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
