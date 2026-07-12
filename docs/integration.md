# Integrating CloudEmu into Your App

The mistake to avoid: **don't write a new `main.go` that just calls CloudEmu and prints "it works."** That tests the library, not your app.

Do this instead — point your app's *existing* cloud client at CloudEmu in your *existing* tests. Your real code runs against an in-memory cloud, no mocks.

## The trick

Make the endpoint injectable, then point it at CloudEmu in tests. An env var is the easy default (shown below) — but it's your call: a config field or setting it directly in the test works just as well. Production uses the real cloud; your code doesn't change.

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
CloudEmu (github.com/stackshy/cloudemu/v2) is an in-memory cloud emulator. To integrate it:
do NOT create a demo main.go — wire it into the real code so existing tests exercise it.
Make the SDK endpoint injectable however this codebase prefers (an env var, a config field,
or set directly in the test — your choice) and point it at CloudEmu: AWS o.BaseEndpoint,
GCP option.WithEndpoint, Azure arm.ClientOptions, Databricks Config.Host. Add a _test.go
helper that starts CloudEmu with httptest.NewServer and points the client at its URL so
existing tests run against it. Production uses the real cloud, code unchanged. Never import
CloudEmu from production code.
See https://github.com/stackshy/cloudemu/blob/development/docs/integration.md
```
