package gcp_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"cloud.google.com/go/storage"
	"google.golang.org/api/option"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

// TestNewFromProvider verifies that a server built straight from a
// fully-constructed provider serves real cloud.google.com/go traffic.
func TestNewFromProvider(t *testing.T) {
	p := cloudemu.NewGCP()

	srv := gcpserver.NewFromProvider(p)
	if srv == nil {
		t.Fatal("NewFromProvider returned nil")
	}

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	ctx := context.Background()

	client, err := storage.NewClient(ctx,
		option.WithEndpoint(ts.URL+"/storage/v1/"),
		option.WithoutAuthentication(),
		option.WithHTTPClient(ts.Client()),
	)
	if err != nil {
		t.Fatalf("storage.NewClient: %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })

	bucket := client.Bucket("b1")

	if err := bucket.Create(ctx, "p1", nil); err != nil {
		t.Fatalf("bucket.Create: %v", err)
	}

	attrs, err := bucket.Attrs(ctx)
	if err != nil {
		t.Fatalf("bucket.Attrs: %v", err)
	}

	if attrs.Name != "b1" {
		t.Errorf("bucket name mismatch: got=%q want=%q", attrs.Name, "b1")
	}
}
