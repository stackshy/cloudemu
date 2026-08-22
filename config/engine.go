package config

import "context"

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
