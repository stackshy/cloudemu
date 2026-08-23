package config

import (
	"context"
	"io"
)

// EngineClosers returns the engines wired into these Options that implement
// io.Closer, so a provider's Close can cascade teardown to them (stopping any
// real Docker containers or subprocesses they own). Engines left at their nil
// default, or that don't implement io.Closer, are skipped — so the result is
// empty for the in-memory default. Engine Close implementations are idempotent,
// so an engine wired into more than one slot closing twice is harmless.
func (o *Options) EngineClosers() []io.Closer {
	engines := []any{o.DatabaseEngine, o.CacheEngine, o.FunctionEngine, o.ComputeEngine, o.ContainerEngine, o.StorageEngine}

	var closers []io.Closer

	for _, e := range engines {
		if c, ok := e.(io.Closer); ok {
			closers = append(closers, c)
		}
	}

	return closers
}

// DatabaseEngine optionally backs relational-database instances with a real
// engine (e.g. a real Postgres) so that clients can run actual SQL against the
// emulator. When nil — the default — instances use synthetic endpoints and no
// real database runs, keeping the emulator in-memory and dependency-free.
//
// Implementations live outside the core module (contrib/) so the core carries
// no database-engine dependency; this is a pluggable capability like Clock.
type DatabaseEngine interface {
	// Provision starts or creates a real database for the instance and returns
	// the host and port a client connects to. It must make the instance's
	// master credentials usable so a caller can connect with them.
	Provision(ctx context.Context, req ProvisionRequest) (ProvisionResult, error)

	// Deprovision tears down the real database backing the instance. It is a
	// no-op if the instance was never provisioned.
	Deprovision(ctx context.Context, instanceID string) error
}

// ProvisionRequest describes the database a DatabaseEngine should back.
type ProvisionRequest struct {
	InstanceID string
	Engine     string // "postgres", "mysql", …
	DBName     string // optional initial database name
	Username   string // master username the caller will connect with
	Password   string // master password the caller will connect with
}

// ProvisionResult is the reachable address of the provisioned database.
type ProvisionResult struct {
	Host string
	Port int
}

// CacheEngine optionally backs cache instances with a real cache server (e.g. a
// real Redis) so clients can run real commands against the emulator. When nil —
// the default — caches keep a synthetic endpoint and no real server runs.
//
// Like DatabaseEngine, implementations live outside the core module (contrib/).
type CacheEngine interface {
	// Provision starts a real cache server for the instance and returns the
	// host and port a client connects to.
	Provision(ctx context.Context, req CacheProvisionRequest) (ProvisionResult, error)

	// Deprovision tears down the real cache server backing the instance. It is a
	// no-op if the instance was never provisioned.
	Deprovision(ctx context.Context, cacheID string) error
}

// CacheProvisionRequest describes the cache a CacheEngine should back.
type CacheProvisionRequest struct {
	CacheID string
	Engine  string // "redis", "memcached"
}

// FunctionEngine optionally executes real function code (e.g. a real Python or
// Node runtime) so clients can Invoke a function and get the result of the
// uploaded handler actually running — not a stubbed echo. When nil — the
// default — functions return a successful stub payload and no code runs,
// keeping the emulator in-memory and dependency-free.
//
// Unlike DatabaseEngine/CacheEngine (which hand back a host:port a client
// dials), a function is invoked through CloudEmu itself, so the engine is an
// invoke transport: Deploy the code once, Invoke it per request, Remove it on
// delete. Implementations live outside the core module (contrib/).
type FunctionEngine interface {
	// Deploy makes a function's code runnable. It is called on create (and on
	// code updates) and may be called again to replace an existing deployment.
	Deploy(ctx context.Context, fn FunctionDeployment) error

	// Invoke runs the named function with the event payload and returns its
	// result. A handler that raises is reported via FunctionResult.FunctionError
	// (not a Go error); a Go error is reserved for the engine failing to run it.
	Invoke(ctx context.Context, name string, event []byte) (FunctionResult, error)

	// Remove tears down the deployment backing the function. It is a no-op if
	// the function was never deployed.
	Remove(ctx context.Context, name string) error
}

// FunctionDeployment describes the code and runtime contract a FunctionEngine
// must be able to execute.
type FunctionDeployment struct {
	Name    string            // function name (the Invoke/Remove key)
	Runtime string            // e.g. "python3.12", "nodejs20.x"
	Handler string            // e.g. "main.handler" (file.function)
	Code    []byte            // deployment package (a .zip archive)
	Env     map[string]string // environment variables exposed to the handler
	Timeout int               // max seconds; 0 = engine default
	// Framework selects the invocation contract the engine runs the handler
	// under. "" (the default) is the event contract fn(event, context)→JSON
	// used by AWS Lambda and Azure Functions; "http" is the
	// functions-framework request/response contract fn(request)→body used by
	// GCP Cloud Functions gen1, where the handler receives a Flask-Request-like
	// object and returns a Flask-coercible value.
	Framework string
}

// FunctionResult is the outcome of a real function invocation.
type FunctionResult struct {
	Payload       []byte // the JSON the handler returned
	Logs          string // stdout/stderr the invocation produced
	FunctionError string // non-empty if the handler raised; mirrors X-Amz-Function-Error
}

// ComputeEngine optionally backs virtual-machine instances with a real backing
// (e.g. a real container acting as the guest) so clients can boot an instance
// that actually runs its boot script and exposes console output. When nil — the
// default — instances use synthetic state and no real backing runs, keeping the
// emulator in-memory and dependency-free.
//
// Like DatabaseEngine, implementations live outside the core module (contrib/)
// so the core carries no compute-backing dependency; this is a pluggable
// capability like Clock.
type ComputeEngine interface {
	// Provision starts a backing container for the instance and runs the boot
	// script once. The returned result may carry a reachable IP; an empty IP is
	// acceptable when the backing surfaces none.
	Provision(ctx context.Context, req ComputeProvisionRequest) (ComputeProvisionResult, error)

	// ConsoleOutput returns the accumulated stdout/stderr the boot script
	// produced — the console-output analog. It is empty for an instance that was
	// never provisioned.
	ConsoleOutput(ctx context.Context, instanceID string) ([]byte, error)

	// Deprovision tears down the real backing for the instance. It is a no-op if
	// the instance was never provisioned.
	Deprovision(ctx context.Context, instanceID string) error
}

// ComputeProvisionRequest describes the VM a ComputeEngine should back.
type ComputeProvisionRequest struct {
	InstanceID string
	ImageID    string
	BootScript []byte            // user-data/boot script run once at provision
	Env        map[string]string // environment exposed to the boot script
}

// ComputeProvisionResult is the outcome of backing a VM. IP is optional; an
// empty value is fine when the backing surfaces no reachable address.
type ComputeProvisionResult struct {
	IP string
}

// ContainerEngine optionally backs container workloads (ECS tasks, Kubernetes
// pods, Azure Container Instances) with real containers so clients can observe
// real logs, exit codes, and exec results. When nil — the default — workloads
// use synthetic state and no real container runs, keeping the emulator
// in-memory and dependency-free.
//
// It is deliberately separate from ComputeEngine: containers are observed via
// logs and exit codes rather than a host:port, a single workload may run
// multiple containers, and a workload may run to completion instead of staying
// up. Like DatabaseEngine, implementations live outside the core module
// (contrib/).
type ContainerEngine interface {
	// Run starts the workload's containers and returns an opaque handle used by
	// the other methods. When spec.RunToCompletion is set it blocks until the
	// containers exit; otherwise it starts them detached and returns immediately.
	Run(ctx context.Context, spec ContainerRunSpec) (handle string, err error)

	// Status reports the current state and exit code of each container in the
	// workload identified by handle.
	Status(ctx context.Context, handle string) ([]ContainerStatus, error)

	// Logs returns the accumulated stdout/stderr for one named container in the
	// workload. A non-positive tailLines returns the full log.
	Logs(ctx context.Context, handle, container string, tailLines int) (string, error)

	// Exec runs a command inside one named container and returns its output and
	// exit code. A Go error is reserved for the engine failing to run the command.
	Exec(ctx context.Context, handle, container string, cmd []string) (ExecResult, error)

	// Stop tears down the workload's containers. It is a no-op if the workload
	// was never run.
	Stop(ctx context.Context, handle string) error
}

// ContainerRunSpec describes a workload a ContainerEngine should run.
type ContainerRunSpec struct {
	Name            string          // workload name (informational)
	Containers      []ContainerSpec // one or more containers to run together
	RunToCompletion bool            // block until exit when set; else detached
}

// ContainerSpec describes a single container within a workload.
type ContainerSpec struct {
	Name    string
	Image   string
	Command []string
	Env     map[string]string
}

// ContainerStatus is the observed state of one container in a workload.
type ContainerStatus struct {
	Name     string
	State    string // e.g. "running", "exited"
	ExitCode int
}

// ExecResult is the outcome of a command run inside a container.
type ExecResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// StorageEngine optionally persists object-storage bytes to a real backing
// (e.g. a real filesystem or a MinIO server) so objects survive process
// restart and can be inspected by external tools. When nil — the default —
// object bytes live only in-memory. Unlike DatabaseEngine/CacheEngine (which
// hand back a host:port a client dials), objects are always served through the
// emulator itself, so the engine is a byte store: Put on write, Get on read,
// Delete/Copy to mirror the object lifecycle. The in-memory Mock keeps the
// object's metadata (ETag, versioning, tags, multipart state); the engine holds
// only the bytes. Implementations live outside the core module (contrib/) so
// the core carries no storage-backing dependency — a pluggable capability.
type StorageEngine interface {
	// Put stores the object's bytes. Called on write (PutObject, CopyObject,
	// CompleteMultipartUpload, and each stored version).
	Put(ctx context.Context, obj StorageObject) error

	// Get returns the object's bytes. It reports a not-found error when the
	// reference is absent (the caller has already validated metadata).
	Get(ctx context.Context, ref StorageRef) (StorageObject, error)

	// Delete removes the object's bytes. It is a no-op when the reference is
	// absent, matching idempotent object deletion.
	Delete(ctx context.Context, ref StorageRef) error

	// Copy duplicates src's bytes to dst server-side (the CopyObject analog).
	Copy(ctx context.Context, dst, src StorageRef) error
}

// StorageObject is an object's bytes plus the metadata a backing may persist
// alongside them. Version is empty for the current (unversioned) object.
type StorageObject struct {
	Bucket      string
	Key         string
	Version     string
	Data        []byte
	ContentType string
	Metadata    map[string]string
}

// StorageRef identifies one object (or object version) in the store.
type StorageRef struct {
	Bucket  string
	Key     string
	Version string
}
