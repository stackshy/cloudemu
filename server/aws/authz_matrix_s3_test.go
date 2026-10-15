package aws

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"

	awsprovider "github.com/stackshy/cloudemu/v2/providers/aws"
)

const (
	s3ReaderPolicy = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":["s3:GetObject","s3:ListBucket"],` +
		`"Resource":["arn:aws:s3:::data","arn:aws:s3:::data/*"]}]}`
	s3CopierPolicy = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":["s3:GetObject","s3:PutObject"],` +
		`"Resource":"arn:aws:s3:::data/*"}]}`
	s3TmpDeleterPolicy = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:DeleteObject",` +
		`"Resource":"arn:aws:s3:::data/tmp/*"}]}`
	s3DenySecretPolicy = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:*","Resource":"*"},` +
		`{"Effect":"Deny","Action":"s3:GetObject","Resource":"arn:aws:s3:::data/secret*"}]}`
	s3XMLDenied = "<Error><Code>AccessDenied</Code>"
)

// seedS3 creates the data and other buckets with a few objects.
func seedS3(t *testing.T, cloud *awsprovider.Provider) {
	t.Helper()

	ctx := context.Background()

	for _, b := range []string{"data", "other"} {
		if err := cloud.S3.CreateBucket(ctx, b); err != nil {
			t.Fatalf("CreateBucket %s: %v", b, err)
		}
	}

	for _, o := range []struct{ bucket, key string }{
		{"data", "obj"}, {"data", "secret.txt"}, {"data", "tmp/a"}, {"data", "tmp/b"}, {"other", "secret"},
	} {
		if err := cloud.S3.PutObject(ctx, o.bucket, o.key, []byte("body-of-"+o.key), "text/plain", nil); err != nil {
			t.Fatalf("PutObject %s/%s: %v", o.bucket, o.key, err)
		}
	}
}

func objectExists(t *testing.T, cloud *awsprovider.Provider, bucket, key string) bool {
	t.Helper()

	_, err := cloud.S3.HeadObject(context.Background(), bucket, key)

	return err == nil
}

func wantStatus(t *testing.T, status int, body string, want int) {
	t.Helper()

	if status != want {
		t.Fatalf("status %d, body %s; want %d", status, body, want)
	}
}

// TestAuthzMatrixS3 authorizes S3 per operation, on the bucket and object
// ARNs the request names, for path-style and virtual-hosted addressing.
func TestAuthzMatrixS3(t *testing.T) {
	ts, cloud := matrixServer(t, nil)
	seedS3(t, cloud)

	reader := userWithPolicy(t, cloud, "s3reader", s3ReaderPolicy)
	host := strings.TrimPrefix(ts.URL, "http://")

	for _, style := range []struct {
		name string
		at   func(bucket, rest string) sreq
	}{
		{"path-style", func(bucket, rest string) sreq { return sreq{path: "/" + bucket + rest, service: "s3"} }},
		{"virtual-hosted", func(bucket, rest string) sreq {
			if rest == "" {
				rest = "/"
			}

			return sreq{path: rest, host: bucket + ".s3.localhost:" + strings.Split(host, ":")[1], service: "s3"}
		}},
	} {
		t.Run(style.name, func(t *testing.T) {
			rq := style.at("data", "/obj")
			rq.method = http.MethodGet
			status, body := doSigned(t, ts, reader, rq)
			wantStatus(t, status, body, http.StatusOK)

			if body != "body-of-obj" {
				t.Fatalf("GetObject body %q", body)
			}

			rq = style.at("data", "?list-type=2")
			rq.method = http.MethodGet
			status, body = doSigned(t, ts, reader, rq)
			wantStatus(t, status, body, http.StatusOK)

			rq = style.at("data", "/new")
			rq.method, rq.body = http.MethodPut, "x"
			status, body = doSigned(t, ts, reader, rq)
			wantDenied(t, status, body, "s3:PutObject on resource: arn:aws:s3:::data/new")

			rq = style.at("readers-bucket", "")
			rq.method = http.MethodPut
			status, body = doSigned(t, ts, reader, rq)
			wantDenied(t, status, body, "s3:CreateBucket on resource: arn:aws:s3:::readers-bucket")

			rq = style.at("other", "/secret")
			rq.method = http.MethodGet
			status, body = doSigned(t, ts, reader, rq)
			wantDenied(t, status, body, "s3:GetObject on resource: arn:aws:s3:::other/secret")

			rq = style.at("data", "/copied")
			rq.method, rq.header = http.MethodPut, map[string]string{"X-Amz-Copy-Source": "/other/secret"}
			status, body = doSigned(t, ts, reader, rq)
			wantDenied(t, status, body, s3XMLDenied)

			if objectExists(t, cloud, "data", "new") || objectExists(t, cloud, "data", "copied") ||
				bucketExists(t, cloud, "readers-bucket") {
				t.Fatal("a denied request changed S3 state")
			}
		})
	}

	t.Run("a Host naming another bucket is checked against that bucket", func(t *testing.T) {
		status, body := doSigned(t, ts, reader, sreq{method: http.MethodGet, path: "/data/obj", service: "s3",
			host: "other.s3.localhost:" + strings.Split(host, ":")[1]})
		wantDenied(t, status, body, "s3:GetObject on resource: arn:aws:s3:::other/data/obj")
	})

	t.Run("an unknown operation is denied to a restricted caller", func(t *testing.T) {
		status, body := doSigned(t, ts, reader, sreq{method: http.MethodPatch, path: "/data", service: "s3"})
		wantDenied(t, status, body, "s3:UnknownOperation")
	})

	t.Run("an unknown operation answers 405 for the bootstrap caller", func(t *testing.T) {
		boot := userWithPolicy(t, cloud, "s3boot", "")
		before := providerState(t, cloud)

		for _, rq := range []sreq{
			{method: http.MethodPatch, path: "/data", service: "s3"},
			{method: http.MethodPost, path: "/data/obj", service: "s3"},
			{method: http.MethodDelete, path: "/data?versions", service: "s3"},
		} {
			status, body := doSigned(t, ts, boot, rq)
			wantStatus(t, status, body, http.StatusMethodNotAllowed)
		}

		if after := providerState(t, cloud); string(before) != string(after) {
			t.Fatal("an unknown S3 operation changed backend state")
		}
	})
}

// TestAuthzMatrixS3Copy checks both sides of a copy: s3:PutObject on the
// destination and s3:GetObject on the source.
func TestAuthzMatrixS3Copy(t *testing.T) {
	ts, cloud := matrixServer(t, nil)
	seedS3(t, cloud)

	copier := userWithPolicy(t, cloud, "copier", s3CopierPolicy)

	status, body := doSigned(t, ts, copier, sreq{method: http.MethodPut, path: "/data/copy-ok", service: "s3",
		header: map[string]string{"X-Amz-Copy-Source": "/data/obj"}})
	wantStatus(t, status, body, http.StatusOK)

	status, body = doSigned(t, ts, copier, sreq{method: http.MethodPut, path: "/data/stolen", service: "s3",
		header: map[string]string{"X-Amz-Copy-Source": "/other/secret"}})
	wantDenied(t, status, body, "s3:GetObject on resource: arn:aws:s3:::other/secret")

	status, body = doSigned(t, ts, copier, sreq{method: http.MethodPut, path: "/data/old?uploadId=u&partNumber=1", service: "s3",
		header: map[string]string{"X-Amz-Copy-Source": "/other/secret"}})
	wantDenied(t, status, body, "s3:GetObject on resource: arn:aws:s3:::other/secret")

	if objectExists(t, cloud, "data", "stolen") {
		t.Fatal("a denied copy wrote the object")
	}

	// A tagged source copied with the default COPY tagging directive also
	// needs s3:GetObjectTagging on the source and s3:PutObjectTagging on the
	// destination; replacing the tags does not read them.
	if err := cloud.S3.PutObjectTagging(context.Background(), "data", "tmp/a", map[string]string{"k": "v"}); err != nil {
		t.Fatalf("PutObjectTagging: %v", err)
	}

	status, body = doSigned(t, ts, copier, sreq{method: http.MethodPut, path: "/data/tag-copy", service: "s3",
		header: map[string]string{"X-Amz-Copy-Source": "/data/tmp/a"}})
	wantDenied(t, status, body, "s3:GetObjectTagging on resource: arn:aws:s3:::data/tmp/a")

	status, body = doSigned(t, ts, copier, sreq{method: http.MethodPut, path: "/data/tag-replace", service: "s3",
		header: map[string]string{"X-Amz-Copy-Source": "/data/tmp/a", "X-Amz-Tagging-Directive": "REPLACE"}})
	wantStatus(t, status, body, http.StatusOK)

	tagger := userWithPolicy(t, cloud, "tagcopier", `{"Version":"2012-10-17","Statement":[{"Effect":"Allow",`+
		`"Action":["s3:GetObject","s3:PutObject","s3:GetObjectTagging","s3:PutObjectTagging"],"Resource":"arn:aws:s3:::data/*"}]}`)
	status, body = doSigned(t, ts, tagger, sreq{method: http.MethodPut, path: "/data/tag-copy", service: "s3",
		header: map[string]string{"X-Amz-Copy-Source": "/data/tmp/a"}})
	wantStatus(t, status, body, http.StatusOK)
}

// TestAuthzMatrixS3Delete covers DeleteObjects per key, the version and
// governance-bypass actions, and a resource-scoped Deny under s3:*.
func TestAuthzMatrixS3Delete(t *testing.T) {
	ts, cloud := matrixServer(t, nil)
	seedS3(t, cloud)

	deleter := userWithPolicy(t, cloud, "tmpdeleter", s3TmpDeleterPolicy)
	batch := func(keys ...string) sreq {
		var b strings.Builder

		b.WriteString("<Delete>")

		for _, k := range keys {
			b.WriteString("<Object><Key>" + k + "</Key></Object>")
		}

		b.WriteString("</Delete>")

		return sreq{method: http.MethodPost, path: "/data?delete", service: "s3", body: b.String()}
	}

	t.Run("one denied key denies the whole request", func(t *testing.T) {
		status, body := doSigned(t, ts, deleter, batch("tmp/a", "obj"))
		wantDenied(t, status, body, "s3:DeleteObject on resource: arn:aws:s3:::data/obj")

		if !objectExists(t, cloud, "data", "tmp/a") || !objectExists(t, cloud, "data", "obj") {
			t.Fatal("a denied DeleteObjects deleted keys")
		}
	})

	t.Run("allowed keys are deleted", func(t *testing.T) {
		status, body := doSigned(t, ts, deleter, batch("tmp/a", "tmp/b"))
		wantStatus(t, status, body, http.StatusOK)

		if objectExists(t, cloud, "data", "tmp/a") {
			t.Fatal("tmp/a was not deleted")
		}
	})

	t.Run("a version delete needs s3:DeleteObjectVersion", func(t *testing.T) {
		status, body := doSigned(t, ts, deleter, sreq{method: http.MethodDelete, path: "/data/tmp/b?versionId=v1", service: "s3"})
		wantDenied(t, status, body, "s3:DeleteObjectVersion")
	})

	t.Run("bypassing governance needs s3:BypassGovernanceRetention", func(t *testing.T) {
		status, body := doSigned(t, ts, deleter, sreq{method: http.MethodDelete, path: "/data/tmp/b", service: "s3",
			header: map[string]string{"X-Amz-Bypass-Governance-Retention": "true"}})
		wantDenied(t, status, body, "s3:BypassGovernanceRetention")
	})

	t.Run("a resource-scoped deny under s3:*", func(t *testing.T) {
		u := userWithPolicy(t, cloud, "nosecret", s3DenySecretPolicy)

		status, body := doSigned(t, ts, u, sreq{method: http.MethodGet, path: "/data/secret.txt", service: "s3"})
		wantDenied(t, status, body, "with an explicit deny")

		status, body = doSigned(t, ts, u, sreq{method: http.MethodGet, path: "/data/obj", service: "s3"})
		wantStatus(t, status, body, http.StatusOK)
	})

	t.Run("GetObjectAttributes needs s3:GetObjectAttributes too", func(t *testing.T) {
		reader := userWithPolicy(t, cloud, "attrreader", s3ReaderPolicy)
		status, body := doSigned(t, ts, reader, sreq{method: http.MethodGet, path: "/data/obj?attributes", service: "s3",
			header: map[string]string{"X-Amz-Object-Attributes": "ETag"}})
		wantDenied(t, status, body, "s3:GetObjectAttributes")
	})
}

// TestAuthzMatrixS3MultipartKeyBinding is the reviewer's repro: a user
// allowed only shared/public/* cannot use another user's upload ID through a
// key it is allowed, to read, steal (complete into its own key) or abort an
// upload of private/victim.
func TestAuthzMatrixS3MultipartKeyBinding(t *testing.T) {
	ts, cloud := matrixServer(t, nil)
	ctx := context.Background()

	if err := cloud.S3.CreateBucket(ctx, "data"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}

	mp, err := cloud.S3.CreateMultipartUpload(ctx, "data", "private/victim", "text/plain")
	if err != nil {
		t.Fatalf("CreateMultipartUpload: %v", err)
	}

	part, err := cloud.S3.UploadPart(ctx, "data", "private/victim", mp.UploadID, 1, []byte("victim-bytes"))
	if err != nil {
		t.Fatalf("UploadPart: %v", err)
	}

	u := userWithPolicy(t, cloud, "publicwriter", `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:*",`+
		`"Resource":"arn:aws:s3:::data/shared/public/*"}]}`)
	at := "/data/shared/public/mine?uploadId=" + mp.UploadID

	for _, rq := range []sreq{
		{method: http.MethodGet, path: at, service: "s3"},
		{method: http.MethodPut, path: at + "&partNumber=2", service: "s3", body: "x"},
		{method: http.MethodPost, path: at, service: "s3", body: `<CompleteMultipartUpload><Part><PartNumber>1</PartNumber>` +
			`<ETag>"` + part.ETag + `"</ETag></Part></CompleteMultipartUpload>`},
		{method: http.MethodDelete, path: at, service: "s3"},
	} {
		status, body := doSigned(t, ts, u, rq)
		if status != http.StatusNotFound || !strings.Contains(body, "NoSuchUpload") {
			t.Fatalf("%s %s: %d %s, want 404 NoSuchUpload", rq.method, rq.path, status, body)
		}
	}

	if objectExists(t, cloud, "data", "shared/public/mine") {
		t.Fatal("the victim's upload was completed into the attacker's key")
	}

	parts, err := cloud.S3.ListParts(ctx, "data", "private/victim", mp.UploadID)
	if err != nil || len(parts) != 1 {
		t.Fatalf("victim upload changed: %v parts, err %v", len(parts), err)
	}
}

// presigned sends a SigV4 query-string (presigned URL) request.
func presigned(t *testing.T, ts *httptest.Server, creds aws.Credentials, method, path string) (int, string) {
	t.Helper()

	ctx := context.Background()

	req, err := http.NewRequestWithContext(ctx, method, ts.URL+path+"?X-Amz-Expires=300", http.NoBody)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	signed, _, err := v4.NewSigner().PresignHTTP(ctx, creds, req, "UNSIGNED-PAYLOAD", "s3", "us-east-1", time.Now())
	if err != nil {
		t.Fatalf("presign: %v", err)
	}

	preq, err := http.NewRequestWithContext(ctx, method, signed, http.NoBody)
	if err != nil {
		t.Fatalf("new presigned request: %v", err)
	}

	resp, err := http.DefaultClient.Do(preq)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()

	b, _ := io.ReadAll(resp.Body)

	return resp.StatusCode, string(b)
}

// TestAuthzMatrixS3Presigned authorizes presigned URLs like signed requests.
func TestAuthzMatrixS3Presigned(t *testing.T) {
	ts, cloud := matrixServer(t, nil)
	seedS3(t, cloud)

	reader := userWithPolicy(t, cloud, "presignreader", s3ReaderPolicy)

	status, body := presigned(t, ts, reader, http.MethodGet, "/data/obj")
	wantStatus(t, status, body, http.StatusOK)

	status, body = presigned(t, ts, reader, http.MethodGet, "/other/secret")
	wantDenied(t, status, body, "s3:GetObject on resource: arn:aws:s3:::other/secret")

	status, body = presigned(t, ts, reader, http.MethodPut, "/data/presigned-put")
	wantDenied(t, status, body, "s3:PutObject")

	if objectExists(t, cloud, "data", "presigned-put") {
		t.Fatal("a denied presigned PUT wrote the object")
	}
}
