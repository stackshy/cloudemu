package s3

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/server/wire/awsauthz"
)

const (
	arnB = "arn:aws:s3:::b"
	arnK = "arn:aws:s3:::b/k/x"
)

var testScope = awsauthz.Scope{AccountID: "123456789012", Region: "us-east-1", Partition: "aws"}

type iamCase struct {
	name, method, target, body string
	header                     map[string]string
	op                         opID
	// want is "action resource" pairs; nil with unknown=true means ok=false.
	want    []string
	unknown bool
}

func newCaseRequest(tc *iamCase) *http.Request {
	req := httptest.NewRequest(tc.method, tc.target, strings.NewReader(tc.body))
	for k, v := range tc.header {
		req.Header.Set(k, v)
	}

	return req
}

func checkStrings(checks []awsauthz.Check) []string {
	out := make([]string, 0, len(checks))
	for _, c := range checks {
		out = append(out, c.Action+" "+c.Resource)
	}

	return out
}

func iamCases() []iamCase {
	g, p, d, h, post := http.MethodGet, http.MethodPut, http.MethodDelete, http.MethodHead, http.MethodPost
	lockHdr := map[string]string{"X-Amz-Object-Lock-Mode": "GOVERNANCE", "X-Amz-Object-Lock-Retain-Until-Date": "2030-01-01T00:00:00Z"}

	return []iamCase{
		{name: "list buckets", method: g, target: "/", op: opListBuckets, want: []string{"s3:ListAllMyBuckets *"}},
		{name: "list buckets bad method", method: p, target: "/", op: opNotAllowed, unknown: true},
		{name: "create bucket", method: p, target: "/b", op: opCreateBucket, want: []string{"s3:CreateBucket " + arnB}},
		{name: "create bucket with object lock, acl and ownership", method: p, target: "/b", op: opCreateBucket,
			header: map[string]string{"X-Amz-Bucket-Object-Lock-Enabled": "true", "X-Amz-Acl": "private", "X-Amz-Object-Ownership": "BucketOwnerEnforced"},
			want: []string{"s3:CreateBucket " + arnB, "s3:PutBucketObjectLockConfiguration " + arnB, "s3:PutBucketVersioning " + arnB,
				"s3:PutBucketAcl " + arnB, "s3:PutBucketOwnershipControls " + arnB}},
		{name: "create bucket with a grant header", method: p, target: "/b", op: opCreateBucket,
			header: map[string]string{"X-Amz-Grant-Read": "id=abc"},
			want:   []string{"s3:CreateBucket " + arnB, "s3:PutBucketAcl " + arnB}},
		{name: "delete bucket", method: d, target: "/b", op: opDeleteBucket, want: []string{"s3:DeleteBucket " + arnB}},
		{name: "head bucket", method: h, target: "/b", op: opHeadBucket, want: []string{"s3:ListBucket " + arnB}},
		{name: "list objects", method: g, target: "/b", op: opListObjects, want: []string{"s3:ListBucket " + arnB}},
		{name: "list objects v2", method: g, target: "/b?list-type=2&prefix=a", op: opListObjects, want: []string{"s3:ListBucket " + arnB}},
		{name: "bucket bad method", method: http.MethodPatch, target: "/b", op: opNotAllowed, unknown: true},
		{name: "list object versions", method: g, target: "/b?versions", op: opListObjectVersions, want: []string{"s3:ListBucketVersions " + arnB}},
		{name: "versions bad method", method: p, target: "/b?versions", op: opNotAllowed, unknown: true},
		{name: "list multipart uploads", method: g, target: "/b?uploads", op: opListMultipartUploads,
			want: []string{"s3:ListBucketMultipartUploads " + arnB}},
		{name: "uploads bad method", method: d, target: "/b?uploads", op: opNotAllowed, unknown: true},
		{name: "get bucket tagging", method: g, target: "/b?tagging", op: opGetBucketTagging, want: []string{"s3:GetBucketTagging " + arnB}},
		{name: "put bucket tagging", method: p, target: "/b?tagging", op: opPutBucketTagging, want: []string{"s3:PutBucketTagging " + arnB}},
		{name: "delete bucket tagging", method: d, target: "/b?tagging", op: opDeleteBucketTagging, want: []string{"s3:PutBucketTagging " + arnB}},
		{name: "get notification", method: g, target: "/b?notification", op: opGetBucketNotification,
			want: []string{"s3:GetBucketNotification " + arnB}},
		{name: "put notification", method: p, target: "/b?notification", op: opPutBucketNotification,
			want: []string{"s3:PutBucketNotification " + arnB}},
		{name: "notification bad method", method: d, target: "/b?notification", op: opNotificationNotAllowed, unknown: true},
		{name: "get versioning", method: g, target: "/b?versioning", op: opGetBucketVersioning, want: []string{"s3:GetBucketVersioning " + arnB}},
		{name: "put versioning", method: p, target: "/b?versioning", op: opPutBucketVersioning, want: []string{"s3:PutBucketVersioning " + arnB}},
		{name: "get bucket acl", method: g, target: "/b?acl", op: opGetBucketACL, want: []string{"s3:GetBucketAcl " + arnB}},
		{name: "put bucket acl", method: p, target: "/b?acl", op: opPutBucketACL, want: []string{"s3:PutBucketAcl " + arnB}},
		{name: "get policy", method: g, target: "/b?policy", op: opGetBucketConfig, want: []string{"s3:GetBucketPolicy " + arnB}},
		{name: "put policy", method: p, target: "/b?policy", op: opPutBucketConfig, want: []string{"s3:PutBucketPolicy " + arnB}},
		{name: "delete policy", method: d, target: "/b?policy", op: opDeleteBucketConfig, want: []string{"s3:DeleteBucketPolicy " + arnB}},
		{name: "delete cors", method: d, target: "/b?cors", op: opDeleteBucketConfig, want: []string{"s3:PutBucketCORS " + arnB}},
		{name: "get lifecycle", method: g, target: "/b?lifecycle", op: opGetBucketConfig, want: []string{"s3:GetLifecycleConfiguration " + arnB}},
		{name: "delete lifecycle", method: d, target: "/b?lifecycle", op: opDeleteBucketConfig,
			want: []string{"s3:PutLifecycleConfiguration " + arnB}},
		{name: "put encryption", method: p, target: "/b?encryption", op: opPutBucketConfig, want: []string{"s3:PutEncryptionConfiguration " + arnB}},
		{name: "get public access block", method: g, target: "/b?publicAccessBlock", op: opGetBucketConfig,
			want: []string{"s3:GetBucketPublicAccessBlock " + arnB}},
		{name: "delete website", method: d, target: "/b?website", op: opDeleteBucketConfig, want: []string{"s3:DeleteBucketWebsite " + arnB}},
		{name: "get location", method: g, target: "/b?location", op: opGetBucketConfig, want: []string{"s3:GetBucketLocation " + arnB}},
		{name: "get object lock config", method: g, target: "/b?object-lock", op: opGetBucketConfig,
			want: []string{"s3:GetBucketObjectLockConfiguration " + arnB}},
		{name: "put accelerate", method: p, target: "/b?accelerate", op: opPutBucketConfig, want: []string{"s3:PutAccelerateConfiguration " + arnB}},

		{name: "put object", method: p, target: "/b/k/x", op: opPutObject, want: []string{"s3:PutObject " + arnK}},
		{name: "put object with tags, acl and lock", method: p, target: "/b/k/x", op: opPutObject,
			header: map[string]string{"X-Amz-Tagging": "a=b", "X-Amz-Acl": "private", "X-Amz-Object-Lock-Mode": "GOVERNANCE",
				"X-Amz-Object-Lock-Legal-Hold": "ON"},
			want: []string{"s3:PutObject " + arnK, "s3:PutObjectTagging " + arnK, "s3:PutObjectAcl " + arnK,
				"s3:PutObjectRetention " + arnK, "s3:PutObjectLegalHold " + arnK}},
		{name: "get object", method: g, target: "/b/k/x", op: opGetObject, want: []string{"s3:GetObject " + arnK}},
		{name: "get object version", method: g, target: "/b/k/x?versionId=v1", op: opGetObject, want: []string{"s3:GetObjectVersion " + arnK}},
		{name: "head object", method: h, target: "/b/k/x", op: opHeadObject, want: []string{"s3:GetObject " + arnK}},
		{name: "head object version", method: h, target: "/b/k/x?versionId=v1", op: opHeadObject, want: []string{"s3:GetObjectVersion " + arnK}},
		{name: "delete object", method: d, target: "/b/k/x", op: opDeleteObject, want: []string{"s3:DeleteObject " + arnK}},
		{name: "delete object version", method: d, target: "/b/k/x?versionId=v1", op: opDeleteObject,
			want: []string{"s3:DeleteObjectVersion " + arnK}},
		{name: "delete object version bypassing governance", method: d, target: "/b/k/x?versionId=v1", op: opDeleteObject,
			header: map[string]string{"X-Amz-Bypass-Governance-Retention": "true"},
			want:   []string{"s3:DeleteObjectVersion " + arnK, "s3:BypassGovernanceRetention " + arnK}},
		{name: "object bad method", method: post, target: "/b/k/x", op: opNotAllowed, unknown: true},
		{name: "copy object", method: p, target: "/b/k/x", op: opCopyObject, header: map[string]string{"X-Amz-Copy-Source": "/src/a%2Fb.txt"},
			want: []string{"s3:PutObject " + arnK, "s3:GetObject arn:aws:s3:::src/a/b.txt"}},
		{name: "copy object version with replaced tags", method: p, target: "/b/k/x", op: opCopyObject,
			header: map[string]string{"X-Amz-Copy-Source": "src/a?versionId=v9", "X-Amz-Tagging-Directive": "REPLACE", "X-Amz-Tagging": "a=b"},
			want:   []string{"s3:PutObject " + arnK, "s3:PutObjectTagging " + arnK, "s3:GetObjectVersion arn:aws:s3:::src/a"}},
		{name: "copy object keeps source tags", method: p, target: "/b/k/x", op: opCopyObject,
			header: map[string]string{"X-Amz-Copy-Source": "src/a", "X-Amz-Tagging": "a=b"},
			want:   []string{"s3:PutObject " + arnK, "s3:GetObject arn:aws:s3:::src/a"}},
		{name: "copy object with a bad source", method: p, target: "/b/k/x", op: opCopyObject,
			header: map[string]string{"X-Amz-Copy-Source": "/"}, unknown: true},
		{name: "get object attributes", method: g, target: "/b/k/x?attributes", op: opGetObjectAttributes,
			want: []string{"s3:GetObject " + arnK, "s3:GetObjectAttributes " + arnK}},
		{name: "get object attributes of a version", method: g, target: "/b/k/x?attributes&versionId=v1", op: opGetObjectAttributes,
			want: []string{"s3:GetObjectVersion " + arnK, "s3:GetObjectVersionAttributes " + arnK}},
		{name: "attributes bad method", method: p, target: "/b/k/x?attributes", op: opNotAllowed, unknown: true},
		{name: "get object tagging", method: g, target: "/b/k/x?tagging", op: opGetObjectTagging, want: []string{"s3:GetObjectTagging " + arnK}},
		{name: "put object version tagging", method: p, target: "/b/k/x?tagging&versionId=v1", op: opPutObjectTagging,
			want: []string{"s3:PutObjectVersionTagging " + arnK}},
		{name: "delete object tagging", method: d, target: "/b/k/x?tagging", op: opDeleteObjectTagging,
			want: []string{"s3:DeleteObjectTagging " + arnK}},
		{name: "get retention", method: g, target: "/b/k/x?retention", op: opGetObjectRetention, want: []string{"s3:GetObjectRetention " + arnK}},
		{name: "put retention bypassing governance", method: p, target: "/b/k/x?retention", op: opPutObjectRetention,
			header: map[string]string{"X-Amz-Bypass-Governance-Retention": "true"},
			want:   []string{"s3:PutObjectRetention " + arnK, "s3:BypassGovernanceRetention " + arnK}},
		{name: "get legal hold", method: g, target: "/b/k/x?legal-hold", op: opGetObjectLegalHold, want: []string{"s3:GetObjectLegalHold " + arnK}},
		{name: "put legal hold", method: p, target: "/b/k/x?legal-hold", op: opPutObjectLegalHold, want: []string{"s3:PutObjectLegalHold " + arnK}},
		{name: "get object acl", method: g, target: "/b/k/x?acl", op: opGetObjectACL, want: []string{"s3:GetObjectAcl " + arnK}},
		{name: "put object version acl", method: p, target: "/b/k/x?acl&versionId=v1", op: opPutObjectACL,
			want: []string{"s3:PutObjectVersionAcl " + arnK}},
		{name: "create multipart upload", method: post, target: "/b/k/x?uploads", op: opCreateMultipartUpload, header: lockHdr,
			want: []string{"s3:PutObject " + arnK, "s3:PutObjectRetention " + arnK}},
		{name: "object uploads bad method", method: g, target: "/b/k/x?uploads", op: opNotAllowed, unknown: true},
		{name: "upload part", method: p, target: "/b/k/x?uploadId=u&partNumber=1", op: opUploadPart, want: []string{"s3:PutObject " + arnK}},
		{name: "upload part copy", method: p, target: "/b/k/x?uploadId=u&partNumber=1", op: opUploadPartCopy,
			header: map[string]string{"X-Amz-Copy-Source": "src/big"},
			want:   []string{"s3:PutObject " + arnK, "s3:GetObject arn:aws:s3:::src/big"}},
		{name: "complete multipart upload", method: post, target: "/b/k/x?uploadId=u", op: opCompleteMultipart,
			want: []string{"s3:PutObject " + arnK}},
		{name: "abort multipart upload", method: d, target: "/b/k/x?uploadId=u", op: opAbortMultipartUpload,
			want: []string{"s3:AbortMultipartUpload " + arnK}},
		{name: "list parts", method: g, target: "/b/k/x?uploadId=u", op: opListParts, want: []string{"s3:ListMultipartUploadParts " + arnK}},
		{name: "multipart bad method", method: http.MethodPatch, target: "/b/k/x?uploadId=u", op: opNotAllowed, unknown: true},

		{name: "delete objects", method: post, target: "/b?delete", op: opDeleteObjects,
			body: `<Delete><Object><Key>a</Key></Object><Object><Key>c/d</Key><VersionId>v1</VersionId></Object>` +
				`<Object><Key>a</Key></Object></Delete>`,
			want: []string{"s3:DeleteObject arn:aws:s3:::b/a", "s3:DeleteObjectVersion arn:aws:s3:::b/c/d"}},
		{name: "delete objects bypassing governance", method: post, target: "/b?delete", op: opDeleteObjects,
			header: map[string]string{"X-Amz-Bypass-Governance-Retention": "true"},
			body:   `<Delete><Object><Key>a</Key></Object></Delete>`,
			want:   []string{"s3:DeleteObject arn:aws:s3:::b/a", "s3:BypassGovernanceRetention arn:aws:s3:::b/a"}},
		{name: "delete objects with a malformed body", method: post, target: "/b?delete", op: opDeleteObjects, body: "<Delete>", unknown: true},
		{name: "delete objects with no keys", method: post, target: "/b?delete", op: opDeleteObjects, body: "<Delete></Delete>", unknown: true},
	}
}

// TestIAMChecksPerOperation pins the classified operation and the IAM checks
// of every S3 operation.
func TestIAMChecksPerOperation(t *testing.T) {
	h := New(nil)

	for _, tc := range iamCases() {
		t.Run(tc.name, func(t *testing.T) {
			if op, _ := classify(newCaseRequest(&tc)); op != tc.op {
				t.Fatalf("classify = %s, want %s", op, tc.op)
			}

			checks, ok := h.IAMChecks(newCaseRequest(&tc), testScope)
			if tc.unknown {
				if ok {
					t.Fatalf("ok = true with %v, want an unknown operation", checkStrings(checks))
				}

				return
			}

			if !ok {
				t.Fatal("ok = false")
			}

			got := checkStrings(checks)
			if strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
				t.Fatalf("checks:\n %v\nwant:\n %v", got, tc.want)
			}
		})
	}
}

// TestEveryDispatchedOperationHasIAMChecks keeps the dispatch and IAM tables
// in step: every operation that runs has checks, and only the 405 answers
// are unknown to IAM.
func TestEveryDispatchedOperationHasIAMChecks(t *testing.T) {
	for op := range dispatchTable {
		_, hasRule := iamRules[op]
		notAllowed := op == opNotAllowed || op == opNotificationNotAllowed

		if hasRule == notAllowed {
			t.Errorf("%s: has IAM rule = %v", op, hasRule)
		}
	}

	for op := range iamRules {
		if _, ok := dispatchTable[op]; !ok {
			t.Errorf("%s has an IAM rule but is never dispatched", op)
		}
	}

	for _, sub := range configSubresources {
		if sub.get == "" || sub.put == "" {
			t.Errorf("sub-resource %s is missing an IAM action", sub.key)
		}
	}
}

// TestIAMChecksPartition builds ARNs in the server's partition.
func TestIAMChecksPartition(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/b/k", nil)

	checks, ok := New(nil).IAMChecks(req, awsauthz.Scope{Partition: "aws-cn"})
	if !ok || len(checks) != 1 || checks[0].Resource != "arn:aws-cn:s3:::b/k" {
		t.Fatalf("checks = %v, ok = %v", checks, ok)
	}
}

// TestClassifyAddressing resolves the same bucket and key from path-style
// and virtual-hosted requests, and keeps every other Host path-style.
func TestClassifyAddressing(t *testing.T) {
	for _, tc := range []struct {
		host, path, bucket, key string
	}{
		// Virtual-hosted S3 endpoint forms.
		{"data.s3.localhost:4566", "/a/b.txt", "data", "a/b.txt"},
		{"DATA.S3.LOCALHOST", "/a/b.txt", "data", "a/b.txt"},
		{"data.s3.amazonaws.com", "/a/b.txt", "data", "a/b.txt"},
		{"data.s3.us-west-2.amazonaws.com", "/", "data", ""},
		{"data.s3.dualstack.us-west-2.amazonaws.com", "/k", "data", "k"},
		{"data.s3.cn-north-1.amazonaws.com.cn", "/k", "data", "k"},
		{"data.s3-us-west-2.amazonaws.com", "/k", "data", "k"},
		{"my.data.s3.localhost.localstack.cloud:4566", "/k", "my.data", "k"},
		{"my.s3.data.s3.amazonaws.com", "/k", "my.s3.data", "k"},
		// Path-style: S3 endpoints without a bucket label, and every other host.
		{"localhost:4566", "/data/a/b.txt", "data", "a/b.txt"},
		{"127.0.0.1:4566", "/data/a/b.txt", "data", "a/b.txt"},
		{"s3.localhost:4566", "/data/k", "data", "k"},
		{"s3.amazonaws.com", "/data/k", "data", "k"},
		{"s3.us-east-1.amazonaws.com", "/data/k", "data", "k"},
		{"s3.localhost.localstack.cloud:4566", "/data/k", "data", "k"},
		{"localhost", "/", "", ""},
		{"cloudemu.localhost:4566", "/mybkt/k", "mybkt", "k"},
		{"aws.localhost:4566", "/mybkt/k", "mybkt", "k"},
		{"data.localhost:4566", "/mybkt/k", "mybkt", "k"},
		{"minio.s3.internal:9000", "/mybkt/k", "mybkt", "k"},
		{"minio.s3-proxy.internal", "/mybkt/k", "mybkt", "k"},
		{"cloudemu", "/mybkt/k", "mybkt", "k"},
		{"host.docker.internal:4566", "/mybkt/k", "mybkt", "k"},
		{"x.s3.amazonaws.com.evil.example", "/mybkt/k", "mybkt", "k"},
		{"bad_name.s3.localhost", "/mybkt/k", "mybkt", "k"},
		{"ab.s3.localhost", "/mybkt/k", "mybkt", "k"},
	} {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		req.Host = tc.host

		_, a := classify(req)
		if a.bucket != tc.bucket || a.key != tc.key {
			t.Errorf("%s%s: bucket %q key %q, want %q %q", tc.host, tc.path, a.bucket, a.key, tc.bucket, tc.key)
		}
	}
}

// TestIAMChecksFollowTheDispatchedBucket checks a virtual-hosted Host cannot
// point the IAM check at one bucket while the request runs on another: the
// checked ARN is the bucket and key the handler writes to.
func TestIAMChecksFollowTheDispatchedBucket(t *testing.T) {
	req := httptest.NewRequest(http.MethodPut, "/data/k", strings.NewReader("x"))
	req.Host = "other.s3.localhost:4566"

	checks, ok := New(nil).IAMChecks(req, testScope)
	if !ok || len(checks) != 1 || checks[0].Resource != "arn:aws:s3:::other/data/k" {
		t.Fatalf("checks = %v", checks)
	}

	_, a := classify(req)
	if a.bucket != "other" || a.key != "data/k" {
		t.Fatalf("dispatch runs on %s/%s", a.bucket, a.key)
	}
}
