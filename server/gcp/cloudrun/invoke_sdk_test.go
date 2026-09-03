package cloudrun_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"google.golang.org/api/option"
	run "google.golang.org/api/run/v2"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/recursionguard"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
	crdriver "github.com/stackshy/cloudemu/v2/services/cloudrun/driver"
)

// invokeFixture boots a full in-process GCP server backed by the real
// cloudemu Cloud Run driver, returns a run/v2 SDK client for the Admin API,
// and exposes the concrete driver + httptest server so a test can register a
// Go handler and dial the server directly for the invoke path.
type invokeFixture struct {
	run *run.Service
	cr  crdriver.CloudRun
	ts  *httptest.Server
}

func newInvokeFixture(t *testing.T) invokeFixture {
	t.Helper()

	cloud := cloudemu.NewGCP(config.WithContainerEngine(nil))
	ts := httptest.NewServer(gcpserver.New(gcpserver.Drivers{CloudRun: cloud.CloudRun}))
	t.Cleanup(ts.Close)

	sdk, err := run.NewService(context.Background(), option.WithEndpoint(ts.URL), option.WithoutAuthentication())
	if err != nil {
		t.Fatalf("run.NewService: %v", err)
	}

	return invokeFixture{run: sdk, cr: cloud.CloudRun, ts: ts}
}

// createService deploys serviceID via the real run/v2 SDK and returns its
// reconciled, generated *.run.app URI — exactly what a real caller would read
// off the Create response to learn where to invoke the service.
func (f invokeFixture) createService(t *testing.T, serviceID string) string {
	t.Helper()

	op, err := f.run.Projects.Locations.Services.Create(parent, sdkService()).
		ServiceId(serviceID).Context(context.Background()).Do()
	if err != nil {
		t.Fatalf("Services.Create: %v", err)
	}

	var created run.GoogleCloudRunV2Service
	decodeOpResponse(t, op, &created)

	if created.Uri == "" {
		t.Fatalf("created service has no uri: %+v", created)
	}

	return created.Uri
}

// doInvoke issues method/body against the service's generated URL: it dials
// this fixture's real httptest server address (there is no real DNS for a
// *.run.app host) while setting the Host header to the service URL's host, so
// the wire server's Host-based routing resolves it — mirroring how a real
// client (curl --resolve, a hosts-file override) would reach a locally run
// emulator at a cloud-shaped URL. It takes no *testing.T so it is also safe to
// call from a registered handler running on a server goroutine (t.Fatalf may
// only be called from the goroutine running the test).
func (f invokeFixture) doInvoke(
	ctx context.Context, serviceURI, method, path string, body io.Reader, depth int,
) (*http.Response, error) {
	u, err := url.Parse(serviceURI)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, method, f.ts.URL+path, body)
	if err != nil {
		return nil, err
	}

	req.Host = u.Host

	if depth > 0 {
		req.Header.Set(recursionguard.DepthHeader, strconv.Itoa(depth))
	}

	return f.ts.Client().Do(req)
}

// invoke is doInvoke for use directly from the test goroutine, failing the
// test on any transport error.
func (f invokeFixture) invoke(t *testing.T, serviceURI, method, path string, body io.Reader, depth int) *http.Response {
	t.Helper()

	resp, err := f.doInvoke(context.Background(), serviceURI, method, path, body, depth)
	if err != nil {
		t.Fatalf("invoke %s %s: %v", method, path, err)
	}

	return resp
}

func TestServiceInvokeEchoesBody(t *testing.T) {
	f := newInvokeFixture(t)
	uri := f.createService(t, "echo")

	resp := f.invoke(t, uri, http.MethodPost, "/anything", strings.NewReader(`{"hello":"world"}`), 0)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	got, _ := io.ReadAll(resp.Body)
	if string(got) != `{"hello":"world"}` {
		t.Fatalf("body = %q, want echo", got)
	}
}

func TestServiceInvokeNoBodyReturnsGreeting(t *testing.T) {
	f := newInvokeFixture(t)
	uri := f.createService(t, "greet")

	resp := f.invoke(t, uri, http.MethodGet, "/", nil, 0)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	got, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(got), "greet") {
		t.Fatalf("body = %q, want a greeting naming the service", got)
	}
}

func TestServiceInvokeRegisteredHandlerRuns(t *testing.T) {
	f := newInvokeFixture(t)
	uri := f.createService(t, "handled")

	var gotMethod, gotPath string

	f.cr.RegisterHandler("handled", func(_ context.Context, req crdriver.InvokeRequest) (crdriver.InvokeResponse, error) {
		gotMethod, gotPath = req.Method, req.Path

		return crdriver.InvokeResponse{StatusCode: http.StatusCreated, Body: []byte("handled it")}, nil
	})

	resp := f.invoke(t, uri, http.MethodPut, "/items/42", strings.NewReader("payload"), 0)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}

	got, _ := io.ReadAll(resp.Body)
	if string(got) != "handled it" {
		t.Fatalf("body = %q", got)
	}

	if gotMethod != http.MethodPut || gotPath != "/items/42" {
		t.Fatalf("handler saw method=%q path=%q", gotMethod, gotPath)
	}
}

func TestServiceInvokeUnknownHostNotFound(t *testing.T) {
	f := newInvokeFixture(t)

	resp := f.invoke(t, "https://nope-0000000000.us-central1.run.app", http.MethodGet, "/", nil, 0)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

// TestServiceInvokeSelfCallTerminatesAtRecursionGuard covers a service whose
// handler calls back into its own URL: left unbounded this recurses
// synchronously forever, so it must terminate at recursionguard.MaxDepth
// (mirroring AWS Lambda's own recursive-loop detection) rather than hanging
// or overflowing the goroutine stack.
func TestServiceInvokeSelfCallTerminatesAtRecursionGuard(t *testing.T) {
	f := newInvokeFixture(t)
	uri := f.createService(t, "loopy")

	var invocations atomic.Int32

	f.cr.RegisterHandler("loopy", func(ctx context.Context, _ crdriver.InvokeRequest) (crdriver.InvokeResponse, error) {
		invocations.Add(1)

		depth := recursionguard.Depth(ctx)

		resp, err := f.doInvoke(ctx, uri, http.MethodGet, "/", nil, depth)
		if err != nil {
			return crdriver.InvokeResponse{}, err
		}
		defer resp.Body.Close()

		body, _ := io.ReadAll(resp.Body)

		return crdriver.InvokeResponse{StatusCode: resp.StatusCode, Body: body}, nil
	})

	resp := f.invoke(t, uri, http.MethodGet, "/", nil, 0)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusLoopDetected {
		t.Fatalf("outermost status = %d, want 508 (the innermost hop that hit the guard)", resp.StatusCode)
	}

	if got := invocations.Load(); got != int32(recursionguard.MaxDepth) {
		t.Fatalf("handler ran %d times, want exactly recursionguard.MaxDepth (%d)", got, recursionguard.MaxDepth)
	}
}
