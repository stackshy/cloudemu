package notifications

import (
	"encoding/json"
	"net/http"
	"net/url"

	notifprovider "github.com/stackshy/cloudemu/v2/providers/oci/notifications"
	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
)

// serveSubscriptions routes the subscription collection, a single
// subscription, the two token endpoints and the actions on one.
func (h *Handler) serveSubscriptions(w http.ResponseWriter, r *http.Request, rt route) {
	switch {
	case rt.ID == "" && rt.Sub == "":
		h.serveSubscriptionCollection(w, r)
	case rt.Sub == "":
		h.serveSubscription(w, r, rt.ID)
	case rt.Sub == subConfirmation && rt.Action == "":
		h.confirmSubscription(w, r, rt.ID)
	case rt.Sub == subUnsubscription && rt.Action == "":
		h.unsubscribeByToken(w, r, rt.ID)
	case rt.Sub == subActions:
		h.serveSubscriptionAction(w, r, rt)
	default:
		notFound(w, r)
	}
}

// serveSubscriptionAction routes /subscriptions/{id}/actions/{action}.
func (h *Handler) serveSubscriptionAction(w http.ResponseWriter, r *http.Request, rt route) {
	switch rt.Action {
	case actionChangeCompartment:
		h.changeSubscriptionCompartment(w, r, rt.ID)
	case actionResendConfirm:
		h.resendConfirmation(w, r, rt.ID)
	default:
		notFound(w, r)
	}
}

func (h *Handler) serveSubscriptionCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.createSubscription(w, r)
	case http.MethodGet:
		h.listSubscriptions(w, r)
	default:
		methodNotAllowed(w, r)
	}
}

func (h *Handler) serveSubscription(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		h.getSubscription(w, r, id)
	case http.MethodPut:
		h.updateSubscription(w, r, id)
	case http.MethodDelete:
		h.deleteSubscription(w, r, id)
	default:
		methodNotAllowed(w, r)
	}
}

// createSubscription creates a subscription. It stays PENDING, and receives
// nothing, until it is confirmed with its token; an ORACLE_FUNCTIONS one is
// created ACTIVE, as ONS does not ask a function to confirm.
func (h *Handler) createSubscription(w http.ResponseWriter, r *http.Request) {
	var req createSubscriptionRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	if !refuseDefinedTags(w, r, req.DefinedTags) {
		return
	}

	if req.CompartmentID == "" {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "compartmentId is required")
		return
	}

	if req.TopicID == "" {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "topicId is required")
		return
	}

	if !h.requireCompartment(w, r, req.CompartmentID) {
		return
	}

	sub, err := h.extras.CreateSubscription(r.Context(), notifprovider.SubscriptionSpec{
		TopicID:       req.TopicID,
		CompartmentID: req.CompartmentID,
		Protocol:      req.Protocol,
		Endpoint:      req.Endpoint,
		Metadata:      req.Metadata,
		FreeformTags:  req.FreeformTags,
	})
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	writeSubscription(w, r, http.StatusCreated, sub)
}

func (h *Handler) getSubscription(w http.ResponseWriter, r *http.Request, id string) {
	sub, err := h.extras.GetSubscription(r.Context(), id)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	writeSubscription(w, r, http.StatusOK, sub)
}

// writeSubscription renders a single subscription in ONS's Subscription shape,
// with the etag header the SDK reads.
func writeSubscription(w http.ResponseWriter, r *http.Request, status int, sub *notifprovider.Subscription) {
	setEtag(w, sub.Etag)
	ocirest.WriteJSON(w, r, status, subscriptionWire(sub))
}

// listSubscriptions returns the subscriptions in a compartment, narrowed to
// one topic when topicId is given.
func (h *Handler) listSubscriptions(w http.ResponseWriter, r *http.Request) {
	compartmentID, given := ocirest.RequireCompartmentID(w, r)
	if !given {
		return
	}

	subs, err := h.extras.ListOCISubscriptions(r.Context(), compartmentID, r.URL.Query().Get("topicId"))
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	out := make([]subscriptionSummary, 0, len(subs))
	for i := range subs {
		out = append(out, subscriptionSummaryWire(&subs[i]))
	}

	page, ok := paginate(w, r, out)
	if !ok {
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, page)
}

// updateSubscription replaces a subscription's delivery policy and tags. The
// full subscription is returned; ONS's UpdateSubscriptionDetails is a subset
// of it, so an SDK decoding either sees the fields it expects.
func (h *Handler) updateSubscription(w http.ResponseWriter, r *http.Request, id string) {
	var req updateSubscriptionRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	if !refuseDefinedTags(w, r, req.DefinedTags) {
		return
	}

	sub, err := h.extras.UpdateSubscription(r.Context(), id, notifprovider.SubscriptionPatch{
		DeliveryPolicy: toDriverPolicy(req.DeliveryPolicy),
		FreeformTags:   req.FreeformTags,
		IfMatch:        ifMatch(r),
	})
	if err != nil {
		writeError(w, r, err)
		return
	}

	writeSubscription(w, r, http.StatusOK, sub)
}

func (h *Handler) deleteSubscription(w http.ResponseWriter, r *http.Request, id string) {
	if err := h.extras.DeleteSubscription(r.Context(), id, ifMatch(r)); err != nil {
		writeError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusNoContent, nil)
}

// confirmSubscription moves a subscription from PENDING to ACTIVE. ONS
// authenticates it with the token it mailed to the endpoint rather than with
// the caller's credentials, so it is a GET on the subscription.
func (h *Handler) confirmSubscription(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, r)
		return
	}

	token, protocol, ok := tokenParams(w, r)
	if !ok {
		return
	}

	result, err := h.extras.ConfirmSubscription(r.Context(), id, token, protocol)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, confirmationResult{
		TopicName:      result.TopicName,
		TopicID:        result.TopicID,
		Endpoint:       result.Endpoint,
		SubscriptionID: result.SubscriptionID,
		UnsubscribeURL: unsubscribeURL(r, result.SubscriptionID, result.Token, protocol),
		Message:        result.Message,
	})
}

// unsubscribeByToken serves the unsubscribe link ONS puts in every delivery.
func (h *Handler) unsubscribeByToken(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, r)
		return
	}

	token, protocol, ok := tokenParams(w, r)
	if !ok {
		return
	}

	if err := h.extras.UnsubscribeByToken(r.Context(), id, token, protocol); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, "subscription "+id+" removed")
}

// resendConfirmation re-issues the token of a subscription still PENDING.
func (h *Handler) resendConfirmation(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, r)
		return
	}

	sub, err := h.extras.ResendSubscriptionConfirmation(r.Context(), id)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, subscriptionWire(sub))
}

// changeSubscriptionCompartment moves a subscription. ONS runs it
// synchronously, unlike the topic move.
func (h *Handler) changeSubscriptionCompartment(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, r)
		return
	}

	var req changeCompartmentRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	if !h.requireCompartment(w, r, req.CompartmentID) {
		return
	}

	if err := h.extras.ChangeSubscriptionCompartment(r.Context(), id, req.CompartmentID); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusNoContent, nil)
}

// tokenParams reads the token and protocol the confirmation endpoints
// authenticate with. ONS marks both mandatory, so either missing is a 400.
func tokenParams(w http.ResponseWriter, r *http.Request) (token, protocol string, ok bool) {
	query := r.URL.Query()

	token, protocol = query.Get("token"), query.Get("protocol")

	switch {
	case token == "":
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "token is required")
		return "", "", false
	case protocol == "":
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "protocol is required")
		return "", "", false
	}

	return token, protocol, true
}

// unsubscribeURL is the link ONS hands back with a confirmation, pointing at
// this emulator's own origin.
func unsubscribeURL(r *http.Request, id, token, protocol string) string {
	query := url.Values{"token": {token}}
	if protocol != "" {
		query.Set("protocol", protocol)
	}

	return apiEndpoint(r) + "/" + apiVersion + "/" + segSubscriptions + "/" +
		url.PathEscape(id) + "/" + subUnsubscription + "?" + query.Encode()
}

// subscriptionWire renders ONS's Subscription, whose delivery policy is a
// JSON-encoded string in deliverPolicy.
func subscriptionWire(sub *notifprovider.Subscription) subscriptionResponse {
	out := subscriptionResponse{subscriptionFields: subscriptionBase(sub)}

	if p := toWirePolicy(sub.DeliveryPolicy); p != nil {
		// A struct of ints and strings always marshals.
		raw, _ := json.Marshal(p) //nolint:errchkjson // cannot fail for deliveryPolicy
		out.DeliverPolicy = string(raw)
	}

	return out
}

// subscriptionSummaryWire renders ONS's SubscriptionSummary, whose delivery
// policy is an object in deliveryPolicy.
func subscriptionSummaryWire(sub *notifprovider.Subscription) subscriptionSummary {
	return subscriptionSummary{
		subscriptionFields: subscriptionBase(sub),
		DeliveryPolicy:     toWirePolicy(sub.DeliveryPolicy),
	}
}

// subscriptionBase fills the fields Subscription and SubscriptionSummary
// share. The confirmation token rides along only while it is still needed.
func subscriptionBase(sub *notifprovider.Subscription) subscriptionFields {
	out := subscriptionFields{
		ID:             sub.ID,
		TopicID:        sub.TopicID,
		CompartmentID:  sub.CompartmentID,
		Protocol:       sub.Protocol,
		Endpoint:       sub.Endpoint,
		LifecycleState: sub.LifecycleState,
		CreatedTime:    sub.CreatedTime,
		Metadata:       sub.Metadata,
		Etag:           sub.Etag,
		FreeformTags:   sub.FreeformTags,
		DefinedTags:    definedTags{},
	}

	if out.FreeformTags == nil {
		out.FreeformTags = map[string]string{}
	}

	if sub.LifecycleState == notifprovider.StatePending {
		out.ConfirmationToken = sub.ConfirmationToken
	}

	return out
}

func toDriverPolicy(p *deliveryPolicy) *notifprovider.DeliveryPolicy {
	if p == nil {
		return nil
	}

	out := notifprovider.DeliveryPolicy{}
	if p.BackoffRetryPolicy != nil {
		out.BackoffRetryPolicy = &notifprovider.BackoffRetryPolicy{
			MaxRetryDuration: p.BackoffRetryPolicy.MaxRetryDuration,
			PolicyType:       p.BackoffRetryPolicy.PolicyType,
		}
	}

	return &out
}

func toWirePolicy(p *notifprovider.DeliveryPolicy) *deliveryPolicy {
	if p == nil {
		return nil
	}

	out := deliveryPolicy{}
	if p.BackoffRetryPolicy != nil {
		out.BackoffRetryPolicy = &backoffRetryPolicy{
			MaxRetryDuration: p.BackoffRetryPolicy.MaxRetryDuration,
			PolicyType:       p.BackoffRetryPolicy.PolicyType,
		}
	}

	return &out
}
