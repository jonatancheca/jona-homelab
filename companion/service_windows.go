//go:build windows

package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"golang.org/x/sys/windows/svc"
)

type companionService struct{}

func runService() error {
	return svc.Run(serviceName, companionService{})
}

func (companionService) Execute(_ []string, requests <-chan svc.ChangeRequest, statuses chan<- svc.Status) (bool, uint32) {
	statuses <- svc.Status{State: svc.StartPending, WaitHint: 5000}
	logEvent("service.starting", map[string]any{"version": releaseVersion()})
	config, err := loadConfig()
	if err != nil {
		writeServiceLog("startup: " + err.Error())
		statuses <- svc.Status{State: svc.Stopped, Win32ExitCode: 1}
		return false, 1
	}
	state := newRuntimeState(config)
	listener, err := net.Listen("tcp4", fmt.Sprintf("0.0.0.0:%d", companionPort))
	if err != nil {
		writeServiceLog("listen: " + err.Error())
		return false, 1
	}
	defer listener.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serverErrors := make(chan error, 1)
	go func() { serverErrors <- runHTTP(ctx, state, listener) }()
	go runPipeServer(ctx, state)
	go refreshInstalledTrayTask(ctx)
	go runUpdateLoop(ctx, state.updates)
	statuses <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	logEvent("service.running", map[string]any{"port": companionPort, "version": releaseVersion()})

	for {
		select {
		case request := <-requests:
			switch request.Cmd {
			case svc.Interrogate:
				statuses <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
			case svc.Stop, svc.Shutdown:
				statuses <- svc.Status{State: svc.StopPending, WaitHint: 10000}
				logEvent("service.stopping", map[string]any{"command": request.Cmd})
				cancel()
				<-serverErrors
				logEvent("service.stopped", nil)
				return false, 0
			}
		case err := <-serverErrors:
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				writeServiceLog("http: " + err.Error())
				statuses <- svc.Status{State: svc.Stopped, Win32ExitCode: 1}
				return false, 1
			}
			return false, 0
		}
	}
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
