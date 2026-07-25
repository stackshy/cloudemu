// Package cloudemu is a Testcontainers-Go module that runs the cloudemu
// standalone server in a container for out-of-process integration tests.
//
//	ctr, err := cloudemu.Run(ctx)
//	defer ctr.Terminate(ctx)
//	endpoint, _ := ctr.AWSEndpoint(ctx) // point your aws-sdk-go-v2 client here
//	ctr.Reset(ctx)                      // clean slate between tests
//
// It lives in its own module so testcontainers-go (and the Docker client it
// pulls in) never becomes a dependency of the core cloudemu module.
package cloudemu

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// DefaultImage is the published image used unless overridden with WithImage.
const DefaultImage = "ghcr.io/stackshy/cloudemu:latest"

const (
	awsPort   = "4566/tcp"
	azurePort = "4568/tcp"
	gcpPort   = "4569/tcp"
	k8sPort   = "4570/tcp"
)

// Container is a running cloudemu server. It embeds the testcontainers
// container, so Terminate and the rest of that API are available directly.
type Container struct {
	*testcontainers.DockerContainer
}

// WithImage overrides the image (default DefaultImage) — e.g. to pin a version
// or use a locally-built tag.
func WithImage(image string) testcontainers.ContainerCustomizer {
	return testcontainers.WithImage(image)
}

// Run starts a cloudemu container and waits until the AWS endpoint answers.
func Run(ctx context.Context, opts ...testcontainers.ContainerCustomizer) (*Container, error) {
	base := []testcontainers.ContainerCustomizer{
		testcontainers.WithExposedPorts(awsPort, azurePort, gcpPort, k8sPort),
		testcontainers.WithWaitStrategy(
			wait.ForHTTP("/").WithPort(awsPort).WithStartupTimeout(90 * time.Second),
		),
	}

	ctr, err := testcontainers.Run(ctx, DefaultImage, append(base, opts...)...)
	if err != nil {
		return nil, fmt.Errorf("run cloudemu: %w", err)
	}
	return &Container{DockerContainer: ctr}, nil
}

// AWSEndpoint is the host URL for the AWS surface (point aws-sdk-go-v2 here with
// BaseEndpoint + path-style).
func (c *Container) AWSEndpoint(ctx context.Context) (string, error) {
	return c.PortEndpoint(ctx, awsPort, "http")
}

// AzureEndpoint is the host URL for the Azure surface (HTTPS, self-signed cert).
func (c *Container) AzureEndpoint(ctx context.Context) (string, error) {
	return c.PortEndpoint(ctx, azurePort, "https")
}

// GCPEndpoint is the host URL for the GCP surface.
func (c *Container) GCPEndpoint(ctx context.Context) (string, error) {
	return c.PortEndpoint(ctx, gcpPort, "http")
}

// KubernetesEndpoint is the host URL for the shared Kubernetes data-plane.
func (c *Container) KubernetesEndpoint(ctx context.Context) (string, error) {
	return c.PortEndpoint(ctx, k8sPort, "http")
}

// Reset wipes all emulator state — call it between tests for a clean slate.
func (c *Container) Reset(ctx context.Context) error {
	return c.control(ctx, "reset", nil)
}

// Seed bulk-loads a fixture into the AWS provider. fixture is marshalled to
// JSON, so pass a seed.Fixtures value or any equivalent structure.
func (c *Container) Seed(ctx context.Context, fixture any) error {
	body, err := json.Marshal(fixture)
	if err != nil {
		return fmt.Errorf("marshal fixture: %w", err)
	}
	return c.control(ctx, "seed", body)
}

func (c *Container) control(ctx context.Context, op string, body []byte) error {
	base, err := c.AWSEndpoint(ctx)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/_cloudemu/"+op, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("_cloudemu/%s: status %d: %s", op, resp.StatusCode, msg)
	}
	return nil
}
