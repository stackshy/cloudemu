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

// Sub-resource keys answered with a default document when not configured.
const (
	subRequestPayment = "requestPayment"
	subAccelerate     = "accelerate"
	subLogging        = "logging"
	subPolicyStatus   = "policyStatus"
)

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
// echo in writeRawBucketConfig never sees it. It needs separate capture and
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
	"object-lock":       "ObjectLockConfigurationNotFoundError",
	"publicAccessBlock": "NoSuchPublicAccessBlockConfiguration",
	"ownershipControls": "OwnershipControlsNotFoundError",
}

// subEncryption is the ?encryption sub-resource key (Get/PutBucketEncryption).
const subEncryption = "encryption"

// configSubresource is a bucket configuration sub-resource query key and the
// IAM actions that read, write and delete it. An empty del means the delete
// operation is authorized by the put action, as S3 does for DeleteBucketCors,
// DeleteBucketLifecycle, DeletePublicAccessBlock and others.
//
// location and policyStatus have no write operation in S3. The handler still
// accepts a write to them, so it is authorized as an action that only s3:*
// (or a matching wildcard) grants.
type configSubresource struct {
	key, get, put, del string
}

// configSubresources are the bucket configuration sub-resources the handler
// answers. When a request names more than one, the first in this order wins.
//
//nolint:gochecknoglobals // static lookup table
var configSubresources = []configSubresource{
	{"policy", "GetBucketPolicy", "PutBucketPolicy", "DeleteBucketPolicy"},
	{"cors", "GetBucketCORS", "PutBucketCORS", ""},
	{"website", "GetBucketWebsite", "PutBucketWebsite", "DeleteBucketWebsite"},
	{subLifecycle, "GetLifecycleConfiguration", "PutLifecycleConfiguration", ""},
	{"replication", "GetReplicationConfiguration", "PutReplicationConfiguration", ""},
	{subEncryption, "GetEncryptionConfiguration", "PutEncryptionConfiguration", ""},
	{"object-lock", "GetBucketObjectLockConfiguration", "PutBucketObjectLockConfiguration", ""},
	{"publicAccessBlock", "GetBucketPublicAccessBlock", "PutBucketPublicAccessBlock", ""},
	{"ownershipControls", "GetBucketOwnershipControls", "PutBucketOwnershipControls", ""},
	{subRequestPayment, "GetBucketRequestPayment", "PutBucketRequestPayment", ""},
	{subAccelerate, "GetAccelerateConfiguration", "PutAccelerateConfiguration", ""},
	{subLogging, "GetBucketLogging", "PutBucketLogging", ""},
	{subLocation, "GetBucketLocation", "PutBucketLocation", ""},
	{subPolicyStatus, "GetBucketPolicyStatus", "PutBucketPolicyStatus", ""},
}

// configSubresourceKey returns the config sub-resource query key present on
// the request, or "" if none.
func configSubresourceKey(q url.Values) string {
	if c, ok := findConfigSubresource(func(c *configSubresource) bool { return q.Has(c.key) }); ok {
		return c.key
	}

	return ""
}

// findConfigSubresource returns the first configuration sub-resource match
// accepts.
func findConfigSubresource(match func(*configSubresource) bool) (configSubresource, bool) {
	for i := range configSubresources {
		if match(&configSubresources[i]) {
			return configSubresources[i], true
		}
	}

	return configSubresource{}, false
}

// putBucketConfig persists a configuration document when the driver supports it,
// otherwise accepts the write as a no-op.
func (h *Handler) putBucketConfig(w http.ResponseWriter, r *http.Request, bucket, sub string) {
	if sub == subObjectLock && h.objectLock != nil {
		h.putObjectLockConfiguration(w, r, bucket)
		return
	}

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
	if sub == subObjectLock && h.objectLock != nil {
		h.getObjectLockConfiguration(w, r, bucket)
		return
	}

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
	// Since January 2023 every S3 bucket has default encryption: a bucket with no
	// explicit configuration answers GetBucketEncryption with 200 and the SSE-S3
	// (AES256) base rule, not ServerSideEncryptionConfigurationNotFoundError.
	// Returning the old error caused the Terraform AWS provider to see perpetual
	// drift on buckets with no customer-defined encryption.
	if sub == subEncryption {
		wire.WriteXML(w, http.StatusOK, defaultEncryptionConfig())
		return
	}

	if code, ok := notConfiguredErr[sub]; ok {
		writeError(w, http.StatusNotFound, code, "The "+sub+" configuration does not exist")
		return
	}

	switch sub {
	case subRequestPayment:
		wire.WriteXML(w, http.StatusOK, requestPaymentXML{Xmlns: xmlns, Payer: "BucketOwner"})
	case subAccelerate:
		wire.WriteXML(w, http.StatusOK, accelerateXML{Xmlns: xmlns})
	case subLogging:
		wire.WriteXML(w, http.StatusOK, loggingXML{Xmlns: xmlns})
	case subLocation:
		// An empty LocationConstraint denotes us-east-1.
		wire.WriteXML(w, http.StatusOK, locationXML{Xmlns: xmlns})
	case subPolicyStatus:
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

// serverSideEncryptionConfigXML is the GetBucketEncryption response body. Only
// the fields needed to render the SSE-S3 default are modeled; a bucket that has
// its own configuration persisted echoes the stored document verbatim instead.
type serverSideEncryptionConfigXML struct {
	XMLName xml.Name                   `xml:"ServerSideEncryptionConfiguration"`
	Xmlns   string                     `xml:"xmlns,attr"`
	Rules   []serverSideEncryptionRule `xml:"Rule"`
}

type serverSideEncryptionRule struct {
	ApplyDefault     applyServerSideEncryptionByDefault `xml:"ApplyServerSideEncryptionByDefault"`
	BucketKeyEnabled bool                               `xml:"BucketKeyEnabled"`
}

type applyServerSideEncryptionByDefault struct {
	SSEAlgorithm string `xml:"SSEAlgorithm"`
}

// sseAlgorithmAES256 is the SSE-S3 algorithm name S3 reports for its default
// bucket encryption.
const sseAlgorithmAES256 = "AES256"

// defaultEncryptionConfig returns the SSE-S3 (AES256) base encryption rule that
// real S3 reports for a bucket with no explicit configuration, with bucket keys
// disabled, matching a freshly created bucket.
func defaultEncryptionConfig() serverSideEncryptionConfigXML {
	return serverSideEncryptionConfigXML{
		Xmlns: xmlns,
		Rules: []serverSideEncryptionRule{{
			ApplyDefault:     applyServerSideEncryptionByDefault{SSEAlgorithm: sseAlgorithmAES256},
			BucketKeyEnabled: false,
		}},
	}
}
