package s3

import (
	"encoding/xml"
	"io"
	"net/http"
	"net/url"

	"github.com/stackshy/cloudemu/v2/server/wire"
)

// subLocation is the ?location sub-resource key (GetBucketLocation).
const subLocation = "location"

// subLifecycle is the ?lifecycle sub-resource key (Put/GetBucketLifecycleConfiguration).
const subLifecycle = "lifecycle"

// maxConfigBody caps a bucket-configuration document. Real S3 bucket policies
// top out at 20 KB; this is a generous ceiling covering cors/lifecycle/website
// XML as well.
const maxConfigBody = 2 << 20

// transitionDefaultMinimumObjectSizeHeader is the request/response header real
// S3 uses to carry PutBucketLifecycleConfiguration's
// TransitionDefaultMinimumObjectSize setting. Unlike every other lifecycle
// field it travels as a header rather than XML body, so the byte-for-byte raw
// echo in writeRawBucketConfig never sees it — it needs separate capture and
// echo alongside the stored document.
const transitionDefaultMinimumObjectSizeHeader = "X-Amz-Transition-Default-Minimum-Object-Size"

// transitionDefaultMinimumObjectSizeKey is the internal RawBucketConfig
// sub-resource name used to persist the header value alongside the lifecycle
// document. It is not a real S3 query-string sub-resource, just a bookkeeping
// key in the same opaque per-bucket document store.
const transitionDefaultMinimumObjectSizeKey = "lifecycle-transition-default-minimum-object-size"

// transitionDefaultMinimumObjectSizeDefault is the value real S3 applies to a
// general purpose bucket's lifecycle configuration when the header is omitted
// from the request.
const transitionDefaultMinimumObjectSizeDefault = "all_storage_classes_128K"

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
	subLifecycle:        "NoSuchLifecycleConfiguration",
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
	"policy", "cors", "website", subLifecycle, "replication", "encryption",
	"object-lock", "publicAccessBlock", "ownershipControls",
	"requestPayment", "accelerate", "logging", subLocation, "policyStatus",
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

	if sub == subLifecycle {
		minSize := r.Header.Get(transitionDefaultMinimumObjectSizeHeader)
		if minSize == "" {
			minSize = transitionDefaultMinimumObjectSizeDefault
		}

		if err := h.rawConfig.PutBucketConfig(r.Context(), bucket, transitionDefaultMinimumObjectSizeKey, []byte(minSize)); err != nil {
			writeErr(w, err)
			return
		}

		w.Header().Set(transitionDefaultMinimumObjectSizeHeader, minSize)
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

		if sub == subLifecycle {
			_ = h.rawConfig.DeleteBucketConfig(r.Context(), bucket, transitionDefaultMinimumObjectSizeKey)
		}
	}

	w.WriteHeader(http.StatusNoContent)
}

// getBucketConfig echoes a stored document, or returns the AWS "not
// configured"/default response when the sub-resource was never set.
func (h *Handler) getBucketConfig(w http.ResponseWriter, r *http.Request, bucket, sub string) {
	// GetBucketLocation reports the region the bucket was created in
	// (CreateBucketConfiguration.LocationConstraint); us-east-1 is the empty
	// constraint. It is derived from bucket state, never a stored document.
	if sub == subLocation {
		if !h.bucketExists(r.Context(), bucket) {
			writeError(w, http.StatusNotFound, "NoSuchBucket", "The specified bucket does not exist")
			return
		}

		region := h.bucketRegion(r.Context(), bucket)
		if region == usEast1 {
			region = ""
		}

		wire.WriteXML(w, http.StatusOK, locationXML{Xmlns: xmlns, Location: region})

		return
	}

	if h.rawConfig != nil {
		if body, err := h.rawConfig.GetBucketConfig(r.Context(), bucket, sub); err == nil {
			if sub == subLifecycle {
				w.Header().Set(transitionDefaultMinimumObjectSizeHeader, h.lifecycleTransitionMinSize(r, bucket))
			}

			writeRawBucketConfig(w, sub, body)

			return
		}
	}

	writeConfigDefault(w, sub)
}

// lifecycleTransitionMinSize returns the TransitionDefaultMinimumObjectSize
// value stored alongside the bucket's lifecycle document, falling back to the
// real-S3 default for a general purpose bucket when none was ever recorded
// (e.g. a document written before this side-channel existed).
func (h *Handler) lifecycleTransitionMinSize(r *http.Request, bucket string) string {
	stored, err := h.rawConfig.GetBucketConfig(r.Context(), bucket, transitionDefaultMinimumObjectSizeKey)
	if err != nil {
		return transitionDefaultMinimumObjectSizeDefault
	}

	return string(stored)
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
	case subLocation:
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
	// Location is the region name (character data); empty denotes us-east-1.
	Location string `xml:",chardata"`
}

type policyStatusXML struct {
	XMLName  xml.Name `xml:"PolicyStatus"`
	Xmlns    string   `xml:"xmlns,attr"`
	IsPublic bool     `xml:"IsPublic"`
}
