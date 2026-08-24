package notificationhubs_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/notificationhubs/armnotificationhubs"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

const gcmRegistration = `<?xml version="1.0" encoding="utf-8"?>
<entry xmlns="http://www.w3.org/2005/Atom">
  <content type="application/xml">
    <GcmRegistrationDescription xmlns="http://schemas.microsoft.com/netservices/2010/10/servicebus/connect">
      <Tags>tag1,tag2</Tags>
      <GcmRegistrationId>DEVICE-TOKEN-123</GcmRegistrationId>
    </GcmRegistrationDescription>
  </content>
</entry>`

var registrationIDRe = regexp.MustCompile(`<RegistrationId>([^<]+)</RegistrationId>`)

func newRegServer(t *testing.T) (*httptest.Server, *armnotificationhubs.ClientFactory) {
	t.Helper()

	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{NotificationHubs: cloudP.NotificationHubs})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	myCloud := cloud.Configuration{
		ActiveDirectoryAuthorityHost: "https://login.microsoftonline.com/",
		Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
			cloud.ResourceManager: {Endpoint: ts.URL, Audience: "https://management.azure.com"},
		},
	}
	opts := &arm.ClientOptions{ClientOptions: azcore.ClientOptions{
		Cloud: myCloud, Transport: ts.Client(), Retry: policy.RetryOptions{MaxRetries: -1},
	}}

	cf, err := armnotificationhubs.NewClientFactory(testSub, fakeCred{}, opts)
	if err != nil {
		t.Fatalf("NewClientFactory: %v", err)
	}

	return ts, cf
}

func regRequest(t *testing.T, ts *httptest.Server, method, path, body string) *http.Response {
	t.Helper()

	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}

	req, err := http.NewRequestWithContext(context.Background(), method, ts.URL+path, rdr)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	req.Host = "my-ns.servicebus.windows.net"
	req.Header.Set("Content-Type", "application/atom+xml;type=entry;charset=utf-8")
	req.Header.Set("x-ms-version", "2015-01")

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}

	return resp
}

func TestDataPlaneDeviceRegistration(t *testing.T) {
	ts, cf := newRegServer(t)
	ctx := context.Background()

	// The hub must exist (ARM control plane) before registering a device.
	ns := cf.NewNamespacesClient()
	if _, err := ns.CreateOrUpdate(ctx, testRG, "my-ns",
		armnotificationhubs.NamespaceCreateOrUpdateParameters{Location: to.Ptr("eastus")}, nil); err != nil {
		t.Fatalf("namespace create: %v", err)
	}

	if _, err := cf.NewClient().CreateOrUpdate(ctx, testRG, "my-ns", "hub1",
		armnotificationhubs.NotificationHubCreateOrUpdateParameters{Location: to.Ptr("eastus")}, nil); err != nil {
		t.Fatalf("hub create: %v", err)
	}

	// Create the registration on the data plane.
	resp := regRequest(t, ts, http.MethodPost, "/hub1/registrations/", gcmRegistration)
	body := readBody(t, resp)

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, body=%s", resp.StatusCode, body)
	}

	if !strings.Contains(body, "DEVICE-TOKEN-123") {
		t.Fatalf("response missing device token: %s", body)
	}

	m := registrationIDRe.FindStringSubmatch(body)
	if len(m) != 2 || m[1] == "" {
		t.Fatalf("response missing RegistrationId: %s", body)
	}

	regID := m[1]

	// Read it back.
	getResp := regRequest(t, ts, http.MethodGet, "/hub1/registrations/"+regID, "")
	getBody := readBody(t, getResp)

	if getResp.StatusCode != http.StatusOK || !strings.Contains(getBody, "DEVICE-TOKEN-123") {
		t.Fatalf("get status=%d body=%s", getResp.StatusCode, getBody)
	}

	// Delete it.
	delResp := regRequest(t, ts, http.MethodDelete, "/hub1/registrations/"+regID, "")
	_ = readBody(t, delResp)

	if delResp.StatusCode != http.StatusOK {
		t.Fatalf("delete status = %d", delResp.StatusCode)
	}

	// A registration under a missing hub is a 404.
	missing := regRequest(t, ts, http.MethodPost, "/ghost-hub/registrations/", gcmRegistration)
	_ = readBody(t, missing)

	if missing.StatusCode != http.StatusNotFound {
		t.Fatalf("missing-hub status = %d, want 404", missing.StatusCode)
	}
}

// TestCreateRegistrationIDLocationHeader drives the CreateRegistrationIdAsync
// flow from the real .NET SDK: POST .../registrationids/, then parse the
// Location header. The SDK extracts the id only when the URI path is
// /{hub}/registrationids/{id} — it checks that Location.Segments[2] equals
// "registrationids/" (a 4-segment path) — so the server must emit that exact
// collection segment, not "registrations".
func TestCreateRegistrationIDLocationHeader(t *testing.T) {
	ts, cf := newRegServer(t)
	ctx := context.Background()

	ns := cf.NewNamespacesClient()
	if _, err := ns.CreateOrUpdate(ctx, testRG, "my-ns",
		armnotificationhubs.NamespaceCreateOrUpdateParameters{Location: to.Ptr("eastus")}, nil); err != nil {
		t.Fatalf("namespace create: %v", err)
	}

	if _, err := cf.NewClient().CreateOrUpdate(ctx, testRG, "my-ns", "hub1",
		armnotificationhubs.NotificationHubCreateOrUpdateParameters{Location: to.Ptr("eastus")}, nil); err != nil {
		t.Fatalf("hub create: %v", err)
	}

	resp := regRequest(t, ts, http.MethodPost, "/hub1/registrationids/", "")
	_ = readBody(t, resp)

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create-registrationid status = %d, want 201", resp.StatusCode)
	}

	loc := resp.Header.Get("Location")
	if loc == "" {
		t.Fatalf("missing Location header")
	}

	u, err := url.Parse(loc)
	if err != nil {
		t.Fatalf("parse Location %q: %v", loc, err)
	}

	// Replicate CreateRegistrationIdAsync's parsing: split into leading-slash
	// segments and require Segments[2] == "registrationids/", extracting [3].
	segs := locationSegments(u.Path)
	if len(segs) != segCount || !strings.EqualFold(segs[regIDSegIdx], "registrationids/") {
		t.Fatalf("Location path %q segments = %v, want /{hub}/registrationids/{id}", u.Path, segs)
	}

	regID := strings.TrimSuffix(segs[regIDSegIdx+1], "/")
	if regID == "" {
		t.Fatalf("empty registration id from Location %q", loc)
	}

	// The extracted id addresses the registration slot: a PUT upsert lands there.
	putResp := regRequest(t, ts, http.MethodPut, "/hub1/registrations/"+regID, gcmRegistration)
	putBody := readBody(t, putResp)

	if putResp.StatusCode != http.StatusOK || !strings.Contains(putBody, regID) {
		t.Fatalf("upsert status=%d body=%s", putResp.StatusCode, putBody)
	}
}

const (
	segCount    = 4
	regIDSegIdx = 2
)

// locationSegments splits a URI path the way System.Uri.Segments does: each
// segment keeps its trailing slash and the leading "/" is its own segment.
func locationSegments(p string) []string {
	out := []string{"/"}

	for _, part := range strings.SplitAfter(strings.TrimPrefix(p, "/"), "/") {
		if part != "" {
			out = append(out, part)
		}
	}

	return out
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	return string(b)
}
