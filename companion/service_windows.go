//go:build windows

package main

import (
	"context"
	"errors"
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
	config, err := loadConfig()
	if err != nil {
		statuses <- svc.Status{State: svc.Stopped, Win32ExitCode: 1}
		return false, 1
	}
	state := newRuntimeState(config)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serverErrors := make(chan error, 1)
	go func() { serverErrors <- runHTTP(ctx, state) }()
	go runPipeServer(ctx, state)
	go runUpdateLoop(ctx, state.updates)
	statuses <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}

	for {
		select {
		case request := <-requests:
			switch request.Cmd {
			case svc.Interrogate:
				statuses <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
			case svc.Stop, svc.Shutdown:
				statuses <- svc.Status{State: svc.StopPending, WaitHint: 10000}
				return false, 0
			}
		case err := <-serverErrors:
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				statuses <- svc.Status{State: svc.Stopped, Win32ExitCode: 1}
				return false, 1
			}
			return false, 0
		}
	}
}

func runUpdateLoop(ctx context.Context, updates *updateCoordinator) {
	select {
	case <-time.After(5 * time.Second):
		updates.checkAndSchedule(ctx)
	case <-ctx.Done():
		return
	}
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			updates.checkAndSchedule(ctx)
		case <-ctx.Done():
			return
		}
	}
}
