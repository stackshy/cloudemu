package eventbridge

import (
	"context"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
	ebdriver "github.com/stackshy/cloudemu/v2/services/eventbus/driver"
)

// connectionManager is the AWS-specific EventBridge connection and API
// destination surface, asserted against the provider (not part of the portable
// EventBus driver).
type connectionManager interface {
	CreateConnection(ctx context.Context, cfg *ebdriver.ConnectionConfig) (*ebdriver.Connection, error)
	DescribeConnection(ctx context.Context, name string) (*ebdriver.Connection, error)
	UpdateConnection(ctx context.Context, upd *ebdriver.ConnectionUpdate) (*ebdriver.Connection, error)
	DeauthorizeConnection(ctx context.Context, name string) (*ebdriver.Connection, error)
	DeleteConnection(ctx context.Context, name string) (*ebdriver.Connection, error)
	ListConnections(ctx context.Context, filter ebdriver.ConnectionFilter) ([]ebdriver.Connection, error)
	CreateAPIDestination(ctx context.Context, cfg *ebdriver.APIDestinationConfig) (*ebdriver.APIDestination, error)
	DescribeAPIDestination(ctx context.Context, name string) (*ebdriver.APIDestination, error)
	UpdateAPIDestination(ctx context.Context, upd *ebdriver.APIDestinationUpdate) (*ebdriver.APIDestination, error)
	DeleteAPIDestination(ctx context.Context, name string) error
	ListAPIDestinations(ctx context.Context, filter ebdriver.APIDestinationFilter) ([]ebdriver.APIDestination, error)
}

type connectionOp func(mgr connectionManager, w http.ResponseWriter, r *http.Request)

// Pagination keys of the connection and API destination lists.
var (
	connectionKey  = func(c connectionJSON) string { return c.Name }     //nolint:gochecknoglobals // pure function value
	destinationKey = func(d apiDestinationJSON) string { return d.Name } //nolint:gochecknoglobals // pure function value
)

// connectionOps maps each connection and API destination operation to its handler.
func connectionOps() map[string]connectionOp {
	return map[string]connectionOp{
		"CreateConnection":       createConnection,
		"DescribeConnection":     describeConnection,
		"UpdateConnection":       updateConnection,
		"DeauthorizeConnection":  connectionStateOp(connectionManager.DeauthorizeConnection),
		"DeleteConnection":       connectionStateOp(connectionManager.DeleteConnection),
		"ListConnections":        listOp(fetchConnections, connectionListItem, connectionKey, connectionPage),
		"CreateApiDestination":   createAPIDestination,
		"DescribeApiDestination": describeAPIDestination,
		"UpdateApiDestination":   updateAPIDestination,
		"DeleteApiDestination":   deleteAPIDestination,
		"ListApiDestinations":    listOp(fetchDestinations, destinationListItem, destinationKey, destinationPage),
	}
}

// serveConnectionOp serves op when it is a connection or API destination
// operation, reporting whether it did.
func (h *Handler) serveConnectionOp(w http.ResponseWriter, r *http.Request, op string) bool {
	fn, ok := connectionOps()[op]
	if !ok {
		return false
	}

	mgr, ok := h.bus.(connectionManager)
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "connections not supported"))
		return true
	}

	fn(mgr, w, r)

	return true
}

type httpParamJSON struct {
	Key           string `json:"Key"`
	Value         string `json:"Value,omitempty"`
	IsValueSecret bool   `json:"IsValueSecret,omitempty"`
}

type httpParamsJSON struct {
	HeaderParameters      []httpParamJSON `json:"HeaderParameters,omitempty"`
	QueryStringParameters []httpParamJSON `json:"QueryStringParameters,omitempty"`
	BodyParameters        []httpParamJSON `json:"BodyParameters,omitempty"`
}

type basicAuthJSON struct {
	Username string `json:"Username,omitempty"`
	Password string `json:"Password,omitempty"`
}

type apiKeyAuthJSON struct {
	APIKeyName  string `json:"ApiKeyName,omitempty"`
	APIKeyValue string `json:"ApiKeyValue,omitempty"`
}

type oauthClientJSON struct {
	ClientID     string `json:"ClientID,omitempty"`
	ClientSecret string `json:"ClientSecret,omitempty"`
}

type oauthJSON struct {
	ClientParameters      *oauthClientJSON `json:"ClientParameters,omitempty"`
	AuthorizationEndpoint string           `json:"AuthorizationEndpoint,omitempty"`
	HTTPMethod            string           `json:"HttpMethod,omitempty"`
	OAuthHTTPParameters   *httpParamsJSON  `json:"OAuthHttpParameters,omitempty"`
}

type authParamsJSON struct {
	BasicAuthParameters      *basicAuthJSON  `json:"BasicAuthParameters,omitempty"`
	OAuthParameters          *oauthJSON      `json:"OAuthParameters,omitempty"`
	APIKeyAuthParameters     *apiKeyAuthJSON `json:"ApiKeyAuthParameters,omitempty"`
	InvocationHTTPParameters *httpParamsJSON `json:"InvocationHttpParameters,omitempty"`
}

type connectionNameRequest struct {
	Name string `json:"Name"`
}

type createConnectionRequest struct {
	Name              string          `json:"Name"`
	Description       string          `json:"Description"`
	AuthorizationType string          `json:"AuthorizationType"`
	AuthParameters    *authParamsJSON `json:"AuthParameters"`
	KmsKeyIdentifier  string          `json:"KmsKeyIdentifier"`
}

type updateConnectionRequest struct {
	Name              string          `json:"Name"`
	Description       *string         `json:"Description"`
	AuthorizationType *string         `json:"AuthorizationType"`
	AuthParameters    *authParamsJSON `json:"AuthParameters"`
	KmsKeyIdentifier  *string         `json:"KmsKeyIdentifier"`
}

// connectionStateResponse is the reply of Create, Update, Deauthorize and
// DeleteConnection.
type connectionStateResponse struct {
	ConnectionArn      string   `json:"ConnectionArn"`
	ConnectionState    string   `json:"ConnectionState"`
	CreationTime       *float64 `json:"CreationTime,omitempty"`
	LastModifiedTime   *float64 `json:"LastModifiedTime,omitempty"`
	LastAuthorizedTime *float64 `json:"LastAuthorizedTime,omitempty"`
}

type connectionJSON struct {
	ConnectionArn      string          `json:"ConnectionArn"`
	Name               string          `json:"Name"`
	Description        string          `json:"Description,omitempty"`
	ConnectionState    string          `json:"ConnectionState"`
	StateReason        string          `json:"StateReason,omitempty"`
	AuthorizationType  string          `json:"AuthorizationType"`
	SecretArn          string          `json:"SecretArn,omitempty"`
	KmsKeyIdentifier   string          `json:"KmsKeyIdentifier,omitempty"`
	AuthParameters     *authParamsJSON `json:"AuthParameters,omitempty"`
	CreationTime       *float64        `json:"CreationTime,omitempty"`
	LastModifiedTime   *float64        `json:"LastModifiedTime,omitempty"`
	LastAuthorizedTime *float64        `json:"LastAuthorizedTime,omitempty"`
}

type listConnectionsResponse struct {
	Connections []connectionJSON `json:"Connections"`
	NextToken   string           `json:"NextToken,omitempty"`
}

func fromHTTPParams(p *httpParamsJSON) *ebdriver.ConnectionHTTPParameters {
	if p == nil {
		return nil
	}

	conv := func(list []httpParamJSON) []ebdriver.HTTPParameter {
		out := make([]ebdriver.HTTPParameter, 0, len(list))
		for _, e := range list {
			out = append(out, ebdriver.HTTPParameter{Key: e.Key, Value: e.Value, IsValueSecret: e.IsValueSecret})
		}

		return out
	}

	return &ebdriver.ConnectionHTTPParameters{
		HeaderParameters:      conv(p.HeaderParameters),
		QueryStringParameters: conv(p.QueryStringParameters),
		BodyParameters:        conv(p.BodyParameters),
	}
}

func toHTTPParams(p *ebdriver.ConnectionHTTPParameters) *httpParamsJSON {
	if p == nil {
		return nil
	}

	conv := func(list []ebdriver.HTTPParameter) []httpParamJSON {
		out := make([]httpParamJSON, 0, len(list))
		for _, e := range list {
			out = append(out, httpParamJSON{Key: e.Key, Value: e.Value, IsValueSecret: e.IsValueSecret})
		}

		return out
	}

	return &httpParamsJSON{
		HeaderParameters:      conv(p.HeaderParameters),
		QueryStringParameters: conv(p.QueryStringParameters),
		BodyParameters:        conv(p.BodyParameters),
	}
}

func fromAuthParams(a *authParamsJSON) *ebdriver.ConnectionAuthParameters {
	if a == nil {
		return nil
	}

	out := &ebdriver.ConnectionAuthParameters{InvocationHTTPParameters: fromHTTPParams(a.InvocationHTTPParameters)}

	if b := a.BasicAuthParameters; b != nil {
		out.Basic = &ebdriver.ConnectionBasicAuth{Username: b.Username, Password: b.Password}
	}

	if k := a.APIKeyAuthParameters; k != nil {
		out.APIKey = &ebdriver.ConnectionAPIKeyAuth{APIKeyName: k.APIKeyName, APIKeyValue: k.APIKeyValue}
	}

	if o := a.OAuthParameters; o != nil {
		out.OAuth = &ebdriver.ConnectionOAuth{
			AuthorizationEndpoint: o.AuthorizationEndpoint,
			HTTPMethod:            o.HTTPMethod,
			OAuthHTTPParameters:   fromHTTPParams(o.OAuthHTTPParameters),
		}

		if c := o.ClientParameters; c != nil {
			out.OAuth.ClientParameters = &ebdriver.ConnectionOAuthClient{ClientID: c.ClientID, ClientSecret: c.ClientSecret}
		}
	}

	return out
}

func toAuthParams(a *ebdriver.ConnectionAuthParameters) *authParamsJSON {
	out := &authParamsJSON{InvocationHTTPParameters: toHTTPParams(a.InvocationHTTPParameters)}

	if b := a.Basic; b != nil {
		out.BasicAuthParameters = &basicAuthJSON{Username: b.Username, Password: b.Password}
	}

	if k := a.APIKey; k != nil {
		out.APIKeyAuthParameters = &apiKeyAuthJSON{APIKeyName: k.APIKeyName, APIKeyValue: k.APIKeyValue}
	}

	if o := a.OAuth; o != nil {
		out.OAuthParameters = &oauthJSON{
			AuthorizationEndpoint: o.AuthorizationEndpoint,
			HTTPMethod:            o.HTTPMethod,
			OAuthHTTPParameters:   toHTTPParams(o.OAuthHTTPParameters),
		}

		if c := o.ClientParameters; c != nil {
			out.OAuthParameters.ClientParameters = &oauthClientJSON{ClientID: c.ClientID, ClientSecret: c.ClientSecret}
		}
	}

	return out
}

func toConnectionState(c *ebdriver.Connection) connectionStateResponse {
	return connectionStateResponse{
		ConnectionArn:      c.ARN,
		ConnectionState:    c.State,
		CreationTime:       timeToEpoch(c.CreatedAt),
		LastModifiedTime:   timeToEpoch(c.LastModifiedAt),
		LastAuthorizedTime: timeToEpoch(c.LastAuthorizedAt),
	}
}

func toConnectionJSON(c *ebdriver.Connection, full bool) connectionJSON {
	out := connectionJSON{
		ConnectionArn:      c.ARN,
		Name:               c.Name,
		ConnectionState:    c.State,
		StateReason:        c.StateReason,
		AuthorizationType:  c.AuthorizationType,
		CreationTime:       timeToEpoch(c.CreatedAt),
		LastModifiedTime:   timeToEpoch(c.LastModifiedAt),
		LastAuthorizedTime: timeToEpoch(c.LastAuthorizedAt),
	}

	if full {
		out.Description = c.Description
		out.SecretArn = c.SecretARN
		out.KmsKeyIdentifier = c.KMSKeyIdentifier
		out.AuthParameters = toAuthParams(&c.AuthParameters)
	}

	return out
}

func createConnection(mgr connectionManager, w http.ResponseWriter, r *http.Request) {
	var req createConnectionRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	c, err := mgr.CreateConnection(r.Context(), &ebdriver.ConnectionConfig{
		Name:              req.Name,
		Description:       req.Description,
		AuthorizationType: req.AuthorizationType,
		KMSKeyIdentifier:  req.KmsKeyIdentifier,
		AuthParameters:    fromAuthParams(req.AuthParameters),
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	resp := toConnectionState(c)
	resp.LastAuthorizedTime = nil

	wire.WriteJSON(w, resp)
}

func describeConnection(mgr connectionManager, w http.ResponseWriter, r *http.Request) {
	var req connectionNameRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	c, err := mgr.DescribeConnection(r.Context(), req.Name)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, toConnectionJSON(c, true))
}

func updateConnection(mgr connectionManager, w http.ResponseWriter, r *http.Request) {
	var req updateConnectionRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	c, err := mgr.UpdateConnection(r.Context(), &ebdriver.ConnectionUpdate{
		Name:              req.Name,
		Description:       req.Description,
		AuthorizationType: req.AuthorizationType,
		KMSKeyIdentifier:  req.KmsKeyIdentifier,
		AuthParameters:    fromAuthParams(req.AuthParameters),
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, toConnectionState(c))
}

// connectionStateOp builds the handler of an operation that takes a connection
// name and answers with the connection's state (Deauthorize, Delete).
func connectionStateOp(
	call func(mgr connectionManager, ctx context.Context, name string) (*ebdriver.Connection, error),
) connectionOp {
	return func(mgr connectionManager, w http.ResponseWriter, r *http.Request) {
		var req connectionNameRequest
		if !wire.DecodeJSON(w, r, &req) {
			return
		}

		c, err := call(mgr, r.Context(), req.Name)
		if err != nil {
			writeErr(w, err)
			return
		}

		wire.WriteJSON(w, toConnectionState(c))
	}
}

func fetchConnections(ctx context.Context, mgr connectionManager, req *listFilterRequest) ([]ebdriver.Connection, error) {
	return mgr.ListConnections(ctx, ebdriver.ConnectionFilter{NamePrefix: req.NamePrefix, State: req.ConnectionState})
}

func connectionListItem(c *ebdriver.Connection) connectionJSON { return toConnectionJSON(c, false) }

func connectionPage(page []connectionJSON, next string) any {
	return listConnectionsResponse{Connections: page, NextToken: next}
}
