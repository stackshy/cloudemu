// Package savingsplans implements the AWS Savings Plans API (api version
// 2019-06-28) as a server.Handler. Point the real
// aws-sdk-go-v2/service/savingsplans client (or the `aws savingsplans` CLI) at a
// Server registered with this handler and the plan purchase/describe lifecycle
// runs against an in-memory backend.
//
// Savings Plans speaks REST-JSON with path-based operation dispatch — each
// operation is POST /{OperationName} with a JSON body (no X-Amz-Target). Plan
// state lives only in the wire server (no portable driver represents Savings
// Plans), so the handler owns a self-contained store rather than a three-layer
// provider driver. A purchased plan lands active immediately, or queued when its
// purchaseTime is in the future; DeleteQueuedSavingsPlan retires a queued plan to
// queued-deleted.
//
// The store also satisfies services/cost.Commitments (Commitments()), so a later
// Cost Explorer coverage/utilization handler reads the real purchased commitments
// the billing engine amortizes.
package savingsplans

import (
	"net/http"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
	"github.com/stackshy/cloudemu/v2/services/cost"
)

// Handler serves Savings Plans REST-JSON requests backed by an in-memory store.
type Handler struct {
	store  *store
	routes map[string]http.HandlerFunc
}

// New returns a Savings Plans handler. accountID shapes generated plan ARNs
// (which are global — no region segment); region tags purchased plans; clock
// (nil = real clock) drives the queued/active timeline for deterministic tests.
func New(accountID, region string, clock config.Clock) *Handler {
	h := &Handler{store: newStore(accountID, region, clock)}
	h.routes = map[string]http.HandlerFunc{
		"/CreateSavingsPlan":                 h.createSavingsPlan,
		"/DeleteQueuedSavingsPlan":           h.deleteQueuedSavingsPlan,
		"/DescribeSavingsPlans":              h.describeSavingsPlans,
		"/DescribeSavingsPlansOfferings":     h.describeOfferings,
		"/DescribeSavingsPlansOfferingRates": h.describeOfferingRates,
		"/DescribeSavingsPlanRates":          h.describePlanRates,
		"/ListTagsForResource":               h.listTags,
		"/TagResource":                       h.tagResource,
		"/UntagResource":                     h.untagResource,
	}

	return h
}

// Commitments exposes the store as a cost.Commitments source so a later Cost
// Explorer coverage/utilization handler (billing-parity step 3) can amortize the
// real Savings Plans purchased here. Wire it in server/aws/aws.go's New once that
// handler exists.
func (h *Handler) Commitments() cost.Commitments { return h.store }

// Matches returns true for Savings Plans requests: a POST to one of the known
// /{OperationName} paths. The exact-path set keeps dispatch disjoint from the S3
// REST catch-all (no real bucket path is one of these operation names), so this
// handler must register before S3.
func (h *Handler) Matches(r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}

	_, ok := h.routes[r.URL.Path]

	return ok
}

// ServeHTTP dispatches on the request path.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if fn, ok := h.routes[r.URL.Path]; ok {
		fn(w, r)

		return
	}

	wire.WriteJSONError(w, http.StatusBadRequest, "ValidationException", "unsupported operation: "+r.URL.Path)
}

func (h *Handler) createSavingsPlan(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SavingsPlanOfferingID string            `json:"savingsPlanOfferingId"`
		Commitment            string            `json:"commitment"`
		ClientToken           string            `json:"clientToken"`
		PurchaseTime          *float64          `json:"purchaseTime"` // epoch seconds
		UpfrontPaymentAmount  string            `json:"upfrontPaymentAmount"`
		Tags                  map[string]string `json:"tags"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	in := createInput{
		savingsPlanOfferingID: req.SavingsPlanOfferingID,
		commitment:            req.Commitment,
		clientToken:           req.ClientToken,
		upfrontPaymentAmount:  req.UpfrontPaymentAmount,
		tags:                  req.Tags,
	}
	if req.PurchaseTime != nil {
		in.purchaseTime = epochToTime(*req.PurchaseTime)
	}

	id, err := h.store.create(&in)
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"savingsPlanId": id})
}

func (h *Handler) deleteQueuedSavingsPlan(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SavingsPlanID string `json:"savingsPlanId"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := h.store.deleteQueued(req.SavingsPlanID); err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{})
}

func (h *Handler) describeSavingsPlans(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Filters         []wireFilter `json:"filters"`
		SavingsPlanIDs  []string     `json:"savingsPlanIds"`
		SavingsPlanArns []string     `json:"savingsPlanArns"`
		States          []string     `json:"states"`
		MaxResults      int          `json:"maxResults"`
		NextToken       string       `json:"nextToken"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	f := newPlanFilter(req.SavingsPlanIDs, req.SavingsPlanArns, req.States, req.Filters)
	plans := h.store.describe(f)

	out := make([]map[string]any, 0, len(plans))
	for _, p := range plans {
		out = append(out, planToWire(p))
	}

	wire.WriteJSON(w, map[string]any{"savingsPlans": out})
}

func (h *Handler) describeOfferings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OfferingIDs []string     `json:"offeringIds"`
		PlanTypes   []string     `json:"planTypes"`
		Filters     []wireFilter `json:"filters"`
		ProductType string       `json:"productType"`
		MaxResults  int          `json:"maxResults"`
		NextToken   string       `json:"nextToken"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	ids := toSet(req.OfferingIDs)
	types := toSet(req.PlanTypes)

	out := make([]map[string]any, 0, len(h.store.offerings))

	for i := range h.store.offerings {
		o := &h.store.offerings[i]

		if len(ids) > 0 {
			if _, ok := ids[o.id]; !ok {
				continue
			}
		}

		if len(types) > 0 {
			if _, ok := types[o.planType]; !ok {
				continue
			}
		}

		out = append(out, offeringToWire(o))
	}

	wire.WriteJSON(w, map[string]any{"searchResults": out})
}

func (h *Handler) describeOfferingRates(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SavingsPlanOfferingIDs []string `json:"savingsPlanOfferingIds"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	rates := make([]map[string]any, 0, len(h.store.offerings))

	for i := range h.store.offerings {
		rates = append(rates, offeringRateToWire(&h.store.offerings[i]))
	}

	wire.WriteJSON(w, map[string]any{"searchResults": rates})
}

func (h *Handler) describePlanRates(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SavingsPlanID string `json:"savingsPlanId"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	f := newPlanFilter([]string{req.SavingsPlanID}, nil, nil, nil)

	plans := h.store.describe(f)
	if len(plans) == 0 {
		writeErr(w, cerrors.Newf(cerrors.NotFound, "savings plan not found: %s", req.SavingsPlanID))

		return
	}

	wire.WriteJSON(w, map[string]any{
		"savingsPlanId": req.SavingsPlanID,
		"searchResults": planRatesFor(plans[0]),
	})
}

func (h *Handler) listTags(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResourceArn string `json:"resourceArn"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	tags, ok := h.store.tagsOf(req.ResourceArn)
	if !ok {
		writeErr(w, cerrors.Newf(cerrors.NotFound, "savings plan not found: %s", req.ResourceArn))

		return
	}

	if tags == nil {
		tags = map[string]string{}
	}

	wire.WriteJSON(w, map[string]any{"tags": tags})
}

func (h *Handler) tagResource(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResourceArn string            `json:"resourceArn"`
		Tags        map[string]string `json:"tags"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := h.store.tag(req.ResourceArn, req.Tags); err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{})
}

func (h *Handler) untagResource(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResourceArn string   `json:"resourceArn"`
		TagKeys     []string `json:"tagKeys"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := h.store.untag(req.ResourceArn, req.TagKeys); err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{})
}

// epochToTime converts epoch seconds (the wire form of a Savings Plans timestamp)
// to a UTC time, preserving fractional-second precision.
func epochToTime(sec float64) time.Time {
	const nanosPerSec = float64(time.Second)

	return time.Unix(0, int64(sec*nanosPerSec)).UTC()
}

// writeErr maps a store/validation error to the closest Savings Plans REST-JSON
// error type.
func writeErr(w http.ResponseWriter, err error) {
	msg := cerrors.Message(err)

	switch {
	case cerrors.IsNotFound(err):
		wire.WriteJSONError(w, http.StatusNotFound, "ResourceNotFoundException", msg)
	case cerrors.IsInvalidArgument(err), cerrors.IsFailedPrecondition(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "ValidationException", msg)
	default:
		wire.WriteJSONError(w, http.StatusInternalServerError, "InternalServerException", msg)
	}
}
