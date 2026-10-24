package rds

import (
	"encoding/xml"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/stackshy/cloudemu/v2/internal/pagination"
	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

// ---- XML shapes ----

type eventSubscriptionXML struct {
	CustSubscriptionID       string   `xml:"CustSubscriptionId"`
	CustomerAwsID            string   `xml:"CustomerAwsId,omitempty"`
	EventSubscriptionArn     string   `xml:"EventSubscriptionArn,omitempty"`
	SnsTopicArn              string   `xml:"SnsTopicArn,omitempty"`
	SourceType               string   `xml:"SourceType,omitempty"`
	Status                   string   `xml:"Status,omitempty"`
	SubscriptionCreationTime string   `xml:"SubscriptionCreationTime,omitempty"`
	EventCategoriesList      []string `xml:"EventCategoriesList>EventCategory,omitempty"`
	SourceIDsList            []string `xml:"SourceIdsList>SourceId,omitempty"`
	Enabled                  bool     `xml:"Enabled"`
}

type eventXML struct {
	SourceIdentifier string   `xml:"SourceIdentifier,omitempty"`
	SourceType       string   `xml:"SourceType,omitempty"`
	SourceArn        string   `xml:"SourceArn,omitempty"`
	Message          string   `xml:"Message,omitempty"`
	EventCategories  []string `xml:"EventCategories>EventCategory,omitempty"`
	Date             string   `xml:"Date,omitempty"`
}

type eventCategoriesMapXML struct {
	SourceType      string   `xml:"SourceType"`
	EventCategories []string `xml:"EventCategories>EventCategory"`
}

type eventSubscriptionResult struct {
	EventSubscription eventSubscriptionXML `xml:"EventSubscription"`
}

type createEventSubscriptionResponse struct {
	XMLName  xml.Name                `xml:"CreateEventSubscriptionResponse"`
	Xmlns    string                  `xml:"xmlns,attr"`
	Result   eventSubscriptionResult `xml:"CreateEventSubscriptionResult"`
	Metadata responseMetadata        `xml:"ResponseMetadata"`
}

type modifyEventSubscriptionResponse struct {
	XMLName  xml.Name                `xml:"ModifyEventSubscriptionResponse"`
	Xmlns    string                  `xml:"xmlns,attr"`
	Result   eventSubscriptionResult `xml:"ModifyEventSubscriptionResult"`
	Metadata responseMetadata        `xml:"ResponseMetadata"`
}

type deleteEventSubscriptionResponse struct {
	XMLName  xml.Name                `xml:"DeleteEventSubscriptionResponse"`
	Xmlns    string                  `xml:"xmlns,attr"`
	Result   eventSubscriptionResult `xml:"DeleteEventSubscriptionResult"`
	Metadata responseMetadata        `xml:"ResponseMetadata"`
}

type describeEventSubscriptionsResponse struct {
	XMLName  xml.Name              `xml:"DescribeEventSubscriptionsResponse"`
	Xmlns    string                `xml:"xmlns,attr"`
	Result   eventSubscriptionList `xml:"DescribeEventSubscriptionsResult"`
	Metadata responseMetadata      `xml:"ResponseMetadata"`
}

type eventSubscriptionList struct {
	EventSubscriptionsList []eventSubscriptionXML `xml:"EventSubscriptionsList>EventSubscription"`
}

type describeEventsResponse struct {
	XMLName  xml.Name         `xml:"DescribeEventsResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   eventsList       `xml:"DescribeEventsResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type eventsList struct {
	Marker string     `xml:"Marker,omitempty"`
	Events []eventXML `xml:"Events>Event"`
}

type describeEventCategoriesResponse struct {
	XMLName  xml.Name               `xml:"DescribeEventCategoriesResponse"`
	Xmlns    string                 `xml:"xmlns,attr"`
	Result   eventCategoriesMapList `xml:"DescribeEventCategoriesResult"`
	Metadata responseMetadata       `xml:"ResponseMetadata"`
}

type eventCategoriesMapList struct {
	EventCategoriesMapList []eventCategoriesMapXML `xml:"EventCategoriesMapList>EventCategoriesMap"`
}

// ---- helpers ----

func (h *Handler) eventSubscriptionsCap() (rdsdriver.EventSubscriptions, bool) {
	es, ok := h.db.(rdsdriver.EventSubscriptions)

	return es, ok
}

func toEventSubscriptionXML(s *rdsdriver.EventSubscription) eventSubscriptionXML {
	return eventSubscriptionXML{
		CustSubscriptionID:       s.Name,
		CustomerAwsID:            s.CustomerAWSID,
		EventSubscriptionArn:     s.ARN,
		SnsTopicArn:              s.SnsTopicARN,
		SourceType:               s.SourceType,
		Status:                   s.Status,
		SubscriptionCreationTime: s.CreatedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
		EventCategoriesList:      s.EventCategories,
		SourceIDsList:            s.SourceIDs,
		Enabled:                  s.Enabled,
	}
}

// ---- handlers ----

func (h *Handler) createEventSubscription(w http.ResponseWriter, r *http.Request) {
	store, ok := h.eventSubscriptionsCap()
	if !ok {
		writeUnsupported(w, "event subscriptions")
		return
	}

	sub, err := store.CreateEventSubscription(r.Context(), rdsdriver.EventSubscriptionConfig{
		Name:            r.Form.Get("SubscriptionName"),
		SnsTopicARN:     r.Form.Get("SnsTopicArn"),
		SourceType:      r.Form.Get("SourceType"),
		EventCategories: awsquery.ListStrings(r.Form, "EventCategories.EventCategory"),
		SourceIDs:       awsquery.ListStrings(r.Form, "SourceIds.SourceId"),
		Enabled:         enabledDefaultTrue(r.Form),
		Tags:            parseRDSTags(r.Form),
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, createEventSubscriptionResponse{
		Xmlns:    Namespace,
		Result:   eventSubscriptionResult{EventSubscription: toEventSubscriptionXML(sub)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

//nolint:dupl // structurally mirrors its sibling per-resource block by design.
func (h *Handler) describeEventSubscriptions(w http.ResponseWriter, r *http.Request) {
	store, ok := h.eventSubscriptionsCap()
	if !ok {
		writeUnsupported(w, "event subscriptions")
		return
	}

	var names []string
	if n := r.Form.Get("SubscriptionName"); n != "" {
		names = []string{n}
	}

	subs, err := store.DescribeEventSubscriptions(r.Context(), names)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := make([]eventSubscriptionXML, 0, len(subs))
	for i := range subs {
		out = append(out, toEventSubscriptionXML(&subs[i]))
	}

	awsquery.WriteXMLResponse(w, describeEventSubscriptionsResponse{
		Xmlns:    Namespace,
		Result:   eventSubscriptionList{EventSubscriptionsList: out},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) modifyEventSubscription(w http.ResponseWriter, r *http.Request) {
	store, ok := h.eventSubscriptionsCap()
	if !ok {
		writeUnsupported(w, "event subscriptions")
		return
	}

	sub, err := store.ModifyEventSubscription(r.Context(), r.Form.Get("SubscriptionName"), rdsdriver.ModifyEventSubscriptionInput{
		SnsTopicARN:     r.Form.Get("SnsTopicArn"),
		SourceType:      r.Form.Get("SourceType"),
		EventCategories: awsquery.ListStrings(r.Form, "EventCategories.EventCategory"),
		Enabled:         optBool(r.Form, "Enabled"),
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, modifyEventSubscriptionResponse{
		Xmlns:    Namespace,
		Result:   eventSubscriptionResult{EventSubscription: toEventSubscriptionXML(sub)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) deleteEventSubscription(w http.ResponseWriter, r *http.Request) {
	store, ok := h.eventSubscriptionsCap()
	if !ok {
		writeUnsupported(w, "event subscriptions")
		return
	}

	sub, err := store.DeleteEventSubscription(r.Context(), r.Form.Get("SubscriptionName"))
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, deleteEventSubscriptionResponse{
		Xmlns:    Namespace,
		Result:   eventSubscriptionResult{EventSubscription: toEventSubscriptionXML(sub)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) describeEvents(w http.ResponseWriter, r *http.Request) {
	store, ok := h.eventSubscriptionsCap()
	if !ok {
		writeUnsupported(w, "event subscriptions")
		return
	}

	maxRecords, ok := describeEventsMaxRecords(r.Form.Get("MaxRecords"))
	if !ok {
		awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidParameterValue",
			"MaxRecords must be between 20 and 100")

		return
	}

	input, field, ok := describeEventsInput(r.Form)
	if !ok {
		awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidParameterValue",
			"Invalid value for parameter "+field)

		return
	}

	events, err := store.DescribeEvents(r.Context(), input)
	if err != nil {
		writeErr(w, err)
		return
	}

	// Events are already oldest-first; page them in that order (no re-sort).
	page, err := pagination.Paginate(events, r.Form.Get("Marker"), maxRecords)
	if err != nil {
		awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidParameterValue", "invalid Marker")
		return
	}

	out := make([]eventXML, 0, len(page.Items))

	for i := range page.Items {
		e := &page.Items[i]
		out = append(out, eventXML{
			SourceIdentifier: e.SourceIdentifier,
			SourceType:       e.SourceType,
			SourceArn:        e.SourceARN,
			Message:          e.Message,
			EventCategories:  e.EventCategories,
			Date:             e.Date.UTC().Format("2006-01-02T15:04:05.000Z"),
		})
	}

	awsquery.WriteXMLResponse(w, describeEventsResponse{
		Xmlns:    Namespace,
		Result:   eventsList{Marker: page.NextPageToken, Events: out},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) describeEventCategories(w http.ResponseWriter, r *http.Request) {
	store, ok := h.eventSubscriptionsCap()
	if !ok {
		writeUnsupported(w, "event subscriptions")
		return
	}

	groups, err := store.DescribeEventCategories(r.Context(), r.Form.Get("SourceType"))
	if err != nil {
		writeErr(w, err)
		return
	}

	out := make([]eventCategoriesMapXML, 0, len(groups))
	for _, g := range groups {
		out = append(out, eventCategoriesMapXML{SourceType: g.SourceType, EventCategories: g.EventCategories})
	}

	awsquery.WriteXMLResponse(w, describeEventCategoriesResponse{
		Xmlns:    Namespace,
		Result:   eventCategoriesMapList{EventCategoriesMapList: out},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

// DescribeEvents MaxRecords bounds: "Default: 100. Constraints: Minimum 20,
// maximum 100." (DescribeEvents API reference).
const (
	minEventRecords     = 20
	maxEventRecords     = 100
	defaultEventRecords = 100
)

// describeEventsMaxRecords resolves DescribeEvents' MaxRecords: absent means
// the default 100; a non-integer or a value outside [20, 100] is invalid.
func describeEventsMaxRecords(raw string) (int, bool) {
	if raw == "" {
		return defaultEventRecords, true
	}

	n, err := strconv.Atoi(raw)
	if err != nil || n < minEventRecords || n > maxEventRecords {
		return 0, false
	}

	return n, true
}

// eventTimeLayouts are the ISO 8601 forms DescribeEvents StartTime/EndTime
// arrive in: the SDKs send full RFC 3339 timestamps, while the API reference
// example uses the minute-precision form "2009-07-08T18:00Z".
//
//nolint:gochecknoglobals // static lookup table
var eventTimeLayouts = []string{time.RFC3339Nano, "2006-01-02T15:04Z07:00", "2006-01-02T15:04", time.DateOnly}

// describeEventsInput decodes DescribeEvents' filters from the form. It
// returns the name of the first malformed field (a StartTime/EndTime that is
// not an ISO 8601 timestamp, or a non-integer Duration) and false, so the
// caller answers InvalidParameterValue as real RDS does instead of silently
// widening the window. The SourceType / SourceIdentifier rules are business
// rules and are enforced by the provider.
func describeEventsInput(form url.Values) (rdsdriver.DescribeEventsInput, string, bool) {
	start, ok := parseEventTime(form.Get("StartTime"))
	if !ok {
		return rdsdriver.DescribeEventsInput{}, "StartTime", false
	}

	end, ok := parseEventTime(form.Get("EndTime"))
	if !ok {
		return rdsdriver.DescribeEventsInput{}, "EndTime", false
	}

	duration := 0

	if raw := form.Get("Duration"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			return rdsdriver.DescribeEventsInput{}, "Duration", false
		}

		duration = n
	}

	return rdsdriver.DescribeEventsInput{
		SourceType:       form.Get("SourceType"),
		SourceIdentifier: form.Get("SourceIdentifier"),
		EventCategories:  awsquery.ListStrings(form, "EventCategories.EventCategory"),
		StartTime:        start,
		EndTime:          end,
		Duration:         duration,
	}, "", true
}

// parseEventTime parses an optional DescribeEvents timestamp. An absent value
// yields the zero time ("no bound") and true; a value in none of the accepted
// layouts yields false.
func parseEventTime(v string) (time.Time, bool) {
	if v == "" {
		return time.Time{}, true
	}

	for _, layout := range eventTimeLayouts {
		if t, err := time.Parse(layout, v); err == nil {
			return t, true
		}
	}

	return time.Time{}, false
}

// enabledDefaultTrue reads the Enabled flag, defaulting to true when the field
// is absent (RDS CreateEventSubscription defaults Enabled to true).
func enabledDefaultTrue(form url.Values) bool {
	if _, ok := form["Enabled"]; !ok {
		return true
	}

	return formBool(form.Get("Enabled"))
}
