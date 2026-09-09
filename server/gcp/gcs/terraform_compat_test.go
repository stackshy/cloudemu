package gcs_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"cloud.google.com/go/storage"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

// newRawServer boots a fresh emulator + GCP HTTP server and returns its base
// URL and an *http.Client, for tests that need to inspect the exact wire bytes
// the way the Terraform google provider / gcloud do (rather than through the
// SDK, which can mask a wire-shape gap).
func newRawServer(t *testing.T) (string, *http.Client) {
	t.Helper()

	cloudP := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.Drivers{Storage: cloudP.GCS})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	return ts.URL, ts.Client()
}

// TestMultipartUploadNameFromQueryParam reproduces the exact request the
// Terraform google provider (and gcloud/gsutil) send for
// google_storage_bucket_object: a multipart/related upload whose metadata part
// carries no name, with the object name supplied only via the ?name= query
// parameter. Per the GCS Objects: insert contract the query parameter "Overrides
// the object metadata's name value, if any" and is "Not required if the request
// body contains object metadata that includes a name value" — so the name may
// come from the query alone. cloudemu previously rejected this with
// "metadata.name required", breaking every Terraform object upload.
func TestMultipartUploadNameFromQueryParam(t *testing.T) {
	base, hc := newRawServer(t)
	ctx := context.Background()

	mkBucket(t, base, hc, "tf-bucket")

	const boundary = "cloudemu-boundary"

	body := "--" + boundary + "\r\n" +
		"Content-Type: application/json\r\n\r\n" +
		`{"bucket":"tf-bucket","contentType":"text/plain"}` + "\r\n" +
		"--" + boundary + "\r\n" +
		"Content-Type: text/plain\r\n\r\n" +
		"hello world\r\n" +
		"--" + boundary + "--\r\n"

	url := base + "/upload/storage/v1/b/tf-bucket/o?uploadType=multipart&name=hello.txt"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	req.Header.Set("Content-Type", "multipart/related; boundary="+boundary)

	resp, err := hc.Do(req)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload status = %d, want 200; body: %s", resp.StatusCode, raw)
	}

	var obj struct {
		Name        string `json:"name"`
		Bucket      string `json:"bucket"`
		Size        string `json:"size"`
		ContentType string `json:"contentType"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("decode object: %v; body: %s", err, raw)
	}

	if obj.Name != "hello.txt" {
		t.Errorf("object name = %q, want hello.txt (from query param)", obj.Name)
	}

	if obj.Size != "11" {
		t.Errorf("object size = %q, want 11", obj.Size)
	}

	if obj.ContentType != "text/plain" {
		t.Errorf("contentType = %q, want text/plain", obj.ContentType)
	}

	// The uploaded object must be independently retrievable under the
	// query-supplied name, confirming it was actually stored (not just echoed).
	got := getMediaBytes(t, base, hc, "tf-bucket", "hello.txt")
	if string(got) != "hello world" {
		t.Errorf("stored content = %q, want %q", got, "hello world")
	}
}

// TestAnywhereCachesListReturnsEmpty covers the Buckets anywhereCaches: list
// endpoint the Terraform google provider calls before force_destroy. cloudemu
// does not model Anywhere Cache instances, but the endpoint must answer 200 with
// an empty list (a bucket with no caches) — not a 404. When it errored, the
// provider aborted its object cleanup and failed the bucket delete with 409
// "not empty". Both the SDK's trailing-slash URL and the bare path are checked.
func TestAnywhereCachesListReturnsEmpty(t *testing.T) {
	base, hc := newRawServer(t)

	mkBucket(t, base, hc, "cache-bucket")

	for _, path := range []string{
		"/storage/v1/b/cache-bucket/anywhereCaches",
		"/storage/v1/b/cache-bucket/anywhereCaches/",
	} {
		resp, err := hc.Get(base + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}

		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200; body: %s", path, resp.StatusCode, raw)
		}

		var list struct {
			Kind  string `json:"kind"`
			Items []any  `json:"items"`
		}
		if err := json.Unmarshal(raw, &list); err != nil {
			t.Fatalf("decode %s: %v; body: %s", path, err, raw)
		}

		if list.Kind != "storage#anywhereCaches" {
			t.Errorf("GET %s kind = %q, want storage#anywhereCaches", path, list.Kind)
		}

		if len(list.Items) != 0 {
			t.Errorf("GET %s items = %v, want empty", path, list.Items)
		}
	}
}

// TestForceDestroyVersionedBucketViaSDK exercises the full clear-then-delete
// sequence the Terraform provider performs during force_destroy on a
// versioning-enabled bucket: overwrite an object (archiving a noncurrent
// version), list every version, delete each by generation, then delete the now
// empty bucket. This must succeed end to end.
func TestForceDestroyVersionedBucketViaSDK(t *testing.T) {
	ctx, client := newStorageClient(t)

	bkt := client.Bucket("fd-bucket")
	if err := bkt.Create(ctx, e2eProject, &storage.BucketAttrs{VersioningEnabled: true}); err != nil {
		t.Fatalf("create bucket: %v", err)
	}

	writeObject(t, ctx, bkt, "k", "v1")
	writeObject(t, ctx, bkt, "k", "v2") // archives v1 as a noncurrent version

	// Delete every version, mirroring the provider's versions=true sweep.
	it := bkt.Objects(ctx, &storage.Query{Versions: true})
	for {
		attrs, err := it.Next()
		if err != nil {
			break
		}

		if err := bkt.Object(attrs.Name).Generation(attrs.Generation).Delete(ctx); err != nil {
			t.Fatalf("delete %s@%d: %v", attrs.Name, attrs.Generation, err)
		}
	}

	if err := bkt.Delete(ctx); err != nil {
		t.Fatalf("delete emptied bucket: %v", err)
	}
}

func mkBucket(t *testing.T, base string, hc *http.Client, name string) {
	t.Helper()

	body := `{"name":"` + name + `"}`

	resp, err := hc.Post(base+"/storage/v1/b?project="+e2eProject, "application/json", bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatalf("create bucket %q: %v", name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("create bucket %q status = %d; body: %s", name, resp.StatusCode, raw)
	}
}

func getMediaBytes(t *testing.T, base string, hc *http.Client, bucket, key string) []byte {
	t.Helper()

	resp, err := hc.Get(base + "/storage/v1/b/" + bucket + "/o/" + key + "?alt=media")
	if err != nil {
		t.Fatalf("get media %s/%s: %v", bucket, key, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("get media status = %d; body: %s", resp.StatusCode, raw)
	}

	raw, _ := io.ReadAll(resp.Body)

	return raw
}

func writeObject(t *testing.T, ctx context.Context, bkt *storage.BucketHandle, key, content string) {
	t.Helper()

	w := bkt.Object(key).NewWriter(ctx)
	if _, err := io.WriteString(w, content); err != nil {
		t.Fatalf("write %s: %v", key, err)
	}

	if err := w.Close(); err != nil {
		t.Fatalf("close %s: %v", key, err)
	}
}
