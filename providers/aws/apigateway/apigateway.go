// Package apigateway is an in-memory mock of Amazon API Gateway (REST API v1).
// It models the control plane (RestApi -> Resource tree -> Method ->
// Integration -> Deployment -> Stage) and the data plane: InvokeRoute resolves
// a request's method+path against a deployed stage and, for a Lambda proxy
// integration, invokes the target function through the injected LambdaInvoker
// seam and maps the function's {statusCode,headers,body} back to an HTTP
// response.
package apigateway

import (
	"context"
	"crypto/rand"
	"sync"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/apigateway/driver"
)

// Compile-time check that Mock implements driver.APIGateway.
var _ driver.APIGateway = (*Mock)(nil)

// idLen is the length of an API Gateway generated id (10 lowercase
// alphanumeric characters), matching the real REST API / resource / deployment
// id shape.
const idLen = 10

// idAlphabet is the character set REST API ids draw from.
const idAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"

// LambdaInvoker is the cross-service seam API Gateway uses to invoke a Lambda
// function synchronously for an AWS_PROXY/AWS integration. It is deliberately
// the recursion-guarded InvokeSync shape (same as the Step Functions
// Task->Lambda seam), returning the function output and a functionError so a
// handler that raised maps to a 502, rather than the fire-and-forget
// InvokeExternal used by S3/SNS/EventBridge. The Lambda mock satisfies it.
type LambdaInvoker interface {
	InvokeSync(ctx context.Context, functionARN string, payload []byte) (output []byte, functionError string, err error)
}

// apiData is one REST API plus its full tree, guarded by its own lock. Every
// resource/method/integration/deployment/stage lives here so a single lock
// makes each control-plane op atomic and never exposes a half-written tree.
type apiData struct {
	mu          sync.RWMutex
	api         driver.RestAPI
	resources   map[string]*driver.Resource
	deployments map[string]*driver.Deployment
	stages      map[string]*driver.Stage
}

// Mock is an in-memory implementation of Amazon API Gateway.
type Mock struct {
	apis *memstore.Store[*apiData]
	opts *config.Options

	// lambda is the AWS_PROXY/AWS invoke seam; nil until SetLambdaInvoker wires
	// the Lambda backend. InvokeRoute is nil-safe when it is unset (returns a
	// 502, matching a Lambda integration whose backend is unreachable).
	lambda LambdaInvoker
}

// New creates a new API Gateway mock.
func New(opts *config.Options) *Mock {
	return &Mock{apis: memstore.New[*apiData](), opts: opts}
}

// SetLambdaInvoker wires the Lambda backend so an AWS_PROXY integration invokes
// the target function synchronously through the recursion-guarded seam.
func (m *Mock) SetLambdaInvoker(i LambdaInvoker) { m.lambda = i }

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
		return nil, cerrors.Newf(cerrors.NotFound, "Invalid REST API identifier specified %s", id)
	}

	return ad, nil
}

// CreateRestAPI creates a new REST API with an implicit root ("/") resource.
func (m *Mock) CreateRestAPI(_ context.Context, in *driver.CreateRestAPIInput) (*driver.RestAPI, error) {
	if in.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "Name is required")
	}

	apiID := genID()
	rootID := genID()

	root := &driver.Resource{
		ID: rootID, RestAPIID: apiID, ParentID: "", PathPart: "", Path: "/",
		Methods: map[string]*driver.Method{},
	}

	api := driver.RestAPI{
		ID: apiID, Name: in.Name, Description: in.Description, Version: in.Version,
		CreatedDate: m.now(), RootResourceID: rootID,
		APIKeySource:               orDefault(in.APIKeySource, "HEADER"),
		Tags:                       copyStrMap(in.Tags),
		BinaryMediaTypes:           append([]string(nil), in.BinaryMediaTypes...),
		EndpointConfigurationTypes: endpointTypes(in.EndpointConfigurationTypes),
	}

	m.apis.Set(apiID, &apiData{
		api:         api,
		resources:   map[string]*driver.Resource{rootID: root},
		deployments: map[string]*driver.Deployment{},
		stages:      map[string]*driver.Stage{},
	})

	out := copyAPI(&api)

	return &out, nil
}

// GetRestAPIs lists all REST APIs.
func (m *Mock) GetRestAPIs(_ context.Context) ([]driver.RestAPI, error) {
	all := m.apis.All()
	out := make([]driver.RestAPI, 0, len(all))

	for _, ad := range all {
		ad.mu.RLock()
		out = append(out, copyAPI(&ad.api))
		ad.mu.RUnlock()
	}

	return out, nil
}

// GetRestAPI returns a single REST API.
func (m *Mock) GetRestAPI(_ context.Context, id string) (*driver.RestAPI, error) {
	ad, err := m.getAPI(id)
	if err != nil {
		return nil, err
	}

	ad.mu.RLock()
	defer ad.mu.RUnlock()

	out := copyAPI(&ad.api)

	return &out, nil
}

// DeleteRestAPI removes a REST API and its whole tree.
func (m *Mock) DeleteRestAPI(_ context.Context, id string) error {
	if !m.apis.Delete(id) {
		return cerrors.Newf(cerrors.NotFound, "Invalid REST API identifier specified %s", id)
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

// endpointTypes defaults the endpoint configuration to EDGE when unset, as the
// real CreateRestApi does.
func endpointTypes(in []string) []string {
	if len(in) == 0 {
		return []string{"EDGE"}
	}

	return append([]string(nil), in...)
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

// copyAPI returns a deep copy of a RestAPI so callers never share its maps/slices.
func copyAPI(a *driver.RestAPI) driver.RestAPI {
	out := *a
	out.Tags = copyStrMap(a.Tags)
	out.BinaryMediaTypes = append([]string(nil), a.BinaryMediaTypes...)
	out.EndpointConfigurationTypes = append([]string(nil), a.EndpointConfigurationTypes...)

	return out
}
