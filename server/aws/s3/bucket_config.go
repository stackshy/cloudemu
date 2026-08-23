package s3

import (
	"encoding/xml"
	"io"
	"net/http"
	"net/url"

	"github.com/stackshy/cloudemu/v2/server/wire"
)

// maxConfigBody caps a bucket-configuration document. Real S3 bucket policies
// top out at 20 KB; this is a generous ceiling covering cors/lifecycle/website
// XML as well.
const maxConfigBody = 2 << 20

// notConfiguredErr maps a read-only bucket configuration sub-resource (query
// key) to the AWS error code S3 returns when the bucket has no such
// configuration. The Terraform aws_s3_bucket resource reads every one of these
// right after create; without them the request falls through to ListObjects and
// the provider fails to parse the response.
//
//nolint:gochecknoglobals // static lookup table
var notConfiguredErr = map[string]string{
	"policy":            "NoSuchBucketPolicy",
	"cors":              "NoSuchCORSConfiguration",
	"website":           "NoSuchWebsiteConfiguration",
	"lifecycle":         "NoSuchLifecycleConfiguration",
	"replication":       "ReplicationConfigurationNotFoundError",
	"encryption":        "ServerSideEncryptionConfigurationNotFoundError",
	"object-lock":       "ObjectLockConfigurationNotFoundError",
	"publicAccessBlock": "NoSuchPublicAccessBlockConfiguration",
	"ownershipControls": "OwnershipControlsNotFoundError",
}

// configSubresources are the read-only bucket configuration sub-resource query
// keys the handler answers (order irrelevant — at most one is present).
//
//nolint:gochecknoglobals // static set
var configSubresources = []string{
	"policy", "cors", "website", "lifecycle", "replication", "encryption",
	"object-lock", "publicAccessBlock", "ownershipControls",
	"requestPayment", "accelerate", "logging", "location", "policyStatus",
}

// configSubresourceKey returns the read-only config sub-resource query key
// present on the request, or "" if none.
func configSubresourceKey(q url.Values) string {
	for _, k := range configSubresources {
		if q.Has(k) {
			return k
		}
	}

	return ""
}

// bucketConfigOp answers a bucket configuration sub-resource. When the driver
// implements RawBucketConfig (real S3 semantics), PUT persists the document,
// GET echoes it back, and DELETE removes it — so aws_s3_bucket_policy,
// _cors_configuration, _server_side_encryption_configuration, _lifecycle_* and
// _website read back what was written instead of a perpetual "not configured"
// diff. GET on an unconfigured sub-resource still returns the AWS-correct
// "not configured"/default response.
//
// Without the RawBucketConfig capability a write is accepted as a no-op (so it
// does not fall through to create/delete the bucket) and reads return defaults.
func (h *Handler) bucketConfigOp(w http.ResponseWriter, r *http.Request, bucket, sub string) {
	switch r.Method {
	case http.MethodPut:
		h.putBucketConfig(w, r, bucket, sub)
	case http.MethodDelete:
		h.deleteBucketConfig(w, r, bucket, sub)
	default:
		h.getBucketConfig(w, r, bucket, sub)
	}
}

// putBucketConfig persists a configuration document when the driver supports it,
// otherwise accepts the write as a no-op.
func (h *Handler) putBucketConfig(w http.ResponseWriter, r *http.Request, bucket, sub string) {
	if h.rawConfig == nil {
		w.WriteHeader(http.StatusOK)
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxConfigBody))
	if err != nil {
		writeError(w, http.StatusBadRequest, "MalformedXML", "could not read request body")
		return
	}

	if err := h.rawConfig.PutBucketConfig(r.Context(), bucket, sub, body); err != nil {
		writeErr(w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// deleteBucketConfig removes a stored configuration document (204), matching
// DeleteBucketCors/Policy/Website/Encryption/Lifecycle.
func (h *Handler) deleteBucketConfig(w http.ResponseWriter, r *http.Request, bucket, sub string) {
	if h.rawConfig != nil {
		if err := h.rawConfig.DeleteBucketConfig(r.Context(), bucket, sub); err != nil {
			writeErr(w, err)
			return
		}
	}

	w.WriteHeader(http.StatusNoContent)
}

// getBucketConfig echoes a stored document, or returns the AWS "not
// configured"/default response when the sub-resource was never set.
func (h *Handler) getBucketConfig(w http.ResponseWriter, r *http.Request, bucket, sub string) {
	if h.rawConfig != nil {
		if body, err := h.rawConfig.GetBucketConfig(r.Context(), bucket, sub); err == nil {
			writeRawBucketConfig(w, sub, body)
			return
		}
	}

	writeConfigDefault(w, sub)
}

// writeRawBucketConfig echoes a persisted configuration document verbatim with
// the sub-resource's content type (policy is JSON; the rest are XML).
func writeRawBucketConfig(w http.ResponseWriter, sub string, body []byte) {
	contentType := "application/xml"
	if sub == "policy" {
		contentType = "application/json"
	}

	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body) //nolint:gosec // echoing a stored config document, not HTML
}

// writeConfigDefault returns the AWS-correct response for an unconfigured
// sub-resource: a "not configured" error for the persistable ones, or the
// service default for the always-present ones (location, request payment, …).
func writeConfigDefault(w http.ResponseWriter, sub string) {
	if code, ok := notConfiguredErr[sub]; ok {
		writeError(w, http.StatusNotFound, code, "The "+sub+" configuration does not exist")
		return
	}

	switch sub {
	case "requestPayment":
		wire.WriteXML(w, http.StatusOK, requestPaymentXML{Xmlns: xmlns, Payer: "BucketOwner"})
	case "accelerate":
		wire.WriteXML(w, http.StatusOK, accelerateXML{Xmlns: xmlns})
	case "logging":
		wire.WriteXML(w, http.StatusOK, loggingXML{Xmlns: xmlns})
	case "location":
		// An empty LocationConstraint denotes us-east-1.
		wire.WriteXML(w, http.StatusOK, locationXML{Xmlns: xmlns})
	case "policyStatus":
		wire.WriteXML(w, http.StatusOK, policyStatusXML{Xmlns: xmlns, IsPublic: false})
	default:
		w.WriteHeader(http.StatusOK)
	}
}

type requestPaymentXML struct {
	XMLName xml.Name `xml:"RequestPaymentConfiguration"`
	Xmlns   string   `xml:"xmlns,attr"`
	Payer   string   `xml:"Payer"`
}

type accelerateXML struct {
	XMLName xml.Name `xml:"AccelerateConfiguration"`
	Xmlns   string   `xml:"xmlns,attr"`
}

type loggingXML struct {
	XMLName xml.Name `xml:"BucketLoggingStatus"`
	Xmlns   string   `xml:"xmlns,attr"`
}

type locationXML struct {
	XMLName xml.Name `xml:"LocationConstraint"`
	Xmlns   string   `xml:"xmlns,attr"`
}

type policyStatusXML struct {
	XMLName  xml.Name `xml:"PolicyStatus"`
	Xmlns    string   `xml:"xmlns,attr"`
	IsPublic bool     `xml:"IsPublic"`
}
