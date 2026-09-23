package redshift

import (
	"encoding/xml"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
)

const (
	// apiVersion is the Redshift query API version.
	apiVersion = "2012-12-01"
	// scopeRedshift is the SigV4 credential-scope service name for Redshift.
	scopeRedshift = "redshift"
	// actionDescribeEvents is shared with RDS and ElastiCache.
	actionDescribeEvents = "DescribeEvents"
)

type eventXML struct {
	SourceIdentifier string   `xml:"SourceIdentifier,omitempty"`
	SourceType       string   `xml:"SourceType,omitempty"`
	Message          string   `xml:"Message,omitempty"`
	EventCategories  []string `xml:"EventCategories>EventCategory,omitempty"`
	Severity         string   `xml:"Severity,omitempty"`
	Date             string   `xml:"Date,omitempty"`
	EventID          string   `xml:"EventId,omitempty"`
}

type describeEventsResponse struct {
	XMLName  xml.Name         `xml:"DescribeEventsResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   eventsResult     `xml:"DescribeEventsResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type eventsResult struct {
	Events []eventXML `xml:"Events>Event"`
}

// describeEvents returns an empty event list. cloudemu does not record
// Redshift events yet.
func (*Handler) describeEvents(w http.ResponseWriter, _ *http.Request) {
	awsquery.WriteXMLResponse(w, describeEventsResponse{
		Xmlns:    Namespace,
		Result:   eventsResult{Events: []eventXML{}},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

// ownsSharedRequest reports whether a shared-verb request is meant for
// Redshift. Unsigned requests with no Version still match.
func ownsSharedRequest(r *http.Request) bool {
	if svc := awsquery.CredentialScopeService(r.Header.Get("Authorization")); svc != "" && svc != scopeRedshift {
		return false
	}

	v := r.Form.Get("Version")

	return v == "" || v == apiVersion
}
