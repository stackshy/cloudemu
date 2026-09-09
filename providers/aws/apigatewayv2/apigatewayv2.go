// Package apigatewayv2 is an in-memory mock of Amazon API Gateway v2 (HTTP and
// WebSocket APIs). It models the control plane only: an API and its Route,
// Integration and Stage sub-collections, reachable over the apigatewayv2
// REST/JSON protocol. It is a separate service from API Gateway REST v1
// (providers/aws/apigateway), sharing no state or types.
package apigatewayv2

import (
	"context"
	"crypto/rand"
	"fmt"
	"sync"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/apigatewayv2/driver"
)

// Compile-time check that Mock implements driver.APIGatewayV2.
var _ driver.APIGatewayV2 = (*Mock)(nil)

// idLen is the length of an apigatewayv2 generated id (10 lowercase
// alphanumeric characters), matching the real API/Route/Integration id shape.
const idLen = 10

// idAlphabet is the character set ids draw from.
const idAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"

// Default selection expressions and integration values API Gateway applies
// when the create request omits them, returned verbatim so clients (Terraform)
// see no drift.
const (
	defaultRouteSelectionExpr  = "$request.method $request.path"
	defaultAPIKeySelectionExpr = "$request.header.x-api-key"
	defaultConnectionType      = "INTERNET"
	defaultPayloadFormat       = "1.0"
	defaultAuthorizationType   = "NONE"
)

// Integration timeout defaults: 30s for HTTP APIs, 29s for WebSocket APIs.
const (
	defaultHTTPTimeoutMillis      = 30000
	defaultWebSocketTimeoutMillis = 29000
)

// apiData is one API plus its full tree, guarded by its own lock so a single
// lock makes each control-plane op atomic and never exposes a half-written tree.
type apiData struct {
	mu           sync.RWMutex
	api          driver.API
	routes       map[string]*driver.Route
	integrations map[string]*driver.Integration
	stages       map[string]*driver.Stage
}

// Mock is an in-memory implementation of Amazon API Gateway v2.
type Mock struct {
	apis   *memstore.Store[*apiData]
	opts   *config.Options
	region string
}

// New creates a new API Gateway v2 mock.
func New(opts *config.Options) *Mock {
	region := opts.Region
	if region == "" {
		region = config.DefaultRegion
	}

	return &Mock{apis: memstore.New[*apiData](), opts: opts, region: region}
}

func (m *Mock) now() int64 { return m.opts.Clock.Now().UTC().Unix() }

// genID returns a random 10-character lowercase-alphanumeric id.
func genID() string {
	b := make([]byte, idLen)
	_, _ = rand.Read(b)

	for i := range b {
		b[i] = idAlphabet[int(b[i])%len(idAlphabet)]
	}

	return string(b)
}

// getAPI returns the stored API data for id, or a NotFound error.
func (m *Mock) getAPI(id string) (*apiData, error) {
	ad, ok := m.apis.Get(id)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "Invalid API identifier specified %s", id)
	}

	return ad, nil
}

// CreateAPI creates a new API with defaulted selection expressions and a
// computed execute-api endpoint.
func (m *Mock) CreateAPI(_ context.Context, in *driver.CreateAPIInput) (*driver.API, error) {
	if in.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "Name is required")
	}

	if in.ProtocolType != driver.ProtocolHTTP && in.ProtocolType != driver.ProtocolWebSocket {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "Invalid protocol type specified: %s", in.ProtocolType)
	}

	apiID := genID()
	api := driver.API{
		APIID: apiID, Name: in.Name, ProtocolType: in.ProtocolType,
		Description: in.Description, Version: in.Version,
		RouteSelectionExpression:  orDefault(in.RouteSelectionExpression, defaultRouteSelectionExpr),
		APIKeySelectionExpression: orDefault(in.APIKeySelectionExpression, defaultAPIKeySelectionExpr),
		DisableExecuteAPIEndpoint: in.DisableExecuteAPIEndpoint,
		APIEndpoint:               fmt.Sprintf("https://%s.execute-api.%s.amazonaws.com", apiID, m.region),
		CreatedDate:               m.now(),
		Tags:                      copyStrMap(in.Tags),
		CorsConfiguration:         copyCors(in.CorsConfiguration),
	}

	m.apis.Set(apiID, &apiData{
		api:          api,
		routes:       map[string]*driver.Route{},
		integrations: map[string]*driver.Integration{},
		stages:       map[string]*driver.Stage{},
	})

	out := copyAPI(&api)

	return &out, nil
}

// GetAPI returns a single API.
func (m *Mock) GetAPI(_ context.Context, apiID string) (*driver.API, error) {
	ad, err := m.getAPI(apiID)
	if err != nil {
		return nil, err
	}

	ad.mu.RLock()
	defer ad.mu.RUnlock()

	out := copyAPI(&ad.api)

	return &out, nil
}

// GetAPIs lists all APIs.
func (m *Mock) GetAPIs(_ context.Context) ([]driver.API, error) {
	all := m.apis.All()
	out := make([]driver.API, 0, len(all))

	for _, ad := range all {
		ad.mu.RLock()
		out = append(out, copyAPI(&ad.api))
		ad.mu.RUnlock()
	}

	return out, nil
}

// UpdateAPI applies the non-nil fields of in to the stored API (PATCH).
func (m *Mock) UpdateAPI(_ context.Context, apiID string, in *driver.UpdateAPIInput) (*driver.API, error) {
	ad, err := m.getAPI(apiID)
	if err != nil {
		return nil, err
	}

	ad.mu.Lock()
	defer ad.mu.Unlock()

	a := &ad.api
	setString(&a.Name, in.Name)
	setString(&a.Description, in.Description)
	setString(&a.Version, in.Version)
	setString(&a.RouteSelectionExpression, in.RouteSelectionExpression)
	setString(&a.APIKeySelectionExpression, in.APIKeySelectionExpression)
	setBool(&a.DisableExecuteAPIEndpoint, in.DisableExecuteAPIEndpoint)

	if in.CorsConfiguration != nil {
		a.CorsConfiguration = copyCors(in.CorsConfiguration)
	}

	out := copyAPI(a)

	return &out, nil
}

// DeleteAPI removes an API and its whole tree.
func (m *Mock) DeleteAPI(_ context.Context, apiID string) error {
	if !m.apis.Delete(apiID) {
		return cerrors.Newf(cerrors.NotFound, "Invalid API identifier specified %s", apiID)
	}

	return nil
}

// orDefault returns v when non-empty, else def.
func orDefault(v, def string) string {
	if v == "" {
		return def
	}

	return v
}

// setString sets *dst to *src when src is non-nil (PATCH helper).
func setString(dst, src *string) {
	if src != nil {
		*dst = *src
	}
}

// setBool sets *dst to *src when src is non-nil (PATCH helper).
func setBool(dst, src *bool) {
	if src != nil {
		*dst = *src
	}
}

func copyStrMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}

	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}

	return out
}

// copyCors returns a deep copy of a Cors, or nil.
func copyCors(c *driver.Cors) *driver.Cors {
	if c == nil {
		return nil
	}

	out := *c
	out.AllowHeaders = append([]string(nil), c.AllowHeaders...)
	out.AllowMethods = append([]string(nil), c.AllowMethods...)
	out.AllowOrigins = append([]string(nil), c.AllowOrigins...)
	out.ExposeHeaders = append([]string(nil), c.ExposeHeaders...)

	return &out
}

// copyAPI returns a deep copy of an API so callers never share its maps/slices.
func copyAPI(a *driver.API) driver.API {
	out := *a
	out.Tags = copyStrMap(a.Tags)
	out.CorsConfiguration = copyCors(a.CorsConfiguration)

	return out
}
