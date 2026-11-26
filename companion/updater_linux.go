package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const serviceUnit = "jona-homelab-companion.service"

func readUpdateStatus() updateStatus {
	var state updateStatus
	b, err := os.ReadFile(updateStatusPath())
	if err != nil || json.Unmarshal(b, &state) != nil {
		return updateStatus{Phase: "idle"}
	}
	if state.active() {
		stamp, err := time.Parse(time.RFC3339Nano, state.UpdatedAt)
		if err != nil || time.Since(stamp) > 15*time.Minute || state.PID == 0 || syscall.Kill(int(state.PID), 0) != nil {
			state.Phase, state.Error = "failed", "Update interrupted. Check diagnostics and retry."
		}
	}
	return state
}

func writeUpdateStatus(state updateStatus) error {
	state.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	state.PID = uint32(os.Getpid())
	b, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return writePrivate(updateStatusPath(), b, true)
}

func launchUpdater(args []string) error {
	if len(args) != 4 || !releaseTagPattern.MatchString(args[0]) {
		return errors.New("invalid update target")
	}
	_, err := systemCommand(5*time.Second, "/usr/bin/systemctl", "--no-block", "start", "jona-homelab-companion-update@"+args[0]+".service")
	return err
}

func releasePackage(tag, dir string) (string, error) {
	if !releaseTagPattern.MatchString(tag) {
		return "", errors.New("invalid release tag")
	}
	base := "https://github.com/" + githubRepository + "/releases/download/" + tag + "/"
	path := filepath.Join(dir, archiveName)
	if err := download(base+checksumName, path+".sha256"); err != nil {
		return "", err
	}
	checksum, err := expectedChecksum(path + ".sha256")
	if err != nil {
		return "", err
	}
	if err := download(base+archiveName, path); err != nil {
		return "", err
	}
	if fileChecksum(path) != checksum {
		return "", errors.New("package checksum mismatch")
	}
	if err := verifyPackage(path, tag); err != nil {
		return "", err
	}
	return path, nil
}

func verifyPackage(path, tag string) error {
	name, err := systemCommand(15*time.Second, "/usr/bin/pacman", "-Qqp", "--", path)
	if err != nil || name != "jona-homelab-companion" {
		return errors.New("unexpected pacman package")
	}
	release, err := systemCommand(15*time.Second, "/usr/bin/bsdtar", "-xOf", path, "usr/share/jona-homelab-companion/RELEASE_VERSION")
	if err != nil || release != tag {
		return errors.New("wrong Companion release in package")
	}
	return nil
}

func rollbackPackage(dir string) (string, error) {
	// The installation guide saves the initial local package in pacman's root-owned cache.
	installed, err := systemCommand(5*time.Second, "/usr/bin/pacman", "-Q", "jona-homelab-companion")
	if err != nil {
		return "", err
	}
	paths, _ := filepath.Glob("/var/cache/pacman/pkg/jona-homelab-companion-*.pkg.tar.zst")
	for _, path := range paths {
		candidate, err := systemCommand(5*time.Second, "/usr/bin/pacman", "-Qp", "--", path)
		if err == nil && candidate == installed && verifyPackage(path, releaseVersion()) == nil {
			return path, nil
		}
	}
	if releaseTagPattern.MatchString(releaseVersion()) {
		return releasePackage(releaseVersion(), dir)
	}
	return "", errors.New("local rollback package missing: copy the installed local package into /var/cache/pacman/pkg before updating")
}

func installPackage(path string) error {
	_, err := systemCommand(2*time.Minute, "/usr/bin/pacman", "--noconfirm", "-U", "--", path)
	return err
}

func restartHealthy(tag string) bool {
	if _, err := systemCommand(15*time.Second, "/usr/bin/systemctl", "daemon-reload"); err != nil {
		return false
	}
	if _, err := systemCommand(30*time.Second, "/usr/bin/systemctl", "restart", serviceUnit); err != nil {
		return false
	}
	return healthy(tag)
}

func applyUpdate(tag string) (resultErr error) {
	if !releaseTagPattern.MatchString(tag) {
		return errors.New("invalid update target")
	}
	lock, err := os.OpenFile(filepath.Join(dataDirectory(), "update.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return err
	}
	stage := func(phase, detail string) error {
		return writeUpdateStatus(updateStatus{Phase: phase, TargetVersion: tag, Error: detail})
	}
	rolledBack := false
	defer func() {
		if resultErr != nil && !rolledBack {
			_ = stage("failed", resultErr.Error())
		}
	}()
	if err := stage("downloading", ""); err != nil {
		return err
	}
	work, err := os.MkdirTemp("", "companion-update-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	target, err := releasePackage(tag, work)
	if err != nil {
		return err
	}
	if err := stage("verifying", ""); err != nil {
		return err
	}
	previousDir := filepath.Join(work, "previous")
	if err := os.Mkdir(previousDir, 0700); err != nil {
		return err
	}
	previous, err := rollbackPackage(previousDir)
	if err != nil {
		return err
	}
	installed, err := systemCommand(5*time.Second, "/usr/bin/pacman", "-Q", "jona-homelab-companion")
	if err != nil {
		return err
	}
	candidate, err := systemCommand(5*time.Second, "/usr/bin/pacman", "-Qp", "--", target)
	if err != nil {
		return err
	}
	oldFields, newFields := strings.Fields(installed), strings.Fields(candidate)
	if len(oldFields) != 2 || len(newFields) != 2 {
		return errors.New("invalid package version")
	}
	cmp, err := systemCommand(5*time.Second, "/usr/bin/vercmp", newFields[1], oldFields[1])
	if err != nil || (cmp != "0" && cmp != "1") {
		return errors.New("refusing an older package")
	}
	if err := stage("installing", ""); err != nil {
		return err
	}
	if err := installPackage(target); err != nil {
		return err
	}
	_ = stage("restarting", "")
	if restartHealthy(tag) {
		return stage("succeeded", "")
	}
	if err := installPackage(previous); err != nil {
		return fmt.Errorf("health check failed; rollback failed: %w", err)
	}
	if !restartHealthy(releaseVersion()) {
		return errors.New("rollback health check failed")
	}
	rolledBack = true
	return stage("rolled-back", "Previous package restored after failed health check.")
}
