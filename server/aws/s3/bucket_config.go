package s3

import (
	"encoding/xml"
	"net/http"
	"net/url"

	"github.com/stackshy/cloudemu/v2/server/wire"
)

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

// bucketConfigOp answers a read-only bucket configuration sub-resource for an
// existing bucket: GET returns the AWS-correct "not configured"/default
// response; a write is accepted as a no-op so it does not fall through to
// create/delete the bucket.
func (*Handler) bucketConfigOp(w http.ResponseWriter, r *http.Request, sub string) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusOK)
		return
	}

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
