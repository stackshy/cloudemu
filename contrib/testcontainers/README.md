# cloudemu Testcontainers module

Run the [cloudemu](https://github.com/stackshy/cloudemu) standalone server in a
container for out-of-process integration tests, using
[Testcontainers-Go](https://golang.testcontainers.org/).

This is a **separate Go module** so `testcontainers-go` (and the Docker client
it pulls in) never becomes a dependency of the core `cloudemu` module.

```go
import (
    "testing"

    cloudemu "github.com/stackshy/cloudemu/v2/contrib/testcontainers"
)

func TestMyApp(t *testing.T) {
    ctx := context.Background()

    ctr, err := cloudemu.Run(ctx) // pulls ghcr.io/stackshy/cloudemu:latest
    if err != nil {
        t.Fatal(err)
    }
    defer ctr.Terminate(ctx)

    endpoint, _ := ctr.AWSEndpoint(ctx) // http://host:mappedPort
    // point aws-sdk-go-v2 at `endpoint` (o.BaseEndpoint), then run your code.

    ctr.Reset(ctx)             // clean slate between tests
    ctr.Seed(ctx, myFixtures)  // load a known baseline (seed.Fixtures or any JSON-able value)
}
```

Methods: `AWSEndpoint`, `AzureEndpoint`, `GCPEndpoint`, `KubernetesEndpoint`,
`Reset`, `Seed`, plus the embedded Testcontainers container (`Terminate`, …).
Pin a version or use a local image with `cloudemu.WithImage("...")`.

The module's own acceptance test builds the image from the repo `Dockerfile`, so
it needs Docker; run it with `go test ./...` from this directory (skipped under
`-short`).
