package eventbridge

import (
	"context"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
	ebdriver "github.com/stackshy/cloudemu/v2/services/eventbus/driver"
)

// partnerSourceManager is the AWS-specific EventBridge partner event source
// surface, both the SaaS partner side and the customer side, asserted against
// the provider (not part of the portable EventBus driver).
type partnerSourceManager interface {
	CreatePartnerEventSource(ctx context.Context, name, account string) (string, error)
	DeletePartnerEventSource(ctx context.Context, name, account string) error
	DescribePartnerEventSource(ctx context.Context, name string) (*ebdriver.PartnerEventSource, error)
	ListPartnerEventSources(ctx context.Context, f ebdriver.EventSourceFilter) ([]ebdriver.PartnerEventSource, string, error)
	ListPartnerEventSourceAccounts(
		ctx context.Context, name string, limit int, nextToken string,
	) ([]ebdriver.PartnerEventSourceAccount, string, error)
	PutPartnerEventEntries(ctx context.Context, events []ebdriver.Event) ([]ebdriver.PutEventsEntryResult, error)
	DescribeEventSource(ctx context.Context, name string) (*ebdriver.EventSource, error)
	ListEventSources(ctx context.Context, f ebdriver.EventSourceFilter) ([]ebdriver.EventSource, string, error)
	ActivateEventSource(ctx context.Context, name string) error
	DeactivateEventSource(ctx context.Context, name string) error
}

type partnerSourceOp func(mgr partnerSourceManager, w http.ResponseWriter, r *http.Request)

func partnerSourceOps() map[string]partnerSourceOp {
	return map[string]partnerSourceOp{
		"CreatePartnerEventSource":       createPartnerEventSource,
		"DeletePartnerEventSource":       deletePartnerEventSource,
		"DescribePartnerEventSource":     describePartnerEventSource,
		"ListPartnerEventSources":        listPartnerEventSources,
		"ListPartnerEventSourceAccounts": listPartnerEventSourceAccounts,
		"PutPartnerEvents":               putPartnerEvents,
		"DescribeEventSource":            describeEventSource,
		"ListEventSources":               listEventSources,
		"ActivateEventSource":            sourceAction(partnerSourceManager.ActivateEventSource),
		"DeactivateEventSource":          sourceAction(partnerSourceManager.DeactivateEventSource),
	}
}

// servePartnerSourceOp serves op when it is a partner event source operation,
// reporting whether it did.
func (h *Handler) servePartnerSourceOp(w http.ResponseWriter, r *http.Request, op string) bool {
	fn, ok := partnerSourceOps()[op]
	if !ok {
		return false
	}

	mgr, ok := h.bus.(partnerSourceManager)
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "partner event sources not supported"))
		return true
	}

	fn(mgr, w, r)

	return true
}

type partnerSourceRequest struct {
	Name    string `json:"Name"`
	Account string `json:"Account"`
}

type listSourcesRequest struct {
	NamePrefix      string `json:"NamePrefix"`
	EventSourceName string `json:"EventSourceName"`
	Limit           int    `json:"Limit"`
	NextToken       string `json:"NextToken"`
}

type partnerSourceJSON struct {
	Arn  string `json:"Arn"`
	Name string `json:"Name"`
}

type partnerAccountJSON struct {
	Account        string   `json:"Account"`
	CreationTime   *float64 `json:"CreationTime,omitempty"`
	ExpirationTime *float64 `json:"ExpirationTime,omitempty"`
	State          string   `json:"State"`
}

type eventSourceJSON struct {
	Arn            string   `json:"Arn"`
	CreatedBy      string   `json:"CreatedBy"`
	CreationTime   *float64 `json:"CreationTime,omitempty"`
	ExpirationTime *float64 `json:"ExpirationTime,omitempty"`
	Name           string   `json:"Name"`
	State          string   `json:"State"`
}

type putPartnerEventsEntry struct {
	Source     string   `json:"Source"`
	DetailType string   `json:"DetailType"`
	Detail     string   `json:"Detail"`
	Resources  []string `json:"Resources"`
	Time       *float64 `json:"Time"`
}

type putPartnerEventsRequest struct {
	Entries []putPartnerEventsEntry `json:"Entries"`
}

func toEventSourceJSON(s *ebdriver.EventSource) eventSourceJSON {
	return eventSourceJSON{
		Arn: s.ARN, CreatedBy: s.CreatedBy, Name: s.Name, State: s.State,
		CreationTime: timeToEpoch(s.CreatedAt), ExpirationTime: timeToEpoch(s.ExpiresAt),
	}
}

func createPartnerEventSource(mgr partnerSourceManager, w http.ResponseWriter, r *http.Request) {
	var req partnerSourceRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	arn, err := mgr.CreatePartnerEventSource(r.Context(), req.Name, req.Account)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, struct {
		EventSourceArn string `json:"EventSourceArn"`
	}{arn})
}

func deletePartnerEventSource(mgr partnerSourceManager, w http.ResponseWriter, r *http.Request) {
	var req partnerSourceRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := mgr.DeletePartnerEventSource(r.Context(), req.Name, req.Account); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, struct{}{})
}

func describePartnerEventSource(mgr partnerSourceManager, w http.ResponseWriter, r *http.Request) {
	var req partnerSourceRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	src, err := mgr.DescribePartnerEventSource(r.Context(), req.Name)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, partnerSourceJSON{Arn: src.ARN, Name: src.Name})
}

func listPartnerEventSources(mgr partnerSourceManager, w http.ResponseWriter, r *http.Request) {
	var req listSourcesRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	items, next, err := mgr.ListPartnerEventSources(r.Context(), ebdriver.EventSourceFilter{
		NamePrefix: req.NamePrefix, Limit: req.Limit, NextToken: req.NextToken,
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	out := make([]partnerSourceJSON, len(items))
	for i := range items {
		out[i] = partnerSourceJSON{Arn: items[i].ARN, Name: items[i].Name}
	}

	wire.WriteJSON(w, struct {
		PartnerEventSources []partnerSourceJSON `json:"PartnerEventSources"`
		NextToken           string              `json:"NextToken,omitempty"`
	}{out, next})
}

func listPartnerEventSourceAccounts(mgr partnerSourceManager, w http.ResponseWriter, r *http.Request) {
	var req listSourcesRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	items, next, err := mgr.ListPartnerEventSourceAccounts(r.Context(), req.EventSourceName, req.Limit, req.NextToken)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := make([]partnerAccountJSON, len(items))
	for i := range items {
		out[i] = partnerAccountJSON{
			Account: items[i].Account, State: items[i].State,
			CreationTime: timeToEpoch(items[i].CreatedAt), ExpirationTime: timeToEpoch(items[i].ExpiresAt),
		}
	}

	wire.WriteJSON(w, struct {
		PartnerEventSourceAccounts []partnerAccountJSON `json:"PartnerEventSourceAccounts"`
		NextToken                  string               `json:"NextToken,omitempty"`
	}{out, next})
}

func putPartnerEvents(mgr partnerSourceManager, w http.ResponseWriter, r *http.Request) {
	var req putPartnerEventsRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	events := make([]ebdriver.Event, len(req.Entries))

	for i := range req.Entries {
		e := &req.Entries[i]
		events[i] = ebdriver.Event{
			Source: e.Source, DetailType: e.DetailType, Detail: e.Detail, Resources: e.Resources, Time: entryTime(e.Time),
		}
	}

	results, err := mgr.PutPartnerEventEntries(r.Context(), events)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeEntryResults(w, results)
}

func describeEventSource(mgr partnerSourceManager, w http.ResponseWriter, r *http.Request) {
	var req partnerSourceRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	src, err := mgr.DescribeEventSource(r.Context(), req.Name)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, toEventSourceJSON(src))
}

func listEventSources(mgr partnerSourceManager, w http.ResponseWriter, r *http.Request) {
	var req listSourcesRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	items, next, err := mgr.ListEventSources(r.Context(), ebdriver.EventSourceFilter{
		NamePrefix: req.NamePrefix, Limit: req.Limit, NextToken: req.NextToken,
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	out := make([]eventSourceJSON, len(items))
	for i := range items {
		out[i] = toEventSourceJSON(&items[i])
	}

	wire.WriteJSON(w, struct {
		EventSources []eventSourceJSON `json:"EventSources"`
		NextToken    string            `json:"NextToken,omitempty"`
	}{out, next})
}

// sourceAction serves an operation that takes only the event source name and
// answers an empty body.
func sourceAction(act func(partnerSourceManager, context.Context, string) error) partnerSourceOp {
	return func(mgr partnerSourceManager, w http.ResponseWriter, r *http.Request) {
		var req partnerSourceRequest
		if !wire.DecodeJSON(w, r, &req) {
			return
		}

		if err := act(mgr, r.Context(), req.Name); err != nil {
			writeErr(w, err)
			return
		}

		wire.WriteJSON(w, struct{}{})
	}
}
