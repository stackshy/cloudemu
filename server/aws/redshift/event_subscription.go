package redshift

import (
	"context"
	"encoding/xml"
	"net/http"
	"net/url"
	"sort"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	redshiftprovider "github.com/stackshy/cloudemu/v2/providers/aws/redshift"
	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
)

// eventSubscriber is the Redshift event subscription surface. It is not part
// of the shared relationaldb driver, so the handler type-asserts for it.
type eventSubscriber interface {
	CreateEventSubscription(ctx context.Context, cfg redshiftprovider.EventSubscriptionConfig) (*redshiftprovider.EventSubscription, error)
	DescribeEventSubscriptions(ctx context.Context, name string) ([]redshiftprovider.EventSubscription, error)
	ModifyEventSubscription(
		ctx context.Context, name string, in redshiftprovider.ModifyEventSubscriptionInput,
	) (*redshiftprovider.EventSubscription, error)
	DeleteEventSubscription(ctx context.Context, name string) error
}

type eventSubscriptionXML struct {
	CustomerAwsID            string   `xml:"CustomerAwsId"`
	CustSubscriptionID       string   `xml:"CustSubscriptionId"`
	SnsTopicArn              string   `xml:"SnsTopicArn"`
	Status                   string   `xml:"Status"`
	SubscriptionCreationTime string   `xml:"SubscriptionCreationTime"`
	SourceType               string   `xml:"SourceType,omitempty"`
	SourceIDs                []string `xml:"SourceIdsList>SourceId"`
	EventCategories          []string `xml:"EventCategoriesList>EventCategory"`
	Severity                 string   `xml:"Severity"`
	Enabled                  bool     `xml:"Enabled"`
	Tags                     []tagXML `xml:"Tags>Tag"`
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
	XMLName  xml.Name         `xml:"DeleteEventSubscriptionResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type describeEventSubscriptionsResponse struct {
	XMLName  xml.Name                 `xml:"DescribeEventSubscriptionsResponse"`
	Xmlns    string                   `xml:"xmlns,attr"`
	Result   eventSubscriptionsResult `xml:"DescribeEventSubscriptionsResult"`
	Metadata responseMetadata         `xml:"ResponseMetadata"`
}

type eventSubscriptionsResult struct {
	Marker        string                 `xml:"Marker,omitempty"`
	Subscriptions []eventSubscriptionXML `xml:"EventSubscriptionsList>EventSubscription"`
}

type eventInfoMapXML struct {
	EventID          string   `xml:"EventId"`
	EventCategories  []string `xml:"EventCategories>EventCategory"`
	EventDescription string   `xml:"EventDescription"`
	Severity         string   `xml:"Severity"`
}

type eventCategoriesMapXML struct {
	SourceType string            `xml:"SourceType"`
	Events     []eventInfoMapXML `xml:"Events>EventInfoMap"`
}

type describeEventCategoriesResponse struct {
	XMLName  xml.Name                `xml:"DescribeEventCategoriesResponse"`
	Xmlns    string                  `xml:"xmlns,attr"`
	Maps     []eventCategoriesMapXML `xml:"DescribeEventCategoriesResult>EventCategoriesMapList>EventCategoriesMap"`
	Metadata responseMetadata        `xml:"ResponseMetadata"`
}

func (h *Handler) eventSubscriber() (eventSubscriber, bool) {
	s, ok := h.db.(eventSubscriber)

	return s, ok
}

func (h *Handler) createEventSubscription(w http.ResponseWriter, r *http.Request) {
	store, ok := h.eventSubscriber()
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "event subscriptions not supported"))
		return
	}

	form := r.Form
	cfg := redshiftprovider.EventSubscriptionConfig{
		Name:        form.Get("SubscriptionName"),
		SnsTopicARN: form.Get("SnsTopicArn"),
		SourceType:  form.Get("SourceType"),
		Severity:    form.Get("Severity"),
		Tags:        parseRedshiftTags(form),
	}

	if ids := listField(form, "SourceIds", "SourceId"); ids != nil {
		cfg.SourceIDs = *ids
	}

	if cats := listField(form, "EventCategories", "EventCategory"); cats != nil {
		cfg.EventCategories = *cats
	}

	if form.Has("Enabled") {
		enabled := formBool(form.Get("Enabled"))
		cfg.Enabled = &enabled
	}

	sub, err := store.CreateEventSubscription(r.Context(), cfg)
	if err != nil {
		writeEventSubErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, createEventSubscriptionResponse{
		Xmlns:    Namespace,
		Result:   eventSubscriptionResult{EventSubscription: h.toEventSubscriptionXML(r.Context(), sub)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) modifyEventSubscription(w http.ResponseWriter, r *http.Request) {
	store, ok := h.eventSubscriber()
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "event subscriptions not supported"))
		return
	}

	form := r.Form
	in := redshiftprovider.ModifyEventSubscriptionInput{
		SnsTopicARN:     optionalString(form, "SnsTopicArn"),
		SourceType:      optionalString(form, "SourceType"),
		SourceIDs:       listField(form, "SourceIds", "SourceId"),
		EventCategories: listField(form, "EventCategories", "EventCategory"),
		Severity:        optionalString(form, "Severity"),
	}

	if form.Has("Enabled") {
		enabled := formBool(form.Get("Enabled"))
		in.Enabled = &enabled
	}

	sub, err := store.ModifyEventSubscription(r.Context(), form.Get("SubscriptionName"), in)
	if err != nil {
		writeEventSubErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, modifyEventSubscriptionResponse{
		Xmlns:    Namespace,
		Result:   eventSubscriptionResult{EventSubscription: h.toEventSubscriptionXML(r.Context(), sub)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) deleteEventSubscription(w http.ResponseWriter, r *http.Request) {
	store, ok := h.eventSubscriber()
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "event subscriptions not supported"))
		return
	}

	if err := store.DeleteEventSubscription(r.Context(), r.Form.Get("SubscriptionName")); err != nil {
		writeEventSubErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, deleteEventSubscriptionResponse{
		Xmlns: Namespace, Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) describeEventSubscriptions(w http.ResponseWriter, r *http.Request) {
	store, ok := h.eventSubscriber()
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "event subscriptions not supported"))
		return
	}

	subs, err := store.DescribeEventSubscriptions(r.Context(), r.Form.Get("SubscriptionName"))
	if err != nil {
		writeEventSubErr(w, err)
		return
	}

	keys := awsquery.ListStrings(r.Form, "TagKeys.TagKey")
	values := awsquery.ListStrings(r.Form, "TagValues.TagValue")

	out := make([]eventSubscriptionXML, 0, len(subs))

	for i := range subs {
		x := h.toEventSubscriptionXML(r.Context(), &subs[i])
		if tagsMatch(x.Tags, keys, values) {
			out = append(out, x)
		}
	}

	page, err := paginateRedshift(out, func(s eventSubscriptionXML) string { return s.CustSubscriptionID },
		r.Form.Get("Marker"), r.Form.Get("MaxRecords"))
	if err != nil {
		writeInvalidMarker(w)
		return
	}

	awsquery.WriteXMLResponse(w, describeEventSubscriptionsResponse{
		Xmlns:    Namespace,
		Result:   eventSubscriptionsResult{Marker: page.NextPageToken, Subscriptions: page.Items},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (*Handler) describeEventCategories(w http.ResponseWriter, r *http.Request) {
	sourceType := r.Form.Get("SourceType")

	maps := make([]eventCategoriesMapXML, 0, len(eventCatalogSourceTypes))

	for _, st := range eventCatalogSourceTypes {
		if sourceType != "" && sourceType != st {
			continue
		}

		events := make([]eventInfoMapXML, 0, len(eventCatalog[st]))
		for _, e := range eventCatalog[st] {
			events = append(events, eventInfoMapXML{
				EventID: e.id, EventCategories: []string{e.category}, EventDescription: e.description, Severity: e.severity,
			})
		}

		maps = append(maps, eventCategoriesMapXML{SourceType: st, Events: events})
	}

	awsquery.WriteXMLResponse(w, describeEventCategoriesResponse{
		Xmlns: Namespace, Maps: maps, Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

// toEventSubscriptionXML renders a subscription with its tags, sorted by key.
func (h *Handler) toEventSubscriptionXML(ctx context.Context, sub *redshiftprovider.EventSubscription) eventSubscriptionXML {
	var tags []tagXML

	if tagger, ok := h.resourceTagger(); ok {
		if m, err := tagger.DescribeTags(ctx, sub.ARN); err == nil {
			for k, v := range m {
				tags = append(tags, tagXML{Key: k, Value: v})
			}

			sort.Slice(tags, func(i, j int) bool { return tags[i].Key < tags[j].Key })
		}
	}

	return eventSubscriptionXML{
		CustomerAwsID:            sub.CustomerAWSID,
		CustSubscriptionID:       sub.Name,
		SnsTopicArn:              sub.SnsTopicARN,
		Status:                   sub.Status,
		SubscriptionCreationTime: sub.CreatedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
		SourceType:               sub.SourceType,
		SourceIDs:                sub.SourceIDs,
		EventCategories:          sub.EventCategories,
		Severity:                 sub.Severity,
		Enabled:                  sub.Enabled,
		Tags:                     tags,
	}
}

// tagsMatch applies the TagKeys and TagValues filters. A subscription matches
// when it has any of the keys and any of the values that were given.
func tagsMatch(tags []tagXML, keys, values []string) bool {
	return anyTag(tags, keys, func(t tagXML) string { return t.Key }) &&
		anyTag(tags, values, func(t tagXML) string { return t.Value })
}

func anyTag(tags []tagXML, want []string, field func(tagXML) string) bool {
	if len(want) == 0 {
		return true
	}

	for _, t := range tags {
		for _, w := range want {
			if field(t) == w {
				return true
			}
		}
	}

	return false
}

// listField reads a query list such as SourceIds.SourceId.N. It returns nil
// when the list was not sent, and an empty list when it was sent empty.
func listField(form url.Values, name, member string) *[]string {
	sent := form.Has(name)

	for k := range form {
		if strings.HasPrefix(k, name+".") {
			sent = true
			break
		}
	}

	if !sent {
		return nil
	}

	out := awsquery.ListStrings(form, name+"."+member)
	if out == nil {
		out = []string{}
	}

	return &out
}

func optionalString(form url.Values, key string) *string {
	if !form.Has(key) {
		return nil
	}

	v := form.Get(key)

	return &v
}

// writeEventSubErr maps provider errors to the Redshift event subscription
// fault codes.
func writeEventSubErr(w http.ResponseWriter, err error) {
	msg := cerrors.Message(err)

	switch {
	case cerrors.IsNotFound(err):
		awsquery.WriteXMLError(w, http.StatusNotFound, eventSubNotFoundCode(msg), msg)
	case cerrors.IsAlreadyExists(err):
		awsquery.WriteXMLError(w, http.StatusBadRequest, "SubscriptionAlreadyExist", msg)
	case cerrors.IsInvalidArgument(err) && strings.Contains(msg, "SNS topic ARN"):
		awsquery.WriteXMLError(w, http.StatusBadRequest, "SNSInvalidTopic", msg)
	case cerrors.GetCode(err) == cerrors.ResourceExhausted:
		awsquery.WriteXMLError(w, http.StatusBadRequest, "TagLimitExceededFault", msg)
	default:
		writeErr(w, err)
	}
}

func eventSubNotFoundCode(msg string) string {
	switch {
	case strings.Contains(msg, "SNS topic"):
		return "SNSTopicArnNotFound"
	case strings.Contains(msg, "event category"):
		return "SubscriptionCategoryNotFound"
	case strings.Contains(msg, "event severity"):
		return "SubscriptionSeverityNotFound"
	case strings.Contains(msg, "event source"):
		return "SourceNotFound"
	default:
		return "SubscriptionNotFound"
	}
}
