package securesourcemanager

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	ssmprovider "github.com/stackshy/cloudemu/v2/providers/gcp/securesourcemanager"
	"github.com/stackshy/cloudemu/v2/server/gcp/lro"
	ssmdriver "github.com/stackshy/cloudemu/v2/services/securesourcemanager/driver"
)

func newHandler(t *testing.T) (*Handler, *ssmprovider.Mock) {
	t.Helper()

	mock := ssmprovider.New(config.NewOptions(config.WithProjectID("p")))

	return New(mock), mock
}

func request(t *testing.T, method, path, body string) *http.Request {
	t.Helper()

	var rdr *strings.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}

	if rdr == nil {
		r, _ := http.NewRequest(method, "http://x"+path, nil)

		return r
	}

	r, _ := http.NewRequest(method, "http://x"+path, rdr)

	return r
}

const scope = "/v1/projects/p/locations/us-central1"

// TestMatchesInstancesDisambiguation is the make-or-break test: instances share
// the identical path with Filestore and Memorystore, so this handler must claim
// only genuinely-Secure-Source-Manager instance traffic and let the sibling
// services' traffic fall through.
func TestMatchesInstancesDisambiguation(t *testing.T) {
	h, mock := newHandler(t)

	// A Filestore create body (fileShares/networks) must NOT be claimed.
	if h.Matches(request(t, http.MethodPost, scope+"/instances",
		`{"fileShares":[{"name":"share1"}],"networks":[{"network":"default"}]}`)) {
		t.Fatalf("claimed a Filestore create body (would steal Filestore traffic)")
	}

	// A Memorystore Redis create body (memorySizeGb/tier) must NOT be claimed.
	if h.Matches(request(t, http.MethodPost, scope+"/instances",
		`{"tier":"BASIC","memorySizeGb":1}`)) {
		t.Fatalf("claimed a Redis create body (would steal Memorystore traffic)")
	}

	// A genuine Secure Source Manager create body (no sibling signal) is claimed.
	if !h.Matches(request(t, http.MethodPost, scope+"/instances",
		`{"kmsKey":"projects/p/locations/us-central1/keyRings/r/cryptoKeys/k"}`)) {
		t.Fatalf("did not claim a genuine Secure Source Manager instance create")
	}

	// An empty create body is a bare public instance -> claimed.
	if !h.Matches(request(t, http.MethodPost, scope+"/instances", "")) {
		t.Fatalf("did not claim an empty instance create body")
	}

	// Item + bare LIST are claimed ONLY when this store owns the instance.
	if h.Matches(request(t, http.MethodGet, scope+"/instances/inst", "")) {
		t.Fatalf("claimed an unowned instance item (would steal Filestore/Redis traffic)")
	}

	if h.Matches(request(t, http.MethodGet, scope+"/instances", "")) {
		t.Fatalf("claimed a bare LIST with no owned instance in scope")
	}

	if _, _, err := mock.CreateInstance(context.Background(), &ssmdriver.Config{
		Project: "p", Location: "us-central1", ID: "inst",
	}); err != nil {
		t.Fatalf("seed instance: %v", err)
	}

	if !h.Matches(request(t, http.MethodGet, scope+"/instances/inst", "")) {
		t.Fatalf("did not claim an owned instance item")
	}

	if !h.Matches(request(t, http.MethodGet, scope+"/instances", "")) {
		t.Fatalf("did not claim a LIST once an instance is owned in scope")
	}

	// An item in a scope where this store owns nothing still falls through.
	if h.Matches(request(t, http.MethodGet, scope+"/instances/other", "")) {
		t.Fatalf("claimed a different, unowned instance item")
	}
}

// TestMatchesRepositoriesDisambiguation verifies the repositories collection —
// shared on the identical /v1/ path with Artifact Registry (the greedy
// fall-through) — is claimed only for genuinely-Secure-Source-Manager traffic:
// a create carrying the required `instance` reference, and item/LIST only for a
// repository this store owns.
func TestMatchesRepositoriesDisambiguation(t *testing.T) {
	h, mock := newHandler(t)

	// An Artifact Registry create body (format, no instance) must NOT be claimed.
	if h.Matches(request(t, http.MethodPost, scope+"/repositories",
		`{"format":"DOCKER","mode":"STANDARD_REPOSITORY"}`)) {
		t.Fatalf("claimed an Artifact Registry create body (would steal AR traffic)")
	}

	// A Secure Source Manager create body (carries the instance reference) is claimed.
	if !h.Matches(request(t, http.MethodPost, scope+"/repositories",
		`{"instance":"projects/p/locations/us-central1/instances/inst"}`)) {
		t.Fatalf("did not claim a genuine Secure Source Manager repository create")
	}

	// Item + bare LIST are claimed ONLY when this store owns the repository.
	if h.Matches(request(t, http.MethodGet, scope+"/repositories/repo", "")) {
		t.Fatalf("claimed an unowned repository item (would steal AR traffic)")
	}

	if h.Matches(request(t, http.MethodGet, scope+"/repositories", "")) {
		t.Fatalf("claimed a bare LIST with no owned repository in scope")
	}

	if _, _, err := mock.CreateRepository(context.Background(), &ssmdriver.Config{
		Project: "p", Location: "us-central1", ID: "repo",
	}); err != nil {
		t.Fatalf("seed repository: %v", err)
	}

	if !h.Matches(request(t, http.MethodGet, scope+"/repositories/repo", "")) {
		t.Fatalf("did not claim an owned repository item")
	}

	if !h.Matches(request(t, http.MethodGet, scope+"/repositories", "")) {
		t.Fatalf("did not claim a LIST once a repository is owned in scope")
	}
}

// TestMatchesForeignSpaces verifies foreign resource spaces are never claimed.
func TestMatchesForeignSpaces(t *testing.T) {
	h, _ := newHandler(t)

	for _, path := range []string{
		scope + "/connectors/c",
		scope + "/environments/e",
		"/v1/projects/p/locations/us-central1",
		"/compute/v1/projects/p/locations/us-central1/repositories",
	} {
		if h.Matches(request(t, http.MethodGet, path, "")) {
			t.Fatalf("claimed foreign path %s", path)
		}
	}
}

// TestMatchesOperationsYieldToPoller verifies the handler claims operation polls
// only when it has no shared LRO registry, so it never regresses a sibling
// service's operation into a 404 in an assembled server.
func TestMatchesOperationsYieldToPoller(t *testing.T) {
	standalone, _ := newHandler(t)

	opPath := scope + "/operations/op-1"
	if !standalone.Matches(request(t, http.MethodGet, opPath, "")) {
		t.Fatalf("standalone handler should claim its own operation polls")
	}

	shared, _ := newHandler(t)
	shared.SetOperationRegistry(lro.NewRegistry())

	if shared.Matches(request(t, http.MethodGet, opPath, "")) {
		t.Fatalf("handler with shared registry must yield operation polls to the poller")
	}
}
