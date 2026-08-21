package compat

import (
	"context"
	"net/http"
	"net/http/httptest"

	"cloud.google.com/go/storage"
	"google.golang.org/api/option"

	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

const (
	providerGCP = "gcp"

	// GCPProject is the fixed project id GCP compat tests provision under.
	GCPProject = "compat-project"
)

// GCPSession is a Session backed by CloudEmu's GCP wire server plus the test
// server transport for building real google-cloud clients.
type GCPSession struct {
	*Session

	transport *http.Client
}

// BootGCP starts CloudEmu's GCP wire server in-process for the given drivers
// and returns a session. GCP client libraries switch to anonymous access when
// pointed at an emulator endpoint.
//
//nolint:gocritic // by-value Drivers mirrors gcpserver.New's ergonomic API
func BootGCP(tb TB, d gcpserver.Drivers) *GCPSession {
	tb.Helper()

	srv := gcpserver.New(d)
	ts := httptest.NewServer(srv)
	tb.Cleanup(ts.Close)

	s := &Session{tb: tb, provider: providerGCP, endpoint: ts.URL}
	tb.Cleanup(s.flush)

	return &GCPSession{Session: s, transport: ts.Client()}
}

// StorageClient returns a real GCS client pointed at the emulator (anonymous;
// retries disabled). The /storage/v1/ suffix is required — the SDK appends
// /b/... directly to the endpoint.
func (g *GCPSession) StorageClient(ctx context.Context) (*storage.Client, error) {
	c, err := storage.NewClient(ctx,
		option.WithEndpoint(g.endpoint+"/storage/v1/"),
		option.WithoutAuthentication(),
		option.WithHTTPClient(g.transport),
	)
	if err != nil {
		return nil, err
	}

	c.SetRetry(storage.WithPolicy(storage.RetryNever))

	return c, nil
}
