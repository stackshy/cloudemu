// Package driver defines the interface and types for the AWS AppSync
// control-plane API. It models GraphQL APIs, their data sources, and their API
// keys, plus resource tagging.
//
// The emulator models the fields clients and IaC tools read back (the computed
// apiId, arn, uris, dataSourceArn, and the API-key expiry), and carries the
// rich configuration blocks it does not interpret (logConfig, userPoolConfig,
// openIDConnectConfig, lambdaAuthorizerConfig, additionalAuthenticationProviders,
// and the per-data-source dynamodbConfig/lambdaConfig/httpConfig/... blocks)
// verbatim as map[string]json.RawMessage so a round-tripped resource reflects
// everything the caller sent. Real GraphQL schema, resolvers, and query
// execution are out of scope for this control-plane surface.
package driver

import (
	"context"
	"encoding/json"
)

// Page is the pagination cursor shared by the list operations.
type Page struct {
	NextToken  string
	MaxResults int32
}

// Authentication types accepted for a GraphQL API.
const (
	AuthAPIKey        = "API_KEY"
	AuthAWSIAM        = "AWS_IAM"
	AuthCognito       = "AMAZON_COGNITO_USER_POOLS"
	AuthOpenIDConnect = "OPENID_CONNECT"
	AuthLambda        = "AWS_LAMBDA"
)

// Defaults applied to a GraphQL API at creation.
const (
	VisibilityGlobal = "GLOBAL"
	APITypeGraphQL   = "GRAPHQL"
)

// URI keys always populated in a GraphQL API's uris map.
const (
	URIKeyGraphQL  = "GRAPHQL"
	URIKeyRealtime = "REALTIME"
)

// Data-source types accepted by CreateDataSource / UpdateDataSource.
const (
	DataSourceLambda        = "AWS_LAMBDA"
	DataSourceDynamoDB      = "AMAZON_DYNAMODB"
	DataSourceElasticsearch = "AMAZON_ELASTICSEARCH"
	DataSourceOpenSearch    = "AMAZON_OPENSEARCH_SERVICE"
	DataSourceHTTP          = "HTTP"
	DataSourceNone          = "NONE"
	DataSourceRelational    = "RELATIONAL_DATABASE"
	DataSourceEventBridge   = "AMAZON_EVENTBRIDGE"
)

// GraphqlAPI is an AppSync GraphQL API. apiId, arn, uris, and owner are
// computed once at create and never regenerated. Extra carries the request
// fields the emulator does not model explicitly, verbatim.
type GraphqlAPI struct {
	APIID              string
	Name               string
	AuthenticationType string
	ARN                string
	URIs               map[string]string
	Owner              string
	Visibility         string
	APIType            string
	XrayEnabled        bool
	Tags               map[string]string
	Extra              map[string]json.RawMessage
}

// DataSource is a data source attached to a GraphQL API. dataSourceArn is
// computed once at create. Extra carries the type-specific config blocks.
type DataSource struct {
	APIID          string
	Name           string
	Type           string
	Description    string
	ServiceRoleArn string
	DataSourceArn  string
	Extra          map[string]json.RawMessage
}

// APIKey is an API key for a GraphQL API. Expires and Deletes are epoch-second
// timestamps computed once at create (floored to the hour) and never
// recomputed on a read.
type APIKey struct {
	ID          string
	Description string
	Expires     int64
	Deletes     int64
}

// CreateGraphqlAPIInput is the input to CreateGraphqlAPI. XrayEnabled is a
// pointer so an omitted value defaults to false rather than forcing it.
type CreateGraphqlAPIInput struct {
	Name               string
	AuthenticationType string
	Visibility         string
	APIType            string
	XrayEnabled        *bool
	Tags               map[string]string
	Extra              map[string]json.RawMessage
}

// UpdateGraphqlAPIInput is the input to UpdateGraphqlAPI. Visibility and
// apiType are immutable after create and are not carried here.
type UpdateGraphqlAPIInput struct {
	APIID              string
	Name               string
	AuthenticationType string
	XrayEnabled        *bool
	Extra              map[string]json.RawMessage
}

// CreateDataSourceInput is the input to CreateDataSource.
type CreateDataSourceInput struct {
	APIID          string
	Name           string
	Type           string
	Description    string
	ServiceRoleArn string
	Extra          map[string]json.RawMessage
}

// UpdateDataSourceInput is the input to UpdateDataSource; Name identifies the
// data source (from the path) and is not itself mutable.
type UpdateDataSourceInput struct {
	APIID          string
	Name           string
	Type           string
	Description    string
	ServiceRoleArn string
	Extra          map[string]json.RawMessage
}

// CreateAPIKeyInput is the input to CreateAPIKey. Expires is 0 when the caller
// omits it, selecting the default validity window.
type CreateAPIKeyInput struct {
	APIID       string
	Description string
	Expires     int64
}

// UpdateAPIKeyInput is the input to UpdateAPIKey. Description is a pointer so an
// omitted value keeps the existing one; Expires is 0 to keep the existing one.
type UpdateAPIKeyInput struct {
	APIID       string
	ID          string
	Description *string
	Expires     int64
}

// AppSync is the AWS AppSync control-plane surface: GraphQL APIs, data sources,
// API keys, and resource tags.
type AppSync interface {
	CreateGraphqlAPI(ctx context.Context, in *CreateGraphqlAPIInput) (*GraphqlAPI, error)
	GetGraphqlAPI(ctx context.Context, apiID string) (*GraphqlAPI, error)
	UpdateGraphqlAPI(ctx context.Context, in *UpdateGraphqlAPIInput) (*GraphqlAPI, error)
	DeleteGraphqlAPI(ctx context.Context, apiID string) error
	ListGraphqlAPIs(ctx context.Context, page Page) (apis []GraphqlAPI, nextToken string, err error)

	CreateDataSource(ctx context.Context, in *CreateDataSourceInput) (*DataSource, error)
	GetDataSource(ctx context.Context, apiID, name string) (*DataSource, error)
	UpdateDataSource(ctx context.Context, in *UpdateDataSourceInput) (*DataSource, error)
	DeleteDataSource(ctx context.Context, apiID, name string) error
	ListDataSources(ctx context.Context, apiID string, page Page) (sources []DataSource, nextToken string, err error)

	CreateAPIKey(ctx context.Context, in *CreateAPIKeyInput) (*APIKey, error)
	ListAPIKeys(ctx context.Context, apiID string, page Page) (keys []APIKey, nextToken string, err error)
	UpdateAPIKey(ctx context.Context, in *UpdateAPIKeyInput) (*APIKey, error)
	DeleteAPIKey(ctx context.Context, apiID, id string) error

	TagResource(ctx context.Context, resourceArn string, tags map[string]string) error
	UntagResource(ctx context.Context, resourceArn string, tagKeys []string) error
	ListTagsForResource(ctx context.Context, resourceArn string) (map[string]string, error)
}
