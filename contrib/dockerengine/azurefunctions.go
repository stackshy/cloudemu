package dockerengine

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
)

const (
	// defaultAzureFunctionsImage is the official Azure Functions host image the
	// engine runs. It bundles the Functions 4.x host and the Python 3.11 worker,
	// so a real Python v2 (function_app.py) app is discovered and executed exactly
	// as it would be on Azure. Pin the major.minor so behavior does not drift.
	defaultAzureFunctionsImage = "mcr.microsoft.com/azure-functions/python:4-python3.11"
	// defaultFunctionsPlatform pins the container platform. The official
	// azure-functions images are published for linux/amd64 only (no arm64
	// manifest), so the engine requests amd64 explicitly — a no-op on an amd64
	// host, and emulated (Rosetta/qemu) on arm64 — so the same image runs
	// everywhere. Override with WithFunctionsPlatform for a differently-built image.
	defaultFunctionsPlatform = "linux/amd64"
	// funcNamePrefix namespaces every container this engine creates so they are
	// easy to spot (and clean up) on the host: `docker ps -a --filter
	// name=cloudemu-func-`.
	funcNamePrefix = "cloudemu-func-"
	// funcHostContainerPort is the port the Functions host serves HTTP on inside
	// the container; the engine publishes it to a docker-assigned host port.
	funcHostContainerPort = "80"
	// wwwroot is where the Functions host reads the app from. The extracted app
	// dir is bind-mounted here, so the host indexes the real app at startup.
	wwwroot = "/home/site/wwwroot"

	// funcReadyTimeout bounds how long Deploy waits for the host to index the app
	// and start answering the function's HTTP route (host boot + worker indexing
	// can take 20-40s on a cold image). funcPollInterval paces the readiness probe;
	// funcProbeTimeout bounds a single probe.
	funcReadyTimeout  = 120 * time.Second
	funcPollInterval  = 1 * time.Second
	funcProbeTimeout  = 5 * time.Second
	funcInvokeTimeout = 30 * time.Second

	// httpNotFound is the status the host returns while the route is not yet
	// indexed; httpServerErrorFloor is the first status treated as a handler
	// failure (Azure returns 500 + a plain-text body when a handler raises).
	httpNotFound         = http.StatusNotFound
	httpServerErrorFloor = http.StatusInternalServerError

	// extractDirPerm / extractFilePerm are the modes for dirs and files written
	// while unpacking the deployment zip into the (short-lived, per-deploy) temp
	// dir the container bind-mounts.
	extractDirPerm  = 0o750
	extractFilePerm = 0o600
)

// Sentinel errors (err113): wrapped at the return site so callers can match and
// the linter stays satisfied without dynamic error creation.
var (
	errFuncNotReady   = errors.New("dockerengine: azure functions host did not become ready in time")
	errFuncUnknown    = errors.New("dockerengine: unknown function")
	errFuncRun        = errors.New("dockerengine: azure functions container run failed")
	errFuncPort       = errors.New("dockerengine: could not read published host port")
	errFuncNoCode     = errors.New("dockerengine: function deployment carries no code")
	errZipExtract     = errors.New("dockerengine: could not extract function zip")
	errZipSlip        = errors.New("dockerengine: zip entry escapes the extraction root")
	errFuncInvokeHTTP = errors.New("dockerengine: invoking the function over HTTP failed")
)

// funcApp records the running container and published port backing one deployed
// function so Invoke can reach it and Remove/Close can tear down exactly what was
// made (container + extracted app dir).
type funcApp struct {
	containerID string
	port        int
	dir         string
}

// AzureFunctions is a config.FunctionEngine that runs a deployed function app's
// code inside a real Azure Functions host container (the official
// mcr.microsoft.com/azure-functions image). Deploy extracts the uploaded zip,
// starts a host container with the app bind-mounted in, and blocks until the host has
// indexed the app and answers the function's HTTP route; Invoke HTTP-POSTs the
// event to that route on the container and returns the real response. Each
// function gets its own container. Safe for concurrent use within a process.
type AzureFunctions struct {
	image    string
	platform string

	mu     sync.Mutex
	runner Runner
	apps   map[string]funcApp
	client *http.Client
}

// AzureFunctionsOption configures an AzureFunctions engine.
type AzureFunctionsOption func(*AzureFunctions)

// WithFunctionsImage overrides the Azure Functions host image (default
// mcr.microsoft.com/azure-functions/python:4-python3.11). Use it to run a
// different runtime/version of the official host image.
func WithFunctionsImage(image string) AzureFunctionsOption {
	return func(a *AzureFunctions) {
		if strings.TrimSpace(image) != "" {
			a.image = image
		}
	}
}

// WithFunctionsPlatform overrides the container platform (default linux/amd64,
// the only architecture the official host image publishes). Pass "" to let
// docker pick the host's native platform.
func WithFunctionsPlatform(platform string) AzureFunctionsOption {
	return func(a *AzureFunctions) {
		a.platform = platform
	}
}

// NewAzureFunctions returns an AzureFunctions engine. Containers are created on
// Deploy and torn down on Remove/Close; the docker CLI must be on PATH (gate with
// Available).
func NewAzureFunctions(opts ...AzureFunctionsOption) *AzureFunctions {
	a := &AzureFunctions{
		image:    defaultAzureFunctionsImage,
		platform: defaultFunctionsPlatform,
		apps:     map[string]funcApp{},
		client:   &http.Client{Timeout: funcInvokeTimeout},
	}
	for _, opt := range opts {
		opt(a)
	}

	return a
}

// Deploy makes a function's code runnable: it extracts the deployment zip to a
// temp dir, starts an Azure Functions host container with the app bind-mounted at
// wwwroot, and blocks until the host has indexed the app and answers the
// function's HTTP route. Re-deploying an existing function replaces its container.
//
//nolint:gocritic // fn is the by-value DTO defined by the FunctionEngine contract.
func (a *AzureFunctions) Deploy(ctx context.Context, fn config.FunctionDeployment) error {
	if len(fn.Code) == 0 {
		return errFuncNoCode
	}

	// Replace any prior deployment for this name before standing up the new one.
	if err := a.Remove(ctx, fn.Name); err != nil {
		return err
	}

	dir, err := extractZip(fn.Code)
	if err != nil {
		return err
	}

	app, err := a.startApp(ctx, fn, dir)
	if err != nil {
		_ = os.RemoveAll(dir)

		return err
	}

	if err := a.waitReady(ctx, fn.Name, app.port); err != nil {
		_ = a.runner.Rm(context.Background(), app.containerID)
		_ = os.RemoveAll(dir)

		return err
	}

	a.mu.Lock()
	a.apps[fn.Name] = app
	a.mu.Unlock()

	return nil
}

// Invoke HTTP-POSTs the event to the function's HTTP trigger on its container
// (http://127.0.0.1:<port>/api/<name>) and returns the response body. A handler
// that raises surfaces as an Azure 500 + plain-text body, which is mapped to
// FunctionResult.FunctionError (not a Go error); a Go error is reserved for the
// engine failing to reach the host.
func (a *AzureFunctions) Invoke(ctx context.Context, name string, event []byte) (config.FunctionResult, error) {
	a.mu.Lock()
	app, ok := a.apps[name]
	a.mu.Unlock()

	if !ok {
		return config.FunctionResult{}, fmt.Errorf("%q: %w", name, errFuncUnknown)
	}

	status, body, err := a.post(ctx, app.port, name, event)
	if err != nil {
		return config.FunctionResult{}, err
	}

	if status >= httpServerErrorFloor {
		return config.FunctionResult{FunctionError: strings.TrimSpace(string(body))}, nil
	}

	return config.FunctionResult{Payload: body}, nil
}

// Remove tears down the container and extracted app dir backing the function.
// No-op if the function was never deployed.
func (a *AzureFunctions) Remove(ctx context.Context, name string) error {
	a.mu.Lock()
	app, ok := a.apps[name]
	delete(a.apps, name)
	a.mu.Unlock()

	if !ok {
		return nil
	}

	err := a.runner.Rm(ctx, app.containerID)
	_ = os.RemoveAll(app.dir)

	return err
}

// Close force-removes every container this engine created and deletes their
// extracted app dirs. Safe to call more than once.
func (a *AzureFunctions) Close() error {
	a.mu.Lock()
	all := a.apps
	a.apps = map[string]funcApp{}
	a.mu.Unlock()

	for _, app := range all {
		_ = a.runner.Rm(context.Background(), app.containerID)
		_ = os.RemoveAll(app.dir)
	}

	return nil
}

// startApp runs the host container with the extracted app bind-mounted at wwwroot
// and its HTTP port published to a docker-assigned host port, then reads that
// port. The caller owns dir cleanup on any error.
//
//nolint:gocritic // fn is the by-value DTO defined by the FunctionEngine contract.
func (a *AzureFunctions) startApp(ctx context.Context, fn config.FunctionDeployment, dir string) (funcApp, error) {
	name := funcNamePrefix + sanitizeName(fn.Name)

	id, err := a.runContainer(ctx, name, dir, hostEnv(fn.Env))
	if err != nil {
		return funcApp{}, err
	}

	port, err := publishedPort(ctx, id)
	if err != nil {
		_ = a.runner.Rm(context.Background(), id)

		return funcApp{}, err
	}

	return funcApp{containerID: id, port: port, dir: dir}, nil
}

// runContainer runs `docker run -d [--platform P] -p 127.0.0.1::80 -v
// <dir>:<wwwroot> --name <n> -e K=V <image>` and returns the container id. The
// app is bind-mounted (not baked into the image) so the host indexes the real
// deployment at startup. Every argument is first-party (never a shell string);
// stdout (the id) and stderr (image-pull progress) are captured separately so a
// first-run pull can never corrupt the parsed id.
func (a *AzureFunctions) runContainer(ctx context.Context, name, dir string, env map[string]string) (string, error) {
	const fixed = 10 // run + -d + --platform P + -p portmap + -v mount + --name name + image

	mount := dir + ":" + wwwroot

	envs := envArgs(env)
	args := make([]string, 0, fixed+len(envs))
	args = append(args, "run", "-d")

	if a.platform != "" {
		args = append(args, "--platform", a.platform)
	}

	args = append(args, "-p", "127.0.0.1::"+funcHostContainerPort, "-v", mount, "--name", name)
	args = append(args, envs...)
	args = append(args, a.image)

	var stdout, stderr bytes.Buffer

	cmd := exec.CommandContext(ctx, dockerBinary, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%w: %s: %w", errFuncRun, strings.TrimSpace(stderr.String()), err)
	}

	return strings.TrimSpace(stdout.String()), nil
}

// publishedPort reads the host port docker mapped the container's HTTP port to
// (`docker port <id> 80/tcp` → "127.0.0.1:49154").
func publishedPort(ctx context.Context, id string) (int, error) {
	out, err := exec.CommandContext(ctx, dockerBinary, "port", id, funcHostContainerPort+"/tcp").Output()
	if err != nil {
		return 0, fmt.Errorf("%w: %w", errFuncPort, err)
	}

	// Output may carry multiple mappings (IPv4/IPv6), one per line; take the first.
	line := strings.TrimSpace(string(out))
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}

	idx := strings.LastIndexByte(line, ':')
	if idx < 0 {
		return 0, fmt.Errorf("%w: unexpected output %q", errFuncPort, string(out))
	}

	port, err := strconv.Atoi(strings.TrimSpace(line[idx+1:]))
	if err != nil {
		return 0, fmt.Errorf("%w: bad port %q", errFuncPort, line[idx+1:])
	}

	return port, nil
}

// waitReady polls the function's HTTP route until the host has indexed the app
// and stops replying 404 (a probe GET with no body may make an indexed handler
// return 500 — that still proves the route is live). It gives up after
// funcReadyTimeout.
func (a *AzureFunctions) waitReady(ctx context.Context, name string, port int) error {
	deadline := time.Now().Add(funcReadyTimeout)

	for time.Now().Before(deadline) {
		if a.routeLive(ctx, port, name) {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(funcPollInterval):
		}
	}

	return errFuncNotReady
}

// routeLive reports whether the function's HTTP route answers with anything other
// than 404 right now (connection refused / host not up yet counts as not-live).
func (a *AzureFunctions) routeLive(ctx context.Context, port int, name string) bool {
	probeCtx, cancel := context.WithTimeout(ctx, funcProbeTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, functionURL(port, name), http.NoBody)
	if err != nil {
		return false
	}

	resp, err := a.client.Do(req)
	if err != nil {
		return false
	}

	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	return resp.StatusCode != httpNotFound
}

// post sends the event to the function's HTTP route and returns the status and
// body.
func (a *AzureFunctions) post(ctx context.Context, port int, name string, event []byte) (status int, body []byte, err error) {
	postCtx, cancel := context.WithTimeout(ctx, funcInvokeTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(postCtx, http.MethodPost, functionURL(port, name), bytes.NewReader(event))
	if err != nil {
		return 0, nil, fmt.Errorf("%w: %w", errFuncInvokeHTTP, err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := a.client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("%w: %w", errFuncInvokeHTTP, err)
	}
	defer resp.Body.Close()

	body, err = io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, fmt.Errorf("%w: %w", errFuncInvokeHTTP, err)
	}

	return resp.StatusCode, body, nil
}

// functionURL builds the HTTP-trigger URL for a function. The default route of an
// Azure Functions HTTP trigger is the function name, so the invoke key doubles as
// the route.
func functionURL(port int, name string) string {
	return fmt.Sprintf("http://127.0.0.1:%d/api/%s", port, name)
}

// hostEnv merges the host settings the Functions host needs to run an HTTP-only
// app without an Azure Storage account with the caller's function env (the
// caller's values win on a key collision).
func hostEnv(userEnv map[string]string) map[string]string {
	env := map[string]string{
		"AzureWebJobsScriptRoot":                             wwwroot,
		"AzureFunctionsJobHost__Logging__Console__IsEnabled": "true",
		// The Python v2 (function_app.py) model is discovered by the worker; older
		// host builds only enable that behind this feature flag, so set it.
		"AzureWebJobsFeatureFlags": "EnableWorkerIndexing",
		"FUNCTIONS_WORKER_RUNTIME": "python",
		// HTTP-only app: no real storage account. Empty storage + file-backed secret
		// storage lets the host serve anonymous HTTP triggers without one.
		"AzureWebJobsStorage":           "",
		"AzureWebJobsSecretStorageType": "files",
		// The host image is amd64-only. Under qemu emulation (an arm64 host without
		// Docker's Rosetta) the .NET host SIGABRTs in its JIT unless W^X is
		// disabled; this flag is the documented fix and is a harmless no-op on a
		// native amd64 host, so it is set unconditionally.
		"DOTNET_EnableWriteXorExecute": "0",
	}

	for k, v := range userEnv {
		env[k] = v
	}

	return env
}

// extractZip writes the deployment zip to a fresh temp dir and returns its path.
// It rejects any entry whose path escapes the extraction root (zip-slip).
func extractZip(code []byte) (string, error) {
	zr, err := zip.NewReader(bytes.NewReader(code), int64(len(code)))
	if err != nil {
		return "", fmt.Errorf("%w: %w", errZipExtract, err)
	}

	dir, err := os.MkdirTemp("", "cloudemu-func-")
	if err != nil {
		return "", fmt.Errorf("%w: %w", errZipExtract, err)
	}

	for _, f := range zr.File {
		if err := extractOne(dir, f); err != nil {
			_ = os.RemoveAll(dir)

			return "", err
		}
	}

	return dir, nil
}

// extractOne writes a single zip entry under dir, rejecting path traversal.
func extractOne(dir string, f *zip.File) error {
	target := filepath.Join(dir, f.Name) //nolint:gosec // guarded against traversal just below

	if !strings.HasPrefix(target, filepath.Clean(dir)+string(os.PathSeparator)) && target != filepath.Clean(dir) {
		return fmt.Errorf("%q: %w", f.Name, errZipSlip)
	}

	if f.FileInfo().IsDir() {
		return os.MkdirAll(target, extractDirPerm)
	}

	if err := os.MkdirAll(filepath.Dir(target), extractDirPerm); err != nil {
		return fmt.Errorf("%w: %w", errZipExtract, err)
	}

	rc, err := f.Open()
	if err != nil {
		return fmt.Errorf("%w: %w", errZipExtract, err)
	}
	defer rc.Close()

	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, extractFilePerm)
	if err != nil {
		return fmt.Errorf("%w: %w", errZipExtract, err)
	}
	defer out.Close()

	//nolint:gosec // deployment zips are first-party test/app fixtures, not hostile input
	if _, err := io.Copy(out, rc); err != nil {
		return fmt.Errorf("%w: %w", errZipExtract, err)
	}

	return nil
}

// staticFunctionEngineCheck asserts AzureFunctions satisfies the
// config.FunctionEngine contract at compile time.
var _ config.FunctionEngine = (*AzureFunctions)(nil)
