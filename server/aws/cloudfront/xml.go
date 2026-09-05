package cloudfront

import (
	"encoding/xml"
	"errors"
	"net/http"
	"time"

	"github.com/stackshy/cloudemu/v2/server/wire"
	cfdriver "github.com/stackshy/cloudemu/v2/services/cloudfront/driver"
)

// xmlns is the CloudFront 2020-05-31 XML namespace stamped on response roots.
const xmlns = "http://cloudfront.amazonaws.com/doc/2020-05-31/"

// xmlRaw carries a verbatim XML sub-tree: on decode it captures an element's
// inner content, on encode it writes those bytes back unchanged.
type xmlRaw struct {
	Inner []byte `xml:",innerxml"`
}

// distributionConfigRequest decodes an inbound <DistributionConfig>. The scalars
// the emulator interprets are lifted out; Inner carries the whole sub-tree
// verbatim so a read round-trips every field the caller sent.
type distributionConfigRequest struct {
	XMLName         xml.Name `xml:"DistributionConfig"`
	CallerReference string   `xml:"CallerReference"`
	Comment         string   `xml:"Comment"`
	Enabled         bool     `xml:"Enabled"`
	Inner           []byte   `xml:",innerxml"`
}

// distributionConfigWithTagsRequest decodes an inbound <DistributionConfigWithTags>
// (CreateDistributionWithTags), pairing a config with its tags.
type distributionConfigWithTagsRequest struct {
	XMLName            xml.Name                  `xml:"DistributionConfigWithTags"`
	DistributionConfig distributionConfigRequest `xml:"DistributionConfig"`
	Tags               tagsXML                   `xml:"Tags"`
}

// tagsXML is the CloudFront <Tags><Items><Tag><Key/><Value/></Tag>…</Items></Tags>
// shape, shared by request decode and response encode.
type tagsXML struct {
	Items []tagXML `xml:"Items>Tag"`
}

type tagXML struct {
	Key   string `xml:"Key"`
	Value string `xml:"Value"`
}

func (t tagsXML) toMap() map[string]string {
	if len(t.Items) == 0 {
		return nil
	}

	out := make(map[string]string, len(t.Items))
	for _, it := range t.Items {
		out[it.Key] = it.Value
	}

	return out
}

// tagKeysRequest decodes an UntagResource body.
type tagKeysRequest struct {
	XMLName xml.Name `xml:"TagKeys"`
	Items   []string `xml:"Items>Key"`
}

// distributionXML is the <Distribution> response: server-assigned identity plus
// the verbatim config sub-tree.
type distributionXML struct {
	XMLName                       xml.Name             `xml:"Distribution"`
	Xmlns                         string               `xml:"xmlns,attr"`
	ID                            string               `xml:"Id"`
	ARN                           string               `xml:"ARN"`
	Status                        string               `xml:"Status"`
	LastModifiedTime              string               `xml:"LastModifiedTime"`
	InProgressInvalidationBatches int                  `xml:"InProgressInvalidationBatches"`
	DomainName                    string               `xml:"DomainName"`
	ActiveTrustedSigners          activeTrustedSigners `xml:"ActiveTrustedSigners"`
	ActiveTrustedKeyGroups        activeTrustedGroups  `xml:"ActiveTrustedKeyGroups"`
	Config                        xmlRaw               `xml:"DistributionConfig"`
}

type activeTrustedSigners struct {
	Enabled  bool `xml:"Enabled"`
	Quantity int  `xml:"Quantity"`
}

type activeTrustedGroups struct {
	Enabled  bool `xml:"Enabled"`
	Quantity int  `xml:"Quantity"`
}

// distributionConfigResponse is the <DistributionConfig> response (GetDistributionConfig).
type distributionConfigResponse struct {
	XMLName xml.Name `xml:"DistributionConfig"`
	Xmlns   string   `xml:"xmlns,attr"`
	Inner   []byte   `xml:",innerxml"`
}

// distributionListResponse is the <DistributionList> response (ListDistributions).
type distributionListResponse struct {
	XMLName     xml.Name                 `xml:"DistributionList"`
	Xmlns       string                   `xml:"xmlns,attr"`
	Marker      string                   `xml:"Marker"`
	MaxItems    int                      `xml:"MaxItems"`
	IsTruncated bool                     `xml:"IsTruncated"`
	Quantity    int                      `xml:"Quantity"`
	Items       []distributionSummaryXML `xml:"Items>DistributionSummary"`
}

// distributionSummaryXML is a <DistributionSummary> — the server identity fields
// followed by the verbatim config sub-tree. The SDK/Terraform deserializers skip
// the few config elements a summary does not model (CallerReference, Logging).
type distributionSummaryXML struct {
	XMLName          xml.Name `xml:"DistributionSummary"`
	ID               string   `xml:"Id"`
	ARN              string   `xml:"ARN"`
	Status           string   `xml:"Status"`
	LastModifiedTime string   `xml:"LastModifiedTime"`
	DomainName       string   `xml:"DomainName"`
	ConfigInner      []byte   `xml:",innerxml"`
}

// invalidationBatchXML is the shared <InvalidationBatch> shape.
type invalidationBatchXML struct {
	CallerReference string   `xml:"CallerReference"`
	Paths           pathsXML `xml:"Paths"`
}

type pathsXML struct {
	Quantity int      `xml:"Quantity"`
	Items    []string `xml:"Items>Path"`
}

// invalidationRequest decodes an inbound <InvalidationBatch>.
type invalidationRequest struct {
	XMLName         xml.Name `xml:"InvalidationBatch"`
	CallerReference string   `xml:"CallerReference"`
	Paths           pathsXML `xml:"Paths"`
}

// invalidationXML is the <Invalidation> response.
type invalidationXML struct {
	XMLName           xml.Name             `xml:"Invalidation"`
	Xmlns             string               `xml:"xmlns,attr"`
	ID                string               `xml:"Id"`
	Status            string               `xml:"Status"`
	CreateTime        string               `xml:"CreateTime"`
	InvalidationBatch invalidationBatchXML `xml:"InvalidationBatch"`
}

// invalidationListResponse is the <InvalidationList> response.
type invalidationListResponse struct {
	XMLName     xml.Name                 `xml:"InvalidationList"`
	Xmlns       string                   `xml:"xmlns,attr"`
	Marker      string                   `xml:"Marker"`
	MaxItems    int                      `xml:"MaxItems"`
	IsTruncated bool                     `xml:"IsTruncated"`
	Quantity    int                      `xml:"Quantity"`
	Items       []invalidationSummaryXML `xml:"Items>InvalidationSummary"`
}

type invalidationSummaryXML struct {
	ID         string `xml:"Id"`
	CreateTime string `xml:"CreateTime"`
	Status     string `xml:"Status"`
}

// tagsResponse is the <Tags> response (ListTagsForResource).
type tagsResponse struct {
	XMLName xml.Name `xml:"Tags"`
	Xmlns   string   `xml:"xmlns,attr"`
	Items   []tagXML `xml:"Items>Tag"`
}

// errorResponse is the CloudFront XML error envelope.
type errorResponse struct {
	XMLName xml.Name `xml:"ErrorResponse"`
	Xmlns   string   `xml:"xmlns,attr"`
	Error   errorXML `xml:"Error"`
}

type errorXML struct {
	Type    string `xml:"Type"`
	Code    string `xml:"Code"`
	Message string `xml:"Message"`
}

// isoTime formats a timestamp the way CloudFront renders LastModifiedTime /
// CreateTime — ISO8601 / RFC3339 in UTC.
func isoTime(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// decodeXML reads an XML request body into v, writing a MalformedXML error and
// returning false on a decode failure.
func decodeXML(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := xml.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "MalformedXML", "the XML you provided was not well-formed: "+err.Error())
		return false
	}

	return true
}

// writeError writes a CloudFront XML error response with the given status.
func writeError(w http.ResponseWriter, status int, code, msg string) {
	wire.WriteXML(w, status, errorResponse{
		Xmlns: xmlns,
		Error: errorXML{Type: "Sender", Code: code, Message: msg},
	})
}

// errCode maps a driver sentinel error to its CloudFront XML code and HTTP status.
type errCode struct {
	status int
	code   string
}

// codeFor returns the CloudFront error code and HTTP status for a driver error,
// falling back to a 500 InternalFailure for anything unrecognized.
func codeFor(err error) errCode {
	switch {
	case errors.Is(err, cfdriver.ErrNoSuchDistribution):
		return errCode{http.StatusNotFound, "NoSuchDistribution"}
	case errors.Is(err, cfdriver.ErrNoSuchInvalidation):
		return errCode{http.StatusNotFound, "NoSuchInvalidation"}
	case errors.Is(err, cfdriver.ErrDistributionAlreadyExists):
		return errCode{http.StatusConflict, "DistributionAlreadyExists"}
	case errors.Is(err, cfdriver.ErrInvalidIfMatchVersion):
		return errCode{http.StatusBadRequest, "InvalidIfMatchVersion"}
	case errors.Is(err, cfdriver.ErrPreconditionFailed):
		return errCode{http.StatusPreconditionFailed, "PreconditionFailed"}
	case errors.Is(err, cfdriver.ErrDistributionNotDisabled):
		return errCode{http.StatusConflict, "DistributionNotDisabled"}
	case errors.Is(err, cfdriver.ErrCallerReferenceImmutable):
		return errCode{http.StatusBadRequest, "IllegalUpdate"}
	default:
		return errCode{http.StatusInternalServerError, "InternalFailure"}
	}
}

// writeErr maps a driver error to its CloudFront XML error response.
func writeErr(w http.ResponseWriter, err error) {
	ec := codeFor(err)
	writeError(w, ec.status, ec.code, err.Error())
}
