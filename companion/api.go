//go:build windows

package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type runtimeState struct {
	config    *configStore
	replay    *replayGuard
	shutdown  *shutdownExecutor
	updates   *updateCoordinator
	simulated bool
}

func newRuntimeState(config *configStore) *runtimeState {
	return &runtimeState{
		config:   config,
		replay:   &replayGuard{values: make(map[string]time.Time)},
		shutdown: newShutdownExecutor(nil),
		updates:  newUpdateCoordinator(config),
	}
}

func (r *runtimeState) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", r.health)
	mux.HandleFunc("/v1/status", r.status)
	mux.HandleFunc("/v1/shutdown", r.shutdownRequest)
	mux.HandleFunc("/v1/power", r.powerRequest)
	return http.MaxBytesHandler(mux, 64*1024)
}

func (r *runtimeState) health(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet || !isLoopback(remoteIP(request)) {
		writePlainStatus(writer, http.StatusForbidden)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"status": "ok", "version": releaseVersion(), "simulated": r.simulated})
}

func (r *runtimeState) status(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writePlainStatus(writer, http.StatusMethodNotAllowed)
		return
	}
	body, nonce, status, ok := r.authenticate(request, false)
	if !ok {
		writePlainStatus(writer, status)
		return
	}
	_ = body
	r.config.recordServerCall(time.Now())
	logEvent("status.ok", map[string]any{"client": remoteIP(request).String()})
	responseBody, _ := json.Marshal(struct {
		Ready    bool   `json:"ready"`
		Version  string `json:"version"`
		Accepted bool   `json:"accepted"`
	}{true, releaseVersion(), true})
	r.writeSigned(writer, http.StatusOK, nonce, responseBody)
}

func (r *runtimeState) shutdownRequest(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writePlainStatus(writer, http.StatusMethodNotAllowed)
		return
	}
	body, nonce, status, ok := r.authenticate(request, true)
	if !ok {
		writePlainStatus(writer, status)
		return
	}
	force, valid := readForce(body)
	if !valid {
		writePlainStatus(writer, http.StatusBadRequest)
		return
	}
	r.config.recordServerCall(time.Now())
	accepted, err := r.shutdown.trySchedule(force)
	responseStatus := http.StatusAccepted
	if err != nil {
		responseStatus = http.StatusInternalServerError
	} else if !accepted {
		responseStatus = http.StatusTooManyRequests
	}
	logEvent("shutdown.response", map[string]any{"client": remoteIP(request).String(), "force": force, "status": responseStatus, "simulated": r.simulated})
	responseBody, _ := json.Marshal(struct {
		Accepted   bool `json:"accepted"`
		RetryAfter int  `json:"retryAfter"`
	}{accepted, 10})
	r.writeSigned(writer, responseStatus, nonce, responseBody)
}

func (r *runtimeState) authenticate(request *http.Request, requireBody bool) (string, string, int, bool) {
	reject := func(status int, reason string) (string, string, int, bool) {
		logEvent("request.rejected", map[string]any{"client": remoteIP(request).String(), "status": status, "reason": reason})
		return "", request.Header.Get(nonceHeader), status, false
	}
	if !isPrivateClient(remoteIP(request)) {
		return reject(http.StatusForbidden, "client outside private IPv4 network")
	}
	body := ""
	if requireBody {
		content, err := io.ReadAll(io.LimitReader(request.Body, 64*1024+1))
		if err != nil || len(content) > 64*1024 {
			return reject(http.StatusRequestEntityTooLarge, "body too large or unreadable")
		}
		body = string(content)
	}
	secret, err := r.config.secret()
	if err != nil {
		return reject(http.StatusInternalServerError, "secret unavailable")
	}
	nonce, valid := verifyRequest(secret, request.Method, request.URL.Path, body, request.Header, time.Now())
	if !valid {
		return reject(http.StatusUnauthorized, "invalid signature or timestamp; check pairing and clock")
	}
	if !r.replay.tryUse(nonce) {
		return reject(http.StatusConflict, "replayed nonce")
	}
	return body, nonce, http.StatusOK, true
}

func readPower(body string) (powerAction, bool, bool) {
	var input struct {
		Action powerAction `json:"action"`
		Force  *bool       `json:"force"`
	}
	decoder := json.NewDecoder(strings.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || input.Force == nil {
		return "", false, false
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return "", false, false
	}
	if input.Action != powerShutdown && input.Action != powerSleep && input.Action != powerHibernate {
		return "", false, false
	}
	if input.Action != powerShutdown && *input.Force {
		return "", false, false
	}
	return input.Action, *input.Force, true
}

func (r *runtimeState) powerRequest(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writePlainStatus(writer, http.StatusMethodNotAllowed)
		return
	}
	body, nonce, status, ok := r.authenticate(request, true)
	if !ok {
		writePlainStatus(writer, status)
		return
	}
	action, force, valid := readPower(body)
	if !valid {
		writePlainStatus(writer, http.StatusBadRequest)
		return
	}
	r.config.recordServerCall(time.Now())
	var accepted bool
	var err error
	if action == powerShutdown {
		accepted, err = r.shutdown.trySchedule(force)
	} else {
		accepted, err = r.shutdown.trySuspend(action)
	}
	status = http.StatusAccepted
	if errors.Is(err, errPowerUnsupported) {
		status = http.StatusConflict
	} else if err != nil {
		status = http.StatusInternalServerError
	} else if !accepted {
		status = http.StatusTooManyRequests
	}
	logEvent("power.response", map[string]any{"action": action, "client": remoteIP(request).String(), "status": status, "simulated": r.simulated})
	response, _ := json.Marshal(map[string]any{"accepted": accepted, "retryAfter": 10})
	r.writeSigned(writer, status, nonce, response)
}

func (r *runtimeState) writeSigned(writer http.ResponseWriter, status int, nonce string, body []byte) {
	secret, err := r.config.secret()
	if err != nil {
		writePlainStatus(writer, http.StatusInternalServerError)
		return
	}
	writer.Header().Set(responseSignatureHeader, signResponse(secret, status, nonce, string(body)))
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_, _ = writer.Write(body)
}

func readForce(body string) (bool, bool) {
	var fields map[string]json.RawMessage
	decoder := json.NewDecoder(strings.NewReader(body))
	if decoder.Decode(&fields) != nil || len(fields) != 1 {
		return false, false
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return false, false
	}
	value, ok := fields["force"]
	if !ok || string(value) == "null" {
		return false, false
	}
	var force bool
	if json.Unmarshal(value, &force) != nil {
		return false, false
	}
	return force, true
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	body, err := json.Marshal(value)
	if err != nil {
		writePlainStatus(writer, http.StatusInternalServerError)
		return
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_, _ = writer.Write(body)
}

func writePlainStatus(writer http.ResponseWriter, status int) {
	http.Error(writer, http.StatusText(status), status)
}

func remoteIP(request *http.Request) net.IP {
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		return net.ParseIP(request.RemoteAddr)
	}
	return net.ParseIP(host)
}

func isLoopback(address net.IP) bool {
	return address != nil && address.IsLoopback()
}

func isPrivateClient(address net.IP) bool {
	if address == nil {
		return false
	}
	if address.IsLoopback() {
		return true
	}
	ipv4 := address.To4()
	if ipv4 == nil {
		return false
	}
	return ipv4[0] == 10 || (ipv4[0] == 172 && ipv4[1] >= 16 && ipv4[1] <= 31) || (ipv4[0] == 192 && ipv4[1] == 168)
}

type replayGuard struct {
	mu     sync.Mutex
	values map[string]time.Time
}

func (g *replayGuard) tryUse(nonce string) bool {
	now := time.Now()
	g.mu.Lock()
	defer g.mu.Unlock()
	for value, timestamp := range g.values {
		if now.Sub(timestamp) > 5*time.Minute {
			delete(g.values, value)
		}
	}
	if _, exists := g.values[nonce]; exists {
		return false
	}
	g.values[nonce] = now
	return true
}

func runHTTP(ctx context.Context, state *runtimeState, listener net.Listener) error {
	server := &http.Server{
		Handler:           state.handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	errorChannel := make(chan error, 1)
	go func() {
		err := server.Serve(listener)
		if !errors.Is(err, http.ErrServerClosed) {
			errorChannel <- err
		}
		close(errorChannel)
	}()
	select {
	case err := <-errorChannel:
		return err
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdownContext)
	}
}
