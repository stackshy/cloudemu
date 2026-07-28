package bedrock

import (
	"encoding/json"
	"net/http"
	"strings"

	bedrockdriver "github.com/stackshy/cloudemu/v2/services/bedrock/driver"
)

// --- marketplace model endpoint wire types ---

type createMarketplaceEndpointRequest struct {
	EndpointConfig        json.RawMessage `json:"endpointConfig"`
	EndpointName          string          `json:"endpointName"`
	ModelSourceIdentifier string          `json:"modelSourceIdentifier"`
	AcceptEula            bool            `json:"acceptEula"`
	ClientRequestToken    string          `json:"clientRequestToken"`
	Tags                  []tagPair       `json:"tags"`
}

type updateMarketplaceEndpointRequest struct {
	EndpointConfig     json.RawMessage `json:"endpointConfig"`
	ClientRequestToken string          `json:"clientRequestToken"`
}

type registerMarketplaceEndpointRequest struct {
	ModelSourceIdentifier string `json:"modelSourceIdentifier"`
}

type marketplaceEndpointJSON struct {
	EndpointARN           string          `json:"endpointArn"`
	ModelSourceIdentifier string          `json:"modelSourceIdentifier,omitempty"`
	EndpointConfig        json.RawMessage `json:"endpointConfig,omitempty"`
	EndpointStatus        string          `json:"endpointStatus,omitempty"`
	Status                string          `json:"status,omitempty"`
	CreatedAt             string          `json:"createdAt,omitempty"`
	UpdatedAt             string          `json:"updatedAt,omitempty"`
	EndpointStatusMessage string          `json:"endpointStatusMessage,omitempty"`
	StatusMessage         string          `json:"statusMessage,omitempty"`
}

type marketplaceEndpointSummaryJSON struct {
	EndpointARN           string `json:"endpointArn"`
	ModelSourceIdentifier string `json:"modelSourceIdentifier,omitempty"`
	Status                string `json:"status,omitempty"`
	CreatedAt             string `json:"createdAt,omitempty"`
	UpdatedAt             string `json:"updatedAt,omitempty"`
}

type marketplaceEndpointResponse struct {
	MarketplaceModelEndpoint marketplaceEndpointJSON `json:"marketplaceModelEndpoint"`
}

type listMarketplaceEndpointsResponse struct {
	MarketplaceModelEndpoints []marketplaceEndpointSummaryJSON `json:"marketplaceModelEndpoints"`
	NextToken                 string                           `json:"nextToken,omitempty"`
}

// --- foundation model agreement wire types ---

type createFMAgreementRequest struct {
	ModelID    string `json:"modelId"`
	OfferToken string `json:"offerToken"`
}

type createFMAgreementResponse struct {
	ModelID string `json:"modelId,omitempty"`
}

type deleteFMAgreementRequest struct {
	ModelID string `json:"modelId"`
}

// fmOfferTermDetailsJSON is emitted as an empty object; the emulator returns no
// term details for synthetic offers.
type fmOfferTermDetailsJSON struct{}

type fmOfferJSON struct {
	OfferToken  string                 `json:"offerToken,omitempty"`
	OfferID     string                 `json:"offerId,omitempty"`
	TermDetails fmOfferTermDetailsJSON `json:"termDetails"`
}

type listFMAgreementOffersResponse struct {
	ModelID string        `json:"modelId,omitempty"`
	Offers  []fmOfferJSON `json:"offers"`
}

type agreementAvailabilityJSON struct {
	Status string `json:"status,omitempty"`
}

type getFMAvailabilityResponse struct {
	ModelID                 string                     `json:"modelId,omitempty"`
	AgreementAvailability   *agreementAvailabilityJSON `json:"agreementAvailability,omitempty"`
	AuthorizationStatus     string                     `json:"authorizationStatus,omitempty"`
	EntitlementAvailability string                     `json:"entitlementAvailability,omitempty"`
	RegionAvailability      string                     `json:"regionAvailability,omitempty"`
}

// --- dispatcher ---

// serveMarketplaceAgreements routes the marketplace-model-endpoint and
// foundation-model-agreement control-plane surfaces. Split out of ServeHTTP to
// keep each dispatcher small.
func (h *Handler) serveMarketplaceAgreements(w http.ResponseWriter, r *http.Request, p string) {
	switch {
	case p == pathCreateFMAgreement:
		h.createFoundationModelAgreement(w, r)
	case p == pathDeleteFMAgreement:
		h.deleteFoundationModelAgreement(w, r)
	case p == prefixListFMAgreementOffers || strings.HasPrefix(p, prefixListFMAgreementOffers+"/"):
		h.listFoundationModelAgreementOffers(w, r, strings.TrimPrefix(strings.TrimPrefix(p, prefixListFMAgreementOffers), "/"))
	case p == prefixFMAvailability || strings.HasPrefix(p, prefixFMAvailability+"/"):
		h.getFoundationModelAvailability(w, r, strings.TrimPrefix(strings.TrimPrefix(p, prefixFMAvailability), "/"))
	default:
		h.serveMarketplaceEndpoints(w, r, strings.TrimPrefix(strings.TrimPrefix(p, prefixMarketplaceEndpoints), "/"))
	}
}

// --- marketplace endpoint dispatch + operations ---

// serveMarketplaceEndpoints handles /marketplace-model/endpoints and its
// resource + registration sub-paths. The endpoint ARN contains slashes, so it
// is the entire remainder unless it carries the /registration suffix.
func (h *Handler) serveMarketplaceEndpoints(w http.ResponseWriter, r *http.Request, rest string) {
	switch {
	case rest == "":
		h.marketplaceEndpointCollection(w, r)
	case strings.HasSuffix(rest, suffixRegistration):
		h.marketplaceEndpointRegistration(w, r, strings.TrimSuffix(rest, suffixRegistration))
	default:
		h.marketplaceEndpointResource(w, r, rest)
	}
}

// marketplaceEndpointCollection handles POST (create) and GET (list) on the
// /marketplace-model/endpoints collection.
func (h *Handler) marketplaceEndpointCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.createMarketplaceEndpoint(w, r)
	case http.MethodGet:
		h.listMarketplaceEndpoints(w, r)
	default:
		methodNotAllowed(w)
	}
}

// marketplaceEndpointResource handles GET/PATCH/DELETE on a single endpoint ARN.
func (h *Handler) marketplaceEndpointResource(w http.ResponseWriter, r *http.Request, arn string) {
	switch r.Method {
	case http.MethodGet:
		h.getMarketplaceEndpoint(w, r, arn)
	case http.MethodPatch:
		h.updateMarketplaceEndpoint(w, r, arn)
	case http.MethodDelete:
		h.deleteMarketplaceEndpoint(w, r, arn)
	default:
		methodNotAllowed(w)
	}
}

// marketplaceEndpointRegistration handles POST (register) and DELETE
// (deregister) on {endpointIdentifier}/registration.
func (h *Handler) marketplaceEndpointRegistration(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodPost:
		h.registerMarketplaceEndpoint(w, r, id)
	case http.MethodDelete:
		h.deregisterMarketplaceEndpoint(w, r, id)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) createMarketplaceEndpoint(w http.ResponseWriter, r *http.Request) {
	var in createMarketplaceEndpointRequest
	if !decodeJSON(w, r, &in) {
		return
	}

	endpoint, err := h.bedrock.CreateMarketplaceModelEndpoint(r.Context(), bedrockdriver.MarketplaceEndpointConfig{
		EndpointName:          in.EndpointName,
		ModelSourceIdentifier: in.ModelSourceIdentifier,
		EndpointConfig:        []byte(in.EndpointConfig),
		AcceptEula:            in.AcceptEula,
		ClientRequestToken:    in.ClientRequestToken,
		Tags:                  tagsToMap(in.Tags),
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, marketplaceEndpointResponse{MarketplaceModelEndpoint: toMarketplaceEndpointJSON(endpoint)})
}

func (h *Handler) getMarketplaceEndpoint(w http.ResponseWriter, r *http.Request, arn string) {
	endpoint, err := h.bedrock.GetMarketplaceModelEndpoint(r.Context(), arn)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, marketplaceEndpointResponse{MarketplaceModelEndpoint: toMarketplaceEndpointJSON(endpoint)})
}

func (h *Handler) listMarketplaceEndpoints(w http.ResponseWriter, r *http.Request) {
	endpoints, err := h.bedrock.ListMarketplaceModelEndpoints(r.Context())
	if err != nil {
		writeErr(w, err)

		return
	}

	filter := r.URL.Query().Get("modelSourceIdentifier")
	out := make([]marketplaceEndpointSummaryJSON, 0, len(endpoints))

	for i := range endpoints {
		if filter != "" && endpoints[i].ModelSourceIdentifier != filter {
			continue
		}

		out = append(out, toMarketplaceEndpointSummaryJSON(&endpoints[i]))
	}

	writeJSON(w, listMarketplaceEndpointsResponse{MarketplaceModelEndpoints: out})
}

func (h *Handler) updateMarketplaceEndpoint(w http.ResponseWriter, r *http.Request, arn string) {
	var in updateMarketplaceEndpointRequest
	if !decodeJSON(w, r, &in) {
		return
	}

	endpoint, err := h.bedrock.UpdateMarketplaceModelEndpoint(r.Context(), arn, []byte(in.EndpointConfig))
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, marketplaceEndpointResponse{MarketplaceModelEndpoint: toMarketplaceEndpointJSON(endpoint)})
}

func (h *Handler) deleteMarketplaceEndpoint(w http.ResponseWriter, r *http.Request, arn string) {
	if err := h.bedrock.DeleteMarketplaceModelEndpoint(r.Context(), arn); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, struct{}{})
}

func (h *Handler) registerMarketplaceEndpoint(w http.ResponseWriter, r *http.Request, id string) {
	var in registerMarketplaceEndpointRequest
	if !decodeJSON(w, r, &in) {
		return
	}

	endpoint, err := h.bedrock.RegisterMarketplaceModelEndpoint(r.Context(), id, in.ModelSourceIdentifier)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, marketplaceEndpointResponse{MarketplaceModelEndpoint: toMarketplaceEndpointJSON(endpoint)})
}

func (h *Handler) deregisterMarketplaceEndpoint(w http.ResponseWriter, r *http.Request, id string) {
	if err := h.bedrock.DeregisterMarketplaceModelEndpoint(r.Context(), id); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, struct{}{})
}

// --- foundation model agreement operations ---

func (h *Handler) createFoundationModelAgreement(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)

		return
	}

	var in createFMAgreementRequest
	if !decodeJSON(w, r, &in) {
		return
	}

	modelID, err := h.bedrock.CreateFoundationModelAgreement(r.Context(), in.ModelID, in.OfferToken)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, createFMAgreementResponse{ModelID: modelID})
}

func (h *Handler) deleteFoundationModelAgreement(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)

		return
	}

	var in deleteFMAgreementRequest
	if !decodeJSON(w, r, &in) {
		return
	}

	if err := h.bedrock.DeleteFoundationModelAgreement(r.Context(), in.ModelID); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, struct{}{})
}

func (h *Handler) listFoundationModelAgreementOffers(w http.ResponseWriter, r *http.Request, modelID string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)

		return
	}

	offers, err := h.bedrock.ListFoundationModelAgreementOffers(r.Context(), modelID, r.URL.Query().Get("offerType"))
	if err != nil {
		writeErr(w, err)

		return
	}

	out := make([]fmOfferJSON, 0, len(offers))
	for i := range offers {
		out = append(out, fmOfferJSON{OfferToken: offers[i].OfferToken, OfferID: offers[i].OfferID})
	}

	writeJSON(w, listFMAgreementOffersResponse{ModelID: modelID, Offers: out})
}

func (h *Handler) getFoundationModelAvailability(w http.ResponseWriter, r *http.Request, modelID string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)

		return
	}

	avail, err := h.bedrock.GetFoundationModelAvailability(r.Context(), modelID)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, getFMAvailabilityResponse{
		ModelID:                 modelID,
		AgreementAvailability:   &agreementAvailabilityJSON{Status: avail.AgreementStatus},
		AuthorizationStatus:     avail.AuthorizationStatus,
		EntitlementAvailability: avail.EntitlementAvailability,
		RegionAvailability:      avail.RegionAvailability,
	})
}

// --- converters ---

func toMarketplaceEndpointJSON(e *bedrockdriver.MarketplaceEndpoint) marketplaceEndpointJSON {
	return marketplaceEndpointJSON{
		EndpointARN:           e.EndpointARN,
		ModelSourceIdentifier: e.ModelSourceIdentifier,
		EndpointConfig:        json.RawMessage(e.EndpointConfig),
		EndpointStatus:        e.EndpointStatus,
		Status:                e.Status,
		CreatedAt:             e.CreatedAt,
		UpdatedAt:             e.UpdatedAt,
		EndpointStatusMessage: e.EndpointStatusMessage,
		StatusMessage:         e.StatusMessage,
	}
}

func toMarketplaceEndpointSummaryJSON(e *bedrockdriver.MarketplaceEndpoint) marketplaceEndpointSummaryJSON {
	return marketplaceEndpointSummaryJSON{
		EndpointARN:           e.EndpointARN,
		ModelSourceIdentifier: e.ModelSourceIdentifier,
		Status:                e.Status,
		CreatedAt:             e.CreatedAt,
		UpdatedAt:             e.UpdatedAt,
	}
}
