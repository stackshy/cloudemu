package serverkit

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/server"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
	ociserver "github.com/stackshy/cloudemu/v2/server/oci"
	"github.com/stackshy/cloudemu/v2/services/kubernetes"
)

// TestMatchesLeaveBodyIntact sends a body larger than any Matches peek limit
// through every handler's Matches and checks the body still reads back whole.
// The AWS auth gate authorizes a request from the body as Matches left it, and
// the handler later serves the same request, so a Matches that truncates the
// body would let the gate and dispatch see different requests.
//
// Form-encoded bodies are left out on purpose: a query handler's Matches
// parses them into r.Form, which both the gate and dispatch read from.
func TestMatchesLeaveBodyIntact(t *testing.T) {
	aws := awsserver.DriversFrom(cloudemu.NewAWS())
	aws.K8sAPI = kubernetes.NewAPIServer()

	servers := map[string]*server.Server{
		"aws": awsserver.New(aws),
		"gcp": gcpserver.New(gcpserver.DriversFrom(cloudemu.NewGCP())),
		"oci": ociserver.New(ociserver.DriversFrom(cloudemu.NewOCI())),
	}

	// Bigger than every peek limit (the largest is 8 MiB), and valid JSON so a
	// peek that parses it keeps going.
	body := []byte(`{"pad":"` + strings.Repeat("a", 9<<20) + `"}`)

	// Request shapes that reach each body-peeking Matches.
	shapes := []struct{ method, path, ctype string }{
		{http.MethodPost, "/TagResource", "application/json"},
		{http.MethodPost, "/UntagResource", "application/json"},
		{http.MethodPost, "/ListTagsForResource", "application/json"},
		{http.MethodPost, "/", "application/x-amz-json-1.1"},
		{http.MethodPost, "/v1/projects/p/locations/l/instances", "application/json"},
		{http.MethodPost, "/v1/projects/p/instances", "application/json"},
		{http.MethodPost, "/v1/projects/p/locations/l/clusters", "application/json"},
		{http.MethodPost, "/v1/projects/p/locations/l/repositories", "application/json"},
		{http.MethodPost, "/v1/projects/p/locations/us-central1-a/instances", "application/json"},
		{http.MethodPost, "/v1/projects/p/locations/l/backupPlans", "application/json"},
		{http.MethodPost, "/v1/projects/p/instances/i/databases", "application/json"},
		{http.MethodPost, "/v1/projects/p/locations/l/endpoints", "application/json"},
		{http.MethodPut, "/bucket/key", "application/octet-stream"},
	}

	for cloud, srv := range servers {
		for _, h := range srv.Handlers() {
			for _, sh := range shapes {
				req := httptest.NewRequest(sh.method, sh.path, bytes.NewReader(body))
				req.Header.Set("Content-Type", sh.ctype)

				h.Matches(req)

				got, err := io.ReadAll(req.Body)
				if err != nil || !bytes.Equal(got, body) {
					t.Errorf("%s %s: %s %s left %d of %d body bytes (err %v)",
						cloud, fmt.Sprintf("%T", h), sh.method, sh.path, len(got), len(body), err)
				}
			}
		}
	}
}
