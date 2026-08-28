# Integrating CloudEmu into Your App

**Integrating CloudEmu into an existing service is one thing: an endpoint override on the client your app already builds.** Run CloudEmu in server mode, then point your running app/services at it by setting the SDK endpoint — `AWS_ENDPOINT_URL` / `o.BaseEndpoint`, `option.WithEndpoint`, or the Azure ARM endpoint override. Your real code runs the live path end-to-end against an in-memory cloud, no mocks.

The mistake to avoid: **don't write a new `main.go` or `_test.go` that just spins CloudEmu up in-process and prints "it works."** That exercises the library, not your app. For integration and E2E, run the server and override the endpoint. In-process/library mode (bottom of this page) is only for Go unit tests you write inside CloudEmu-aware code.

> Need the real path to exercise real SQL, Redis or function code rather than the in-memory backend? Back the relevant driver with a [real engine](features.md#11-real-data-plane-engines-opt-in) — the wiring is a `config.With<X>Engine` option (or the `cloudemu-server` flags), everything below stays the same.

## 1. Run the server

```sh
docker run --rm -p 4566:4566 -p 4568:4568 -p 4569:4569 \
  ghcr.io/stackshy/cloudemu:latest   # Apple Silicon: add --platform linux/amd64 if needed
```

Prints the live endpoints — AWS `http://127.0.0.1:4566` (HTTP), Azure `https://127.0.0.1:4568` (HTTPS, self-signed), GCP `http://127.0.0.1:4569` (HTTP). Full flags, ports, and TLS: [standalone-server.md](standalone-server.md).

## 2. Override the endpoint (per SDK)

Copy the seam your SDK uses verbatim — this is real client wiring, not a mock.

### AWS — `aws-sdk-go-v2`

Two paths. The per-client option always works:

```go
client := s3.NewFromConfig(cfg, func(o *s3.Options) {
	o.BaseEndpoint = aws.String("http://127.0.0.1:4566")
	o.UsePathStyle = true // S3 needs path-style against a local endpoint
})
```

Or let `LoadDefaultConfig` read the endpoint from the environment:

```go
// AWS_ENDPOINT_URL=http://127.0.0.1:4566 in the environment
cfg, _ := config.LoadDefaultConfig(ctx)
client := s3.NewFromConfig(cfg) // still set o.UsePathStyle=true for S3
```

**Gotcha:** `AWS_ENDPOINT_URL` is only honored by `LoadDefaultConfig` on recent `aws-sdk-go-v2` releases (`config` v1.27+ / SDK 2023-12 or newer). On older versions the env var is ignored and requests silently go to real AWS — pin a current version, or set `o.BaseEndpoint` explicitly per client, which works on every version. For S3, remember `o.UsePathStyle = true` regardless of path.

### AWS — boto3 / Python

```python
import boto3
s3 = boto3.client("s3", endpoint_url="http://127.0.0.1:4566",
                  aws_access_key_id="test", aws_secret_access_key="test",
                  region_name="us-east-1")
```

The AWS CLI takes the same override as `--endpoint-url http://127.0.0.1:4566` (or `AWS_ENDPOINT_URL`). Any credentials are accepted — CloudEmu does not validate signatures.

### GCP — `cloud.google.com/go`

```go
client, _ := storage.NewClient(ctx,
	option.WithEndpoint("http://127.0.0.1:4569"),
	option.WithoutAuthentication())
```

`WithoutAuthentication()` is required — it stops the SDK from reaching out for real credentials.

### Azure — `azure-sdk-for-go`

Azure is HTTPS with a self-signed cert. Override the ARM endpoint through a `cloud.Configuration`, and either trust the cert or skip verification for local dev:

```go
cloudCfg := cloud.Configuration{
	Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
		cloud.ResourceManager: {
			Endpoint: "https://127.0.0.1:4568",
			Audience: "https://management.azure.com",
		},
	},
}
opts := &arm.ClientOptions{ClientOptions: azcore.ClientOptions{Cloud: cloudCfg}}
```

Any `azcore.TokenCredential` works — tokens are not validated. See [standalone-server.md](standalone-server.md#trusting-the-azure-self-signed-cert-any-language) for trusting the cert per language.

## 3. Make the endpoint injectable

In production the override is absent and the client hits the real cloud; in dev/CI it points at CloudEmu. An env var is the easy default — but it's your call: a config field or setting it directly works just as well. Your code doesn't change.

```go
// Where your app builds its client:
return s3.NewFromConfig(cfg, func(o *s3.Options) {
	if ep := os.Getenv("CLOUDEMU_ENDPOINT"); ep != "" {
		o.BaseEndpoint = aws.String(ep) // dev/CI → CloudEmu; unset → real AWS
		o.UsePathStyle = true
	}
})
```

Point `CLOUDEMU_ENDPOINT` (or `AWS_ENDPOINT_URL`) at the running server, and your real service exercises the in-memory backend end-to-end. Reset between runs with `curl -X POST http://127.0.0.1:4566/_cloudemu/reset`.

## In-process / library mode — Go unit tests only

Only for Go unit tests written inside CloudEmu-aware code: skip the server and run it in-process. Never import CloudEmu from production code.

```go
// In a _test.go file:
func startCloudEmu(t *testing.T) {
	srv := awsserver.New(awsserver.Drivers{S3: cloudemu.NewAWS().S3})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	t.Setenv("CLOUDEMU_ENDPOINT", ts.URL) // same injectable knob as above
}
```

The endpoint knob is identical to server mode — AWS `o.BaseEndpoint`, GCP `option.WithEndpoint`, Azure `arm.ClientOptions` cloud endpoint, Databricks `Config.Host` — only the URL now comes from `httptest` instead of the running server.

## Tell your AI agent (paste into your repo's `AGENTS.md`)

```markdown
CloudEmu (github.com/stackshy/cloudemu/v2) is an in-memory cloud emulator. To integrate it
into an existing service, run it in SERVER mode and set the SDK endpoint on your running app —
NOT a new main.go or _test.go: AWS AWS_ENDPOINT_URL / o.BaseEndpoint (+ UsePathStyle for S3),
GCP option.WithEndpoint + option.WithoutAuthentication, Azure arm.ClientOptions ResourceManager
endpoint, Databricks Config.Host. Make that endpoint injectable however this codebase prefers
(env var, config field, or set in the test) so production keeps the real cloud, code unchanged.
Server mode is the default for integration/E2E; in-process httptest.NewServer is ONLY for Go unit
tests inside CloudEmu-aware code. Never import CloudEmu from production code.
See https://github.com/stackshy/cloudemu/blob/development/docs/integration.md
```
