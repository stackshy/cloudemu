# Integrating CloudEmu into Your App

The mistake to avoid: **don't write a new `main.go` that just calls CloudEmu and prints "it works."** That tests the library, not your app.

Do this instead — point your app's *existing* cloud client at CloudEmu in your *existing* tests. Your real code runs against an in-memory cloud, no mocks.

## The trick

Read the endpoint from an env var. Tests set it to a CloudEmu server; production leaves it unset and talks to the real cloud. Your code doesn't change.

```go
// Where your app builds its client:
return s3.NewFromConfig(cfg, func(o *s3.Options) {
	if ep := os.Getenv("CLOUDEMU_ENDPOINT"); ep != "" {
		o.BaseEndpoint = aws.String(ep) // tests → CloudEmu; unset → real AWS
		o.UsePathStyle = true
	}
})
```

```go
// In a _test.go file:
func startCloudEmu(t *testing.T) {
	srv := awsserver.New(awsserver.Drivers{S3: cloudemu.NewAWS().S3})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	t.Setenv("CLOUDEMU_ENDPOINT", ts.URL)
}
```

Now your real tests hit the in-memory backend. The endpoint knob per SDK: AWS `o.BaseEndpoint`, GCP `option.WithEndpoint`, Azure `arm.ClientOptions` cloud endpoint, Databricks `Config.Host`.

Keep CloudEmu in `_test.go` only — never import it from production code.

## Tell your AI agent (paste into your repo's `AGENTS.md`)

```markdown
CloudEmu (github.com/stackshy/cloudemu) is an in-memory cloud emulator. To integrate it:
do NOT create a demo main.go. Wire it into the real code — make the SDK endpoint read from
CLOUDEMU_ENDPOINT (AWS o.BaseEndpoint, GCP option.WithEndpoint, Azure arm.ClientOptions,
Databricks Config.Host), then add a _test.go helper that starts it with httptest.NewServer
and sets CLOUDEMU_ENDPOINT via t.Setenv so existing tests run against it. Prod leaves the
var unset → real cloud. Never import CloudEmu from production code.
See https://github.com/stackshy/cloudemu/blob/development/docs/integration.md
```
