package elasticache

import (
	"encoding/xml"
	"net/http"
	"strconv"

	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
)

const (
	// apiVersion is the ElastiCache query API version.
	apiVersion = "2015-02-02"
	// actionDescribeEvents is shared with RDS and Redshift.
	actionDescribeEvents = "DescribeEvents"
)

// Limits on DescribeEvents inputs, from the ElastiCache API reference.
const (
	minEventRecords  = 20
	maxEventRecords  = 100
	maxEventDuration = 20160 // minutes, 14 days
)

// eventSourceTypes are the SourceType values ElastiCache accepts.
var eventSourceTypes = map[string]struct{}{ //nolint:gochecknoglobals // static lookup table
	"cache-cluster":             {},
	"cache-parameter-group":     {},
	"cache-security-group":      {},
	"cache-subnet-group":        {},
	"replication-group":         {},
	"serverless-cache":          {},
	"serverless-cache-snapshot": {},
	"user":                      {},
	"user-group":                {},
}

type eventXML struct {
	SourceIdentifier string `xml:"SourceIdentifier,omitempty"`
	SourceType       string `xml:"SourceType,omitempty"`
	Message          string `xml:"Message,omitempty"`
	Date             string `xml:"Date,omitempty"`
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

// describeEvents checks the inputs and returns an empty event list.
// cloudemu does not record ElastiCache events yet, same as RDS.
func (*Handler) describeEvents(w http.ResponseWriter, r *http.Request) {
	if msg := validateDescribeEvents(r); msg != "" {
		awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidParameterValue", msg)
		return
	}

	awsquery.WriteXMLResponse(w, describeEventsResponse{
		Xmlns:    Namespace,
		Result:   eventsResult{Events: []eventXML{}},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

// validateDescribeEvents returns an error message, or "" when the input is valid.
func validateDescribeEvents(r *http.Request) string {
	if st := r.Form.Get("SourceType"); st != "" {
		if _, ok := eventSourceTypes[st]; !ok {
			return "Invalid value for SourceType: " + st
		}
	}

	if !intInRange(r.Form.Get("MaxRecords"), minEventRecords, maxEventRecords) {
		return "MaxRecords must be between 20 and 100"
	}

	if !intInRange(r.Form.Get("Duration"), 0, maxEventDuration) {
		return "Duration must be between 0 and 20160 minutes"
	}

	return ""
}

// intInRange reports whether raw is empty or an integer in [lo, hi].
func intInRange(raw string, lo, hi int) bool {
	if raw == "" {
		return true
	}

	n, err := strconv.Atoi(raw)

	return err == nil && n >= lo && n <= hi
}
