package driver

// Marketplace model endpoint status values. Status is the registration status
// (types.Status); EndpointStatus mirrors the SageMaker endpoint state.
const (
	MarketplaceEndpointStatusRegistered   = "REGISTERED"
	MarketplaceEndpointStatusIncompatible = "INCOMPATIBLE_ENDPOINT"
	MarketplaceEndpointStatusInService    = "InService"
)

// Foundation-model agreement / availability values.
const (
	AgreementStatusAvailable         = "AVAILABLE"
	AgreementStatusPending           = "PENDING"
	AuthorizationStatusAuthorized    = "AUTHORIZED"
	AuthorizationStatusNotAuthorized = "NOT_AUTHORIZED"
	AvailabilityAvailable            = "AVAILABLE"
)

// MarketplaceEndpointConfig describes a marketplace model endpoint to create.
// EndpointConfig is the opaque endpointConfig union JSON carried through
// verbatim.
type MarketplaceEndpointConfig struct {
	EndpointName          string
	ModelSourceIdentifier string
	EndpointConfig        []byte
	AcceptEula            bool
	ClientRequestToken    string
	Tags                  map[string]string
}

// MarketplaceEndpoint describes a deployed marketplace model endpoint.
type MarketplaceEndpoint struct {
	EndpointARN           string
	ModelSourceIdentifier string
	EndpointConfig        []byte
	EndpointStatus        string
	Status                string
	EndpointStatusMessage string
	StatusMessage         string
	CreatedAt             string
	UpdatedAt             string
}

// FoundationModelOffer is a synthetic agreement offer for a foundation model.
type FoundationModelOffer struct {
	OfferToken string
	OfferID    string
}

// FoundationModelAvailability describes the agreement, authorization,
// entitlement, and region availability of a foundation model.
type FoundationModelAvailability struct {
	AgreementStatus         string
	AuthorizationStatus     string
	EntitlementAvailability string
	RegionAvailability      string
}
