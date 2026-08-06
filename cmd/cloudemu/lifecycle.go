//go:build unix

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	stateFileName     = "state.json"
	logFileName       = "cloudemu.log"
	endpointsFileName = "endpoints.json"

	startupTimeout = 15 * time.Second
	stopTimeout    = 12 * time.Second
	healthInterval = 100 * time.Millisecond
	dialTimeout    = time.Second

	dirPerm  = 0o755
	filePerm = 0o600
)

// Sentinel errors so callers (and err113) get static, wrappable failures.
var (
	errTimeout     = errors.New("timed out")
	errNoEndpoints = errors.New("no endpoints to probe")
	errUnknownCmd  = errors.New("unknown lifecycle command")
)

// daemonState is the run-dir record describing a running standalone server.
type daemonState struct {
	PID       int               `json:"pid"`
	Endpoints map[string]string `json:"endpoints"`
	StartedAt string            `json:"startedAt"`
	Args      []string          `json:"args"`
}

// runDir resolves the directory that holds the daemon's pid/log/endpoints.
// An explicit --home wins; otherwise it defaults to ~/.cloudemu.
func runDir(home string) (string, error) {
	if home != "" {
		return home, nil
	}

	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(h, ".cloudemu"), nil
}

func statePath(dir string) string     { return filepath.Join(dir, stateFileName) }
func logPath(dir string) string       { return filepath.Join(dir, logFileName) }
func endpointsPath(dir string) string { return filepath.Join(dir, endpointsFileName) }

func writeState(dir string, s daemonState) error {
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return err
	}

	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(statePath(dir), b, filePerm)
}

func readState(dir string) (daemonState, error) {
	var s daemonState

	b, err := os.ReadFile(statePath(dir))
	if err != nil {
		return s, err
	}

	if err := json.Unmarshal(b, &s); err != nil {
		return s, err
	}

	return s, nil
}

func removeState(dir string) error {
	if err := os.Remove(statePath(dir)); err != nil && !os.IsNotExist(err) {
		return err
	}

	return nil
}

// processAlive reports whether pid names a live process (signal 0 probe).
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}

	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}

	return p.Signal(syscall.Signal(0)) == nil
}

// terminate stops another process (not our child) gracefully (SIGTERM), then
// escalates to SIGKILL if it hasn't exited within stopTimeout. Used by `stop`,
// where the target daemon was started by a previous CLI invocation and has been
// reparented to init (so it's reaped on death and signal-0 reports it gone).
func terminate(p *os.Process) error {
	_ = p.Signal(syscall.SIGTERM)

	if waitExit(p.Pid, stopTimeout) == nil {
		return nil
	}

	_ = p.Kill()

	return waitExit(p.Pid, stopTimeout)
}

// killChild stops a process we started (our direct child), reaping it so it
// doesn't linger as a zombie. SIGTERM first, escalate to SIGKILL after
// stopTimeout. Reaping via Wait is what makes this safe on the spawnServe
// failure path, where the CLI is still the child's parent.
func killChild(cmd *exec.Cmd) {
	_ = cmd.Process.Signal(syscall.SIGTERM)

	done := make(chan struct{})

	go func() {
		_, _ = cmd.Process.Wait()

		close(done)
	}()

	select {
	case <-done:
	case <-time.After(stopTimeout):
		_ = cmd.Process.Kill()

		<-done
	}
}

// daemonReachable reports whether the recorded endpoints answer — a portable
// identity check that guards against a recycled PID (a live but unrelated
// process) before we signal it.
func daemonReachable(eps map[string]string) bool {
	if u := httpHealthURL(eps); u != "" {
		return pollHealth(u, dialTimeout) == nil
	}

	for _, k := range []string{"azure", "kubernetes"} {
		if ep := eps[k]; ep != "" {
			if hp, err := hostPortOf(ep); err == nil {
				return pollTCP(hp, dialTimeout) == nil
			}
		}
	}

	return false
}

// pollHealth polls an HTTP health URL until it returns 200 or timeout elapses.
// It is used only for the plain-HTTP endpoints (AWS/GCP); the self-signed HTTPS
// endpoints use pollTCP instead so no TLS verification has to be disabled.
func pollHealth(healthURL string, timeout time.Duration) error {
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(timeout)

	for {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, healthURL, http.NoBody)
		if err != nil {
			return err
		}

		if resp, err := client.Do(req); err == nil {
			_ = resp.Body.Close()

			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("waiting for %s to become healthy: %w", healthURL, errTimeout)
		}

		time.Sleep(healthInterval)
	}
}

// pollTCP polls until a TCP connection to hostPort succeeds or timeout elapses.
// It confirms a listener is accepting without needing TLS trust — used for the
// self-signed HTTPS endpoints (Azure/Kubernetes).
func pollTCP(hostPort string, timeout time.Duration) error {
	var dialer net.Dialer

	deadline := time.Now().Add(timeout)

	for {
		ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
		conn, err := dialer.DialContext(ctx, "tcp", hostPort)

		cancel()

		if err == nil {
			_ = conn.Close()

			return nil
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("waiting for %s to accept connections: %w", hostPort, errTimeout)
		}

		time.Sleep(healthInterval)
	}
}

// waitServerReady blocks until every present endpoint is accepting requests:
// an HTTP health probe for the plain-HTTP endpoints (AWS/GCP) and a TCP probe
// for the self-signed HTTPS endpoints (Azure/Kubernetes).
func waitServerReady(eps map[string]string, timeout time.Duration) error {
	if len(eps) == 0 {
		return errNoEndpoints
	}

	for _, k := range []string{"aws", "gcp"} {
		if ep := eps[k]; ep != "" {
			if err := pollHealth(strings.TrimRight(ep, "/")+"/_cloudemu/health", timeout); err != nil {
				return err
			}
		}
	}

	for _, k := range []string{"azure", "kubernetes"} {
		if ep := eps[k]; ep != "" {
			hp, err := hostPortOf(ep)
			if err != nil {
				return err
			}

			if err := pollTCP(hp, timeout); err != nil {
				return err
			}
		}
	}

	return nil
}

// httpHealthURL returns a plain-HTTP /_cloudemu/health URL, or "" if none.
func httpHealthURL(eps map[string]string) string {
	for _, k := range []string{"aws", "gcp"} {
		if ep := eps[k]; ep != "" {
			return strings.TrimRight(ep, "/") + "/_cloudemu/health"
		}
	}

	return ""
}

// hostPortOf extracts host:port from an endpoint URL.
func hostPortOf(endpoint string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}

	return u.Host, nil
}

// splitHomeFlag extracts --home / --home=value (also single-dash) from args and
// returns the value plus the remaining args to forward to `serve`.
func splitHomeFlag(args []string) (home string, rest []string) {
	rest = make([]string, 0, len(args))

	for i := 0; i < len(args); i++ {
		a := args[i]

		switch {
		case a == "--home" || a == "-home":
			if i+1 < len(args) {
				home = args[i+1]
				i++
			}
		case strings.HasPrefix(a, "--home="):
			home = strings.TrimPrefix(a, "--home=")
		case strings.HasPrefix(a, "-home="):
			home = strings.TrimPrefix(a, "-home=")
		default:
			rest = append(rest, a)
		}
	}

	return home, rest
}

// readEndpoints loads and prunes the endpoints file serve writes on startup.
func readEndpoints(path string) (map[string]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	raw := map[string]string{}
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, err
	}

	out := make(map[string]string, len(raw))

	for k, v := range raw {
		if v != "" {
			out[k] = v
		}
	}

	return out, nil
}

// waitForEndpoints polls the endpoints file until serve has written it.
func waitForEndpoints(path string, timeout time.Duration) (map[string]string, error) {
	deadline := time.Now().Add(timeout)

	for {
		eps, err := readEndpoints(path)
		if err == nil && len(eps) > 0 {
			return eps, nil
		}

		if time.Now().After(deadline) {
			return nil, fmt.Errorf("waiting for endpoints file %s: %w", path, errTimeout)
		}

		time.Sleep(healthInterval)
	}
}

func printEndpoints(eps map[string]string) {
	fmt.Println("cloudemu — running")
	fmt.Println("──────────────────")

	for _, k := range []string{"aws", "azure", "gcp", "kubernetes"} {
		if ep := eps[k]; ep != "" {
			fmt.Printf("  %-11s %s\n", k, ep)
		}
	}
}

// runStart launches `cloudemu serve` as a detached background process and waits
// for it to become ready. Remaining args (after --home) pass through to serve.
func runStart(args []string) error {
	home, rest := splitHomeFlag(args)

	dir, err := runDir(home)
	if err != nil {
		return err
	}

	if s, rErr := readState(dir); rErr == nil && processAlive(s.PID) && daemonReachable(s.Endpoints) {
		fmt.Printf("cloudemu already running (pid %d)\n", s.PID)
		printEndpoints(s.Endpoints)

		return nil
	}

	if mkErr := os.MkdirAll(dir, dirPerm); mkErr != nil {
		return mkErr
	}

	epPath := endpointsPath(dir)
	_ = os.Remove(epPath) // drop a stale file so waitForEndpoints sees the fresh one

	eps, err := spawnServe(dir, rest, epPath)
	if err != nil {
		return err
	}

	printEndpoints(eps)

	return nil
}

// spawnServe forks the detached serve process, waits for readiness, records the
// run state, and returns the resolved endpoints. On a readiness failure it
// stops the child and surfaces the log path.
func spawnServe(dir string, serveArgs []string, epPath string) (map[string]string, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}

	logF, err := os.OpenFile(logPath(dir), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, filePerm)
	if err != nil {
		return nil, err
	}
	defer logF.Close()

	full := append([]string{"serve", "--endpoints-file", epPath, "--quiet"}, serveArgs...)
	cmd := exec.CommandContext(context.Background(), exe, full...)
	cmd.Stdout = logF
	cmd.Stderr = logF
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if startErr := cmd.Start(); startErr != nil {
		return nil, startErr
	}

	eps, readyErr := waitForEndpoints(epPath, startupTimeout)
	if readyErr == nil {
		readyErr = waitServerReady(eps, startupTimeout)
	}

	if readyErr != nil {
		killChild(cmd) // don't leave an untracked, detached child

		return nil, fmt.Errorf("cloudemu failed to start (see %s): %w", logPath(dir), readyErr)
	}

	state := daemonState{
		PID:       cmd.Process.Pid,
		Endpoints: eps,
		StartedAt: time.Now().UTC().Format(time.RFC3339),
		Args:      serveArgs,
	}

	// A persisted state is what makes the daemon findable by stop/status; if we
	// can't record it, kill the child rather than orphan it.
	if err := writeState(dir, state); err != nil {
		killChild(cmd)

		return nil, fmt.Errorf("failed to persist daemon state (daemon killed): %w", err)
	}

	return eps, nil
}

// runStop signals the running daemon to shut down and waits for it to exit.
func runStop(args []string) error {
	home, _ := splitHomeFlag(args)

	dir, err := runDir(home)
	if err != nil {
		return err
	}

	s, err := readState(dir)
	if errors.Is(err, os.ErrNotExist) {
		fmt.Println("cloudemu is not running")

		return nil
	}

	if err != nil {
		return fmt.Errorf("reading state (daemon may still be running): %w", err)
	}

	// Treat a dead pid, or a live pid whose endpoints don't answer (PID reused
	// by an unrelated process), as stale — clean up without signaling it.
	if !processAlive(s.PID) || !daemonReachable(s.Endpoints) {
		_ = removeState(dir)

		fmt.Println("cloudemu is not running (cleaned up stale state)")

		return nil
	}

	proc, err := os.FindProcess(s.PID)
	if err != nil {
		return err
	}

	if err := terminate(proc); err != nil {
		return fmt.Errorf("stopping pid %d: %w", s.PID, err)
	}

	_ = removeState(dir)

	fmt.Printf("cloudemu stopped (pid %d)\n", s.PID)

	return nil
}

// waitExit blocks until pid is no longer alive or timeout elapses.
func waitExit(pid int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)

	for processAlive(pid) {
		if time.Now().After(deadline) {
			return fmt.Errorf("waiting for pid %d to exit: %w", pid, errTimeout)
		}

		time.Sleep(healthInterval)
	}

	return nil
}

// runStatus reports whether the daemon is running and its endpoints.
func runStatus(args []string) error {
	home, _ := splitHomeFlag(args)

	dir, err := runDir(home)
	if err != nil {
		return err
	}

	s, err := readState(dir)
	if errors.Is(err, os.ErrNotExist) {
		fmt.Println("cloudemu: stopped")

		return nil
	}

	if err != nil {
		return fmt.Errorf("reading state (daemon may still be running): %w", err)
	}

	if !processAlive(s.PID) {
		fmt.Println("cloudemu: stopped")

		return nil
	}

	fmt.Printf("cloudemu: running (pid %d, since %s)\n", s.PID, s.StartedAt)
	printEndpoints(s.Endpoints)

	return nil
}

// runLogs prints the daemon log, optionally following new output with -f.
func runLogs(args []string) error {
	home, rest := splitHomeFlag(args)
	follow := false

	for _, a := range rest {
		if a == "-f" || a == "--follow" {
			follow = true
		}
	}

	dir, err := runDir(home)
	if err != nil {
		return err
	}

	return tailLog(logPath(dir), follow)
}

// runDelete stops the daemon (if running) and removes only cloudemu's own files
// from the run directory — never a blanket RemoveAll of a user-supplied --home.
func runDelete(args []string) error {
	home, _ := splitHomeFlag(args)

	if err := runStop(args); err != nil {
		return err
	}

	dir, err := runDir(home)
	if err != nil {
		return err
	}

	for _, p := range []string{statePath(dir), logPath(dir), endpointsPath(dir)} {
		if rmErr := os.Remove(p); rmErr != nil && !os.IsNotExist(rmErr) {
			return rmErr
		}
	}

	// Remove the dir only if it's now empty (ignore "not empty" / "not exist").
	_ = os.Remove(dir)

	fmt.Printf("cloudemu: removed cloudemu files under %s\n", dir)

	return nil
}

// tailLog prints the log file. When follow is set it streams appended output
// until interrupted, re-seeking to the start if the log is truncated (e.g. a
// restart reopens it with O_TRUNC).
func tailLog(path string, follow bool) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("no log at %s: %w", path, err)
	}
	defer f.Close()

	offset, err := io.Copy(os.Stdout, f)
	if err != nil {
		return err
	}

	if !follow {
		return nil
	}

	for {
		time.Sleep(healthInterval)

		fi, err := f.Stat()
		if err != nil {
			return err
		}

		if fi.Size() < offset {
			if _, seekErr := f.Seek(0, io.SeekStart); seekErr != nil {
				return seekErr
			}

			offset = 0
		}

		n, err := io.Copy(os.Stdout, f)
		if err != nil {
			return err
		}

		offset += n
	}
}

// runLifecycle dispatches the start/stop/status/logs/delete subcommands.
func runLifecycle(cmd string, args []string) error {
	switch cmd {
	case cmdStart:
		return runStart(args)
	case cmdStop:
		return runStop(args)
	case cmdStatus:
		return runStatus(args)
	case cmdLogs:
		return runLogs(args)
	case cmdDelete:
		return runDelete(args)
	default:
		return fmt.Errorf("%w: %q", errUnknownCmd, cmd)
	}
}
