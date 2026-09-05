package gcs_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"cloud.google.com/go/storage"
	"google.golang.org/api/option"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

// TestGCSBucketCORSRoundTrip proves a bucket's cors[] configuration set at
// create is read back, and that a patch replaces it — real GCS persists the
// cors field (google_storage_bucket's cors block), which was previously
// accepted-and-dropped by the wire layer.
func TestGCSBucketCORSRoundTrip(t *testing.T) {
	ctx, client := newStorageClient(t)

	bkt := client.Bucket("cors-cfg")
	if err := bkt.Create(ctx, e2eProject, &storage.BucketAttrs{
		CORS: []storage.CORS{{
			MaxAge:          time.Hour,
			Methods:         []string{"GET", "HEAD"},
			Origins:         []string{"*"},
			ResponseHeaders: []string{"Content-Type"},
		}},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	a, err := bkt.Attrs(ctx)
	if err != nil {
		t.Fatalf("Attrs: %v", err)
	}

	if len(a.CORS) != 1 {
		t.Fatalf("CORS rules = %d, want 1 (cors dropped on round-trip)", len(a.CORS))
	}

	rule := a.CORS[0]
	if rule.MaxAge != time.Hour {
		t.Errorf("MaxAge = %v, want 1h", rule.MaxAge)
	}

	if len(rule.Methods) != 2 || rule.Methods[0] != "GET" || rule.Methods[1] != "HEAD" {
		t.Errorf("Methods = %v, want [GET HEAD]", rule.Methods)
	}

	if len(rule.Origins) != 1 || rule.Origins[0] != "*" {
		t.Errorf("Origins = %v, want [*]", rule.Origins)
	}

	if len(rule.ResponseHeaders) != 1 || rule.ResponseHeaders[0] != "Content-Type" {
		t.Errorf("ResponseHeaders = %v, want [Content-Type]", rule.ResponseHeaders)
	}

	// A patch replaces the rule set.
	if _, err := bkt.Update(ctx, storage.BucketAttrsToUpdate{
		CORS: []storage.CORS{{
			MaxAge:  2 * time.Hour,
			Methods: []string{"PUT"},
			Origins: []string{"https://example.com"},
		}},
	}); err != nil {
		t.Fatalf("Update CORS: %v", err)
	}

	a2, err := bkt.Attrs(ctx)
	if err != nil {
		t.Fatalf("Attrs after update: %v", err)
	}

	if len(a2.CORS) != 1 || a2.CORS[0].MaxAge != 2*time.Hour || a2.CORS[0].Methods[0] != "PUT" {
		t.Errorf("CORS after patch = %+v, want the replaced rule", a2.CORS)
	}
}

// TestGCSBucketLocationUppercased proves a lower-case location supplied at
// create is normalized to upper case on read, matching real GCS (which always
// returns e.g. "US-CENTRAL1").
func TestGCSBucketLocationUppercased(t *testing.T) {
	ctx, client := newStorageClient(t)

	bkt := client.Bucket("loc-lower")
	if err := bkt.Create(ctx, e2eProject, &storage.BucketAttrs{Location: "us-central1"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	a, err := bkt.Attrs(ctx)
	if err != nil {
		t.Fatalf("Attrs: %v", err)
	}

	if a.Location != "US-CENTRAL1" {
		t.Errorf("Location = %q, want US-CENTRAL1 (GCS uppercases locations)", a.Location)
	}
}

// TestGCSBucketLabelMergePatch proves a Buckets.patch merges labels rather than
// replacing them, and that a label mapped to null is deleted — the semantics the
// SDK/Terraform rely on (SetLabel/DeleteLabel send only the changed keys, with a
// deleted key encoded as JSON null).
func TestGCSBucketLabelMergePatch(t *testing.T) {
	ctx, client := newStorageClient(t)

	bkt := client.Bucket("labels-merge")
	if err := bkt.Create(ctx, e2eProject, &storage.BucketAttrs{
		Labels: map[string]string{"env": "test", "team": "platform", "region": "us"},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	upd := storage.BucketAttrsToUpdate{}
	upd.SetLabel("env", "prod")
	upd.DeleteLabel("team")

	a, err := bkt.Update(ctx, upd)
	if err != nil {
		t.Fatalf("Update labels: %v", err)
	}

	want := map[string]string{"env": "prod", "region": "us"}
	if len(a.Labels) != len(want) {
		t.Fatalf("Labels = %v, want %v", a.Labels, want)
	}

	for k, v := range want {
		if a.Labels[k] != v {
			t.Errorf("Labels[%q] = %q, want %q", k, a.Labels[k], v)
		}
	}

	if _, ok := a.Labels["team"]; ok {
		t.Errorf("label 'team' should have been deleted by a null patch, got %q", a.Labels["team"])
	}
}

// TestGCSBucketVersioningDisabledWireField proves the wire distinction Terraform
// depends on: a bucket that never configured versioning omits the versioning
// field entirely, while a bucket whose versioning was explicitly disabled
// returns {"enabled":false}. At the SDK level both read as VersioningEnabled ==
// false, so the presence/absence of the field — which drives whether a
// `versioning { enabled = false }` block perpetually diffs — is asserted on the
// raw JSON.
func TestGCSBucketVersioningDisabledWireField(t *testing.T) {
	cloudP := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.Drivers{Storage: cloudP.GCS})
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
	client.SetRetry(storage.WithPolicy(storage.RetryNever))
	t.Cleanup(func() { _ = client.Close() })

	// A bucket that never configured versioning must omit the field.
	if err := client.Bucket("ver-plain").Create(ctx, e2eProject, nil); err != nil {
		t.Fatalf("create plain: %v", err)
	}

	if _, present := bucketVersioningField(t, ts, "ver-plain"); present {
		t.Errorf("versioning field present on a never-configured bucket, want omitted")
	}

	// A bucket disabled after being enabled must return {enabled:false}.
	bkt := client.Bucket("ver-toggle")
	if err := bkt.Create(ctx, e2eProject, nil); err != nil {
		t.Fatalf("create toggle: %v", err)
	}

	if _, err := bkt.Update(ctx, storage.BucketAttrsToUpdate{VersioningEnabled: true}); err != nil {
		t.Fatalf("enable versioning: %v", err)
	}

	if _, err := bkt.Update(ctx, storage.BucketAttrsToUpdate{VersioningEnabled: false}); err != nil {
		t.Fatalf("disable versioning: %v", err)
	}

	enabled, present := bucketVersioningField(t, ts, "ver-toggle")
	if !present {
		t.Fatalf("versioning field omitted after an explicit disable, want {enabled:false}")
	}

	if enabled {
		t.Errorf("versioning.enabled = true after disable, want false")
	}
}

// bucketVersioningField GETs the raw bucket JSON and reports the versioning
// block's enabled value and whether the versioning field was present at all.
func bucketVersioningField(t *testing.T, ts *httptest.Server, name string) (enabled, present bool) {
	t.Helper()

	resp, err := ts.Client().Get(ts.URL + "/storage/v1/b/" + name)
	if err != nil {
		t.Fatalf("GET bucket %q: %v", name, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read bucket %q body: %v", name, err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET bucket %q status = %d: %s", name, resp.StatusCode, body)
	}

	var doc struct {
		Versioning *struct {
			Enabled bool `json:"enabled"`
		} `json:"versioning"`
	}

	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("unmarshal bucket %q: %v", name, err)
	}

	if doc.Versioning == nil {
		return false, false
	}

	return doc.Versioning.Enabled, true
}
