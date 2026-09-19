package blobstore_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/contrib/realengine/blobstore"
	ociserver "github.com/stackshy/cloudemu/v2/server/oci"
)

const ociCompartment = "ocid1.compartment.oc1..aaaaaaaablobstore"

// ociCall issues one Object Storage request against the emulator and fails the
// test on any non-2xx.
func ociCall(t *testing.T, ts *httptest.Server, method, path string, body []byte) []byte {
	t.Helper()

	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(t.Context(), method, ts.URL+path, reader)
	if err != nil {
		t.Fatalf("build %s %s: %v", method, path, err)
	}

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	out, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s %s: %v", method, path, err)
	}

	if resp.StatusCode/100 != 2 {
		t.Fatalf("%s %s: status %d: %s", method, path, resp.StatusCode, out)
	}

	return out
}

// TestOCIObjectStorageBlobstoreE2E runs the real-user flow against OCI Object
// Storage backed by a real filesystem engine (no Docker, no cloud account):
// read the namespace, create a bucket, put an object, get it, head it, copy it
// with the rename action, delete the original and confirm it is gone — then
// read the surviving bytes straight off disk under the engine root, proving
// they flowed through the engine rather than living only in memory.
//
// The requests are hand-built rather than driven by github.com/oracle/oci-go-sdk
// because that client mandates a signed request with an RSA keypair and a
// ConfigurationProvider, which the emulator does not verify; the wire shape is
// what this test is about.
func TestOCIObjectStorageBlobstoreE2E(t *testing.T) {
	eng := blobstore.New("")
	t.Cleanup(func() { _ = eng.Close() })

	cloud := cloudemu.NewOCI(
		config.WithStorageEngine(eng),
		config.WithCompartmentID(ociCompartment),
	)
	ts := httptest.NewServer(ociserver.New(ociserver.Drivers{
		ObjectStorage: cloud.ObjectStorage,
		CompartmentID: cloud.CompartmentID,
		TenancyOCID:   cloud.TenancyOCID,
		Region:        cloud.Region,
	}))
	t.Cleanup(ts.Close)

	var namespace string
	if err := json.Unmarshal(ociCall(t, ts, http.MethodGet, "/n", nil), &namespace); err != nil {
		t.Fatalf("decode namespace: %v", err)
	}

	const (
		bucket = "blob-bucket"
		object = "docs/greeting.txt"
		moved  = "docs/greeting-moved.txt"
	)

	body := []byte("hello from the real blobstore engine")
	root := "/n/" + namespace + "/b"

	spec, err := json.Marshal(map[string]string{"name": bucket, "compartmentId": ociCompartment})
	if err != nil {
		t.Fatalf("marshal bucket spec: %v", err)
	}

	ociCall(t, ts, http.MethodPost, root, spec)
	ociCall(t, ts, http.MethodPut, root+"/"+bucket+"/o/"+object, body)

	if got := ociCall(t, ts, http.MethodGet, root+"/"+bucket+"/o/"+object, nil); !bytes.Equal(got, body) {
		t.Fatalf("object round-trip mismatch: got %q want %q", got, body)
	}

	var listed struct {
		Objects []struct {
			Name string `json:"name"`
			Size int64  `json:"size"`
		} `json:"objects"`
	}

	if err := json.Unmarshal(ociCall(t, ts, http.MethodGet, root+"/"+bucket+"/o", nil), &listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}

	if len(listed.Objects) != 1 || listed.Objects[0].Size != int64(len(body)) {
		t.Fatalf("list must report the real size after the offload: %+v", listed.Objects)
	}

	rename, err := json.Marshal(map[string]string{"sourceName": object, "newName": moved})
	if err != nil {
		t.Fatalf("marshal rename: %v", err)
	}

	ociCall(t, ts, http.MethodPost, root+"/"+bucket+"/actions/renameObject", rename)

	if got := ociCall(t, ts, http.MethodGet, root+"/"+bucket+"/o/"+moved, nil); !bytes.Equal(got, body) {
		t.Fatalf("renamed object mismatch: got %q want %q", got, body)
	}

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, ts.URL+root+"/"+bucket+"/o/"+object, nil)
	if err != nil {
		t.Fatalf("build get: %v", err)
	}

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("get deleted source: %v", err)
	}

	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for the renamed-away source, got %d", resp.StatusCode)
	}

	assertEngineFileMatches(t, eng, bucket, moved, body)
}
