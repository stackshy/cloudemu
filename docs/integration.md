# Integrating CloudEmu into Your App

To use CloudEmu with an existing service, you change one thing: the endpoint on the SDK client your app already builds. Run CloudEmu in server mode and set the endpoint with `AWS_ENDPOINT_URL` / `o.BaseEndpoint`, `option.WithEndpoint`, or the Azure ARM endpoint override. Your app's normal code path then runs against the in-memory cloud, with no mocks in your code.

Use server mode for integration and E2E tests. The in-process library mode at the bottom of this page is for Go unit tests.

> If you want that path to run real SQL, Redis or function code instead of the in-memory backend, back the driver with a [real engine](features.md#11-real-data-plane-engines-opt-in). You enable it with a `config.With<X>Engine` option (or the `cloudemu-server` flags). Nothing else on this page changes.

## 1. Run the server

```sh
docker run --rm -p 4566:4566 -p 4568:4568 -p 4569:4569 \
  ghcr.io/stackshy/cloudemu:latest   # Apple Silicon: add --platform linux/amd64 if needed
```

It prints the endpoints: AWS `http://127.0.0.1:4566` (HTTP), Azure `https://127.0.0.1:4568` (HTTPS, self-signed), GCP `http://127.0.0.1:4569` (HTTP). Flags, ports and TLS are covered in [standalone-server.md](standalone-server.md).

## 2. Override the endpoint (per SDK)

These snippets are ordinary client setup. Copy the one for your SDK.

### AWS: `aws-sdk-go-v2`

There are two ways. The per-client option works on every SDK version:

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

Watch out: `LoadDefaultConfig` only reads `AWS_ENDPOINT_URL` on recent `aws-sdk-go-v2` releases (`config` v1.27+ / SDK 2023-12 or newer). Older versions ignore the variable, and requests go to real AWS without any error. Use a current version, or set `o.BaseEndpoint` on each client. For S3, set `o.UsePathStyle = true` either way.

### AWS: boto3 / Python

```python
import boto3
s3 = boto3.client("s3", endpoint_url="http://127.0.0.1:4566",
                  aws_access_key_id="test", aws_secret_access_key="test",
                  region_name="us-east-1")
```

The AWS CLI takes the same override as `--endpoint-url http://127.0.0.1:4566` (or `AWS_ENDPOINT_URL`). Any credentials are accepted, because CloudEmu does not validate signatures.

### GCP: `cloud.google.com/go`

```go
client, _ := storage.NewClient(ctx,
	option.WithEndpoint("http://127.0.0.1:4569"),
	option.WithoutAuthentication())
```

`WithoutAuthentication()` is required. Without it the SDK tries to fetch real credentials.

### Azure: `azure-sdk-for-go`

Azure uses HTTPS with a self-signed cert. Override the ARM endpoint through a `cloud.Configuration`, and either trust the cert or skip verification for local dev:

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

Any `azcore.TokenCredential` works, since tokens are not validated. See [standalone-server.md](standalone-server.md#trusting-the-azure-self-signed-cert-any-language) for trusting the cert per language.

## 3. Make the endpoint injectable

In production the override is unset and the client talks to the real cloud. In dev and CI it points at CloudEmu. An environment variable is the easiest way to do this, but a config field or setting it directly works too. The rest of your code stays the same.

```go
// Where your app builds its client:
return s3.NewFromConfig(cfg, func(o *s3.Options) {
	if ep := os.Getenv("CLOUDEMU_ENDPOINT"); ep != "" {
		o.BaseEndpoint = aws.String(ep) // dev/CI → CloudEmu; unset → real AWS
		o.UsePathStyle = true
	}
})
```

Set `CLOUDEMU_ENDPOINT` (or `AWS_ENDPOINT_URL`) to the running server's address and your service will use the in-memory backend. Reset state between runs with `curl -X POST http://127.0.0.1:4566/_cloudemu/reset`.

## In-process / library mode (Go unit tests only)

For Go unit tests you can skip the server and run CloudEmu in-process. Don't import CloudEmu from production code.

```go
// In a _test.go file:
func startCloudEmu(t *testing.T) {
	srv := awsserver.New(awsserver.Drivers{S3: cloudemu.NewAWS().S3})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	t.Setenv("CLOUDEMU_ENDPOINT", ts.URL) // same injectable knob as above
}
```

The endpoint setting is the same as in server mode (AWS `o.BaseEndpoint`, GCP `option.WithEndpoint`, Azure `arm.ClientOptions` cloud endpoint, Databricks `Config.Host`). The only difference is that the URL comes from `httptest` instead of a running server.

## Tell your AI agent (paste into your repo's `AGENTS.md`)

```markdown
CloudEmu (github.com/stackshy/cloudemu/v2) is an in-memory cloud emulator. To integrate it
into an existing service, run it in SERVER mode and set the SDK endpoint on your running app:
AWS AWS_ENDPOINT_URL / o.BaseEndpoint (+ UsePathStyle for S3),
GCP option.WithEndpoint + option.WithoutAuthentication, Azure arm.ClientOptions ResourceManager
endpoint, Databricks Config.Host. Make that endpoint injectable however this codebase prefers
(env var, config field, or set in the test) so production keeps the real cloud, code unchanged.
Server mode is the default for integration/E2E; in-process httptest.NewServer is ONLY for Go unit
tests inside CloudEmu-aware code. Never import CloudEmu from production code.
See https://github.com/stackshy/cloudemu/blob/development/docs/integration.md
```
