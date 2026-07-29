package rds

import (
	"encoding/xml"
	"net/http"
	"net/url"

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
	SourceIdsList            []string `xml:"SourceIdsList>SourceId,omitempty"`
	Enabled                  bool     `xml:"Enabled"`
}

type eventXML struct {
	SourceIdentifier string   `xml:"SourceIdentifier,omitempty"`
	SourceType       string   `xml:"SourceType,omitempty"`
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
		SourceIdsList:            s.SourceIDs,
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

	events, err := store.DescribeEvents(r.Context(),
		r.Form.Get("SourceType"), r.Form.Get("SourceIdentifier"),
		awsquery.ListStrings(r.Form, "EventCategories.EventCategory"))
	if err != nil {
		writeErr(w, err)
		return
	}

	out := make([]eventXML, 0, len(events))
	for _, e := range events {
		out = append(out, eventXML{
			SourceIdentifier: e.SourceIdentifier,
			SourceType:       e.SourceType,
			Message:          e.Message,
			EventCategories:  e.EventCategories,
			Date:             e.Date.UTC().Format("2006-01-02T15:04:05.000Z"),
		})
	}

	awsquery.WriteXMLResponse(w, describeEventsResponse{
		Xmlns:    Namespace,
		Result:   eventsList{Events: out},
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

// enabledDefaultTrue reads the Enabled flag, defaulting to true when the field
// is absent (RDS CreateEventSubscription defaults Enabled to true).
func enabledDefaultTrue(form url.Values) bool {
	if _, ok := form["Enabled"]; !ok {
		return true
	}

	return formBool(form.Get("Enabled"))
}
