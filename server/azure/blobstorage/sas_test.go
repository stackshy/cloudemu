package blobstorage_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// sasQuery builds a service-SAS query string with the given permissions and
// expiry. The signature is a placeholder: cloudemu does not verify SAS
// signatures cryptographically (the wire layer accepts any credentials), it
// only enforces the permission set and validity window.
func sasQuery(perms string, expiry time.Time) string {
	return "sv=2023-11-03&sr=b&sp=" + perms +
		"&se=" + expiry.UTC().Format("2006-01-02T15:04:05Z") +
		"&sig=placeholder-signature"
}

// doSAS issues a raw HTTP request through the test transport (bypassing the
// azblob client, which would require a real credential to mint a SAS).
func doSAS(t *testing.T, e *blobEnv, method, url, body string) *http.Response {
	t.Helper()

	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}

	req, err := http.NewRequestWithContext(context.Background(), method, url, rdr)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}

	if method == http.MethodPut {
		req.Header.Set("x-ms-blob-type", "BlockBlob")
	}

	resp, err := e.tr.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}

	return resp
}

// TestSDKReadOnlySASCannotDeleteOrOverwrite checks that a read-only SAS
// (sp=r) authorizes a read but is rejected (403 AuthorizationPermissionMismatch)
// for delete and overwrite — the whole point of least-privilege SAS scoping.
func TestSDKReadOnlySASCannotDeleteOrOverwrite(t *testing.T) {
	e := newBlobEnv(t)
	ctx := context.Background()

	if _, err := e.svc.UploadBuffer(ctx, "c1", "k1", []byte("v1"), nil); err != nil {
		t.Fatalf("UploadBuffer: %v", err)
	}

	readSAS := e.base + "/c1/k1?" + sasQuery("r", time.Now().Add(time.Hour))

	// Read is allowed.
	resp := doSAS(t, e, http.MethodGet, readSAS, "")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("read-only SAS GET status = %d, want 200 (code=%s)", resp.StatusCode, resp.Header.Get("X-Ms-Error-Code"))
	}
	_ = resp.Body.Close()

	// Delete is rejected.
	resp = doSAS(t, e, http.MethodDelete, readSAS, "")
	if resp.StatusCode != http.StatusForbidden || resp.Header.Get("X-Ms-Error-Code") != "AuthorizationPermissionMismatch" {
		t.Errorf("read-only SAS DELETE = %d/%s, want 403/AuthorizationPermissionMismatch",
			resp.StatusCode, resp.Header.Get("X-Ms-Error-Code"))
	}
	_ = resp.Body.Close()

	// Overwrite is rejected.
	resp = doSAS(t, e, http.MethodPut, readSAS, "hacked")
	if resp.StatusCode != http.StatusForbidden || resp.Header.Get("X-Ms-Error-Code") != "AuthorizationPermissionMismatch" {
		t.Errorf("read-only SAS PUT = %d/%s, want 403/AuthorizationPermissionMismatch",
			resp.StatusCode, resp.Header.Get("X-Ms-Error-Code"))
	}
	_ = resp.Body.Close()

	// The blob must be untouched by the rejected mutations.
	if got := e.download(t, "k1"); got != "v1" {
		t.Errorf("blob content = %q, want v1 (rejected SAS write must not overwrite)", got)
	}
}

// TestSDKExpiredSASRejected checks that a SAS whose expiry (se) is in the past
// is rejected with 403 AuthenticationFailed even for a read.
func TestSDKExpiredSASRejected(t *testing.T) {
	e := newBlobEnv(t)
	ctx := context.Background()

	if _, err := e.svc.UploadBuffer(ctx, "c1", "k1", []byte("v1"), nil); err != nil {
		t.Fatalf("UploadBuffer: %v", err)
	}

	expiredSAS := e.base + "/c1/k1?" + sasQuery("r", time.Now().Add(-time.Hour))

	resp := doSAS(t, e, http.MethodGet, expiredSAS, "")
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	if resp.StatusCode != http.StatusForbidden || resp.Header.Get("X-Ms-Error-Code") != "AuthenticationFailed" {
		t.Errorf("expired SAS GET = %d/%s, want 403/AuthenticationFailed",
			resp.StatusCode, resp.Header.Get("X-Ms-Error-Code"))
	}
}

// TestSDKReadWriteSASAllowsDelete checks the permission grant works in the
// affirmative direction too: a SAS with delete permission (sp=rd) is allowed
// to delete.
func TestSDKReadWriteSASAllowsDelete(t *testing.T) {
	e := newBlobEnv(t)
	ctx := context.Background()

	if _, err := e.svc.UploadBuffer(ctx, "c1", "k1", []byte("v1"), nil); err != nil {
		t.Fatalf("UploadBuffer: %v", err)
	}

	rdSAS := e.base + "/c1/k1?" + sasQuery("rd", time.Now().Add(time.Hour))

	resp := doSAS(t, e, http.MethodDelete, rdSAS, "")
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("sp=rd SAS DELETE status = %d, want 202 (code=%s)", resp.StatusCode, resp.Header.Get("X-Ms-Error-Code"))
	}
}
