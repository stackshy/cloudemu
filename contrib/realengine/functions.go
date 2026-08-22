package realengine

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
)

var (
	errNotDeployed     = errors.New("function not deployed")
	errBadHandler      = errors.New("invalid handler: want file.function")
	errZipSlip         = errors.New("illegal path in archive")
	errEmptyResult     = errors.New("empty result from runtime")
	errEntryTooLarge   = errors.New("archive entry exceeds size limit")
	errArchiveTooLarge = errors.New("archive exceeds size limit")
)

const (
	// defaultFuncTimeout bounds a single invocation when the function declares none.
	defaultFuncTimeout = 30 * time.Second
	// maxUnzipBytes caps a single archive entry to guard against zip bombs.
	maxUnzipBytes = 64 << 20  // 64 MiB per entry
	maxUnzipTotal = 256 << 20 // 256 MiB total across the archive
	maxZipEntries = 4096
	dirPerm       = 0o755
	runnerPerm    = 0o644
)

// Subprocess is a config.FunctionEngine that runs a function's uploaded code in
// a real language runtime (Python or Node) as a child process — no Docker. The
// deployment package is unzipped to a temp directory per function; each Invoke
// spawns the interpreter with the event on stdin and reads the handler's return
// value back from a result file. Safe for concurrent use.
type Subprocess struct {
	mu    sync.Mutex
	funcs map[string]*deployedFunc
}

type deployedFunc struct {
	dir        string
	rt         runtime
	handler    string // file.function, or a bare entrypoint for the http framework
	framework  string // "" event contract, "http" functions-framework request/response
	runnerPath string // bootstrap script, written once at Deploy
	env        map[string]string
	timeout    time.Duration
}

// NewSubprocess returns a Subprocess function engine. Wire it in with
// config.WithFunctionEngine(realengine.NewSubprocess()).
func NewSubprocess() *Subprocess {
	return &Subprocess{funcs: map[string]*deployedFunc{}}
}

// Deploy unzips fn.Code and records how to run it. Re-deploying a name replaces
// the previous deployment.
//
//nolint:gocritic // fn is the by-value DTO defined by the FunctionEngine contract
func (s *Subprocess) Deploy(_ context.Context, fn config.FunctionDeployment) error {
	rt, err := runtimeFor(fn.Runtime, fn.Framework)
	if err != nil {
		return err
	}

	dir, err := os.MkdirTemp("", "cloudemu-fn-")
	if err != nil {
		return fmt.Errorf("create function dir: %w", err)
	}

	if err := unzip(fn.Code, dir); err != nil {
		_ = os.RemoveAll(dir)

		return fmt.Errorf("unzip deployment package: %w", err)
	}

	// Write the bootstrap runner once now, not on every Invoke: its content is
	// invariant for the deployment, so per-invoke rewrites are wasted I/O and a
	// latent race between concurrent invocations.
	runnerPath := filepath.Join(dir, rt.runnerName)
	if err := os.WriteFile(runnerPath, []byte(rt.runnerSrc), runnerPerm); err != nil {
		_ = os.RemoveAll(dir)

		return fmt.Errorf("write runner: %w", err)
	}

	timeout := defaultFuncTimeout
	if fn.Timeout > 0 {
		timeout = time.Duration(fn.Timeout) * time.Second
	}

	s.mu.Lock()
	if old, ok := s.funcs[fn.Name]; ok {
		_ = os.RemoveAll(old.dir)
	}

	s.funcs[fn.Name] = &deployedFunc{
		dir: dir, rt: rt, handler: fn.Handler, framework: fn.Framework,
		runnerPath: runnerPath, env: fn.Env, timeout: timeout,
	}
	s.mu.Unlock()

	return nil
}

// Invoke runs the function with event on stdin and returns its result.
func (s *Subprocess) Invoke(ctx context.Context, name string, event []byte) (config.FunctionResult, error) {
	s.mu.Lock()
	fn, ok := s.funcs[name]
	s.mu.Unlock()

	if !ok {
		return config.FunctionResult{}, fmt.Errorf("%q: %w", name, errNotDeployed)
	}

	return fn.run(ctx, event)
}

// Remove deletes the deployment backing name. No-op if unknown.
func (s *Subprocess) Remove(_ context.Context, name string) error {
	s.mu.Lock()
	fn, ok := s.funcs[name]
	delete(s.funcs, name)
	s.mu.Unlock()

	if ok {
		_ = os.RemoveAll(fn.dir)
	}

	return nil
}

// Close removes every deployment directory. Safe to call more than once.
func (s *Subprocess) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, fn := range s.funcs {
		_ = os.RemoveAll(fn.dir)
	}

	s.funcs = map[string]*deployedFunc{}

	return nil
}

// runResult is the JSON the runner scripts write to the result file.
type runResult struct {
	Error   string `json:"error"`
	Payload string `json:"payload"`
}

func (fn *deployedFunc) run(ctx context.Context, event []byte) (config.FunctionResult, error) {
	file, function, err := fn.resolveHandler()
	if err != nil {
		return config.FunctionResult{}, err
	}

	resultFile, err := os.CreateTemp(fn.dir, "result-*.json")
	if err != nil {
		return config.FunctionResult{}, fmt.Errorf("create result file: %w", err)
	}

	resultPath := resultFile.Name()
	_ = resultFile.Close()

	defer func() { _ = os.Remove(resultPath) }()

	runCtx, cancel := context.WithTimeout(ctx, fn.timeout)
	defer cancel()

	handlerFile := filepath.Join(fn.dir, file+fn.rt.ext)
	cmd, logs := fn.buildCmd(runCtx, fn.runnerPath, handlerFile, function, resultPath, event)
	runErr := cmd.Run()

	res, readErr := readRunResult(resultPath)
	if readErr != nil {
		// No result file means the runtime itself failed (bad interpreter, crash
		// before writing) — surface the captured logs so the cause is visible.
		return config.FunctionResult{Logs: logs.String()},
			fmt.Errorf("runtime error: %w: %s", firstErr(runErr, readErr), strings.TrimSpace(logs.String()))
	}

	return config.FunctionResult{
		Payload:       []byte(res.Payload),
		Logs:          logs.String(),
		FunctionError: res.Error,
	}, nil
}

// buildCmd assembles the interpreter command that runs the bootstrap runner: the
// event on stdin, the handler coordinates and result-file path in the
// environment, and the function's own env vars layered on top. The returned
// buffer captures the invocation's stdout+stderr as logs.
func (fn *deployedFunc) buildCmd(
	ctx context.Context, runnerPath, handlerFile, function, resultPath string, event []byte,
) (*exec.Cmd, *bytes.Buffer) {
	//nolint:gosec // interpreter + first-party runner script; user code is data it loads, not the command
	cmd := exec.CommandContext(ctx, fn.rt.interpreter, runnerPath)
	cmd.Dir = fn.dir
	cmd.Stdin = bytes.NewReader(event)

	logs := &bytes.Buffer{}
	cmd.Stdout = logs
	cmd.Stderr = logs

	env := append(os.Environ(),
		"_CLOUDEMU_HANDLER_FILE="+handlerFile,
		"_CLOUDEMU_HANDLER_FUNC="+function,
		"_CLOUDEMU_RESULT_FILE="+resultPath,
	)
	for k, v := range fn.env {
		env = append(env, k+"="+v)
	}

	cmd.Env = env

	return cmd, logs
}

func readRunResult(path string) (runResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return runResult{}, err
	}

	if len(data) == 0 {
		return runResult{}, errEmptyResult
	}

	var res runResult
	if err := json.Unmarshal(data, &res); err != nil {
		return runResult{}, err
	}

	return res, nil
}

// resolveHandler determines the source file and function name to run. Under the
// http framework a bare (dotless) entrypoint — the Cloud Functions gen1
// convention, e.g. "hello_http" — resolves against the runtime's default source
// file (main.py / index.js). Everything else keeps the "file.function"
// convention used by the event contract.
func (fn *deployedFunc) resolveHandler() (file, function string, err error) {
	if fn.framework == frameworkHTTP && !strings.Contains(fn.handler, ".") {
		return fn.rt.defaultFile, fn.handler, nil
	}

	return splitHandler(fn.handler)
}

// splitHandler splits "file.function" into its file (path, no extension) and
// function parts on the last dot.
func splitHandler(handler string) (file, function string, err error) {
	i := strings.LastIndex(handler, ".")
	if i <= 0 || i == len(handler)-1 {
		return "", "", fmt.Errorf("%q: %w", handler, errBadHandler)
	}

	return handler[:i], handler[i+1:], nil
}

func firstErr(a, b error) error {
	if a != nil {
		return a
	}

	return b
}

func unzip(archive []byte, dir string) error {
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return err
	}

	if len(zr.File) > maxZipEntries {
		return fmt.Errorf("%w: %d entries", errArchiveTooLarge, len(zr.File))
	}

	var total int64

	for _, f := range zr.File {
		n, err := extractZipEntry(f, dir)
		if err != nil {
			return err
		}

		total += n
		if total > maxUnzipTotal {
			return errArchiveTooLarge
		}
	}

	return nil
}

// extractZipEntry writes one archive entry under dir and returns the bytes
// written, guarding against path traversal and oversize entries.
func extractZipEntry(f *zip.File, dir string) (int64, error) {
	// Guard against path traversal (zip slip): the target must stay under dir.
	target := filepath.Join(dir, f.Name) //nolint:gosec // validated against dir below
	if !strings.HasPrefix(target, filepath.Clean(dir)+string(os.PathSeparator)) && target != filepath.Clean(dir) {
		return 0, fmt.Errorf("%q: %w", f.Name, errZipSlip)
	}

	if f.FileInfo().IsDir() {
		return 0, os.MkdirAll(target, dirPerm)
	}

	if err := os.MkdirAll(filepath.Dir(target), dirPerm); err != nil {
		return 0, err
	}

	rc, err := f.Open()
	if err != nil {
		return 0, err
	}
	defer rc.Close()

	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, dirPerm)
	if err != nil {
		return 0, err
	}
	defer out.Close()

	// Copy one byte past the per-entry cap so an oversize entry is detected, not
	// silently truncated: io.CopyN returns a nil error at exactly N bytes even
	// when the source has more remaining.
	n, err := io.CopyN(out, rc, maxUnzipBytes+1)
	if err != nil && !errors.Is(err, io.EOF) {
		return 0, err
	}

	if n > maxUnzipBytes {
		return 0, fmt.Errorf("%q: %w", f.Name, errEntryTooLarge)
	}

	return n, nil
}
