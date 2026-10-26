package driver

import "time"

// HTTPParameter is one extra header, query string or body parameter an
// EventBridge connection sends with each request. A secret value is stored in
// the connection's secret and never returned by DescribeConnection.
type HTTPParameter struct {
	Key           string
	Value         string
	IsValueSecret bool
}

// ConnectionHTTPParameters are the extra parameters a connection sends with
// every invocation or OAuth token request.
type ConnectionHTTPParameters struct {
	HeaderParameters      []HTTPParameter
	QueryStringParameters []HTTPParameter
	BodyParameters        []HTTPParameter
}

// ConnectionBasicAuth holds BASIC authorization credentials.
type ConnectionBasicAuth struct {
	Username string
	Password string
}

// ConnectionAPIKeyAuth holds API_KEY authorization credentials: the header name
// and the key sent in it.
type ConnectionAPIKeyAuth struct {
	APIKeyName  string
	APIKeyValue string
}

// ConnectionOAuthClient holds the OAuth client credentials.
type ConnectionOAuthClient struct {
	ClientID     string
	ClientSecret string
}

// ConnectionOAuth holds OAUTH_CLIENT_CREDENTIALS authorization parameters.
type ConnectionOAuth struct {
	AuthorizationEndpoint string
	HTTPMethod            string
	ClientParameters      *ConnectionOAuthClient
	OAuthHTTPParameters   *ConnectionHTTPParameters
}

// ConnectionAuthParameters holds the authorization parameters of a connection.
// Exactly one of Basic, APIKey and OAuth is set, matching the connection's
// authorization type.
type ConnectionAuthParameters struct {
	Basic                    *ConnectionBasicAuth
	APIKey                   *ConnectionAPIKeyAuth
	OAuth                    *ConnectionOAuth
	InvocationHTTPParameters *ConnectionHTTPParameters
}

// Connection describes an EventBridge connection: the authorization an API
// destination uses to call an HTTP endpoint. Connections are an AWS-only
// concept, consumed through an optional interface, not the portable driver.
type Connection struct {
	Name              string
	ARN               string
	Description       string
	State             string
	StateReason       string
	AuthorizationType string
	SecretARN         string
	KMSKeyIdentifier  string
	AuthParameters    ConnectionAuthParameters
	CreatedAt         time.Time
	LastModifiedAt    time.Time
	LastAuthorizedAt  time.Time
}

// ConnectionConfig carries the parameters of an EventBridge CreateConnection call.
type ConnectionConfig struct {
	Name              string
	Description       string
	AuthorizationType string
	KMSKeyIdentifier  string
	AuthParameters    *ConnectionAuthParameters
}

// ConnectionUpdate carries the parameters of an EventBridge UpdateConnection
// call. A nil field was omitted from the request and leaves the connection
// unchanged; a set auth block replaces the stored one.
type ConnectionUpdate struct {
	Name              string
	Description       *string
	AuthorizationType *string
	KMSKeyIdentifier  *string
	AuthParameters    *ConnectionAuthParameters
}

// ConnectionFilter narrows ListConnections.
type ConnectionFilter struct {
	NamePrefix string
	State      string
}

// APIDestination describes an EventBridge API destination: an HTTP endpoint a
// rule target can invoke through a connection.
type APIDestination struct {
	Name                         string
	ARN                          string
	Description                  string
	State                        string
	ConnectionARN                string
	InvocationEndpoint           string
	HTTPMethod                   string
	InvocationRateLimitPerSecond int
	CreatedAt                    time.Time
	LastModifiedAt               time.Time
}

// APIDestinationConfig carries the parameters of an EventBridge
// CreateApiDestination call. A nil InvocationRateLimitPerSecond takes the
// default.
type APIDestinationConfig struct {
	Name                         string
	Description                  string
	ConnectionARN                string
	InvocationEndpoint           string
	HTTPMethod                   string
	InvocationRateLimitPerSecond *int
}

// APIDestinationUpdate carries the parameters of an EventBridge
// UpdateApiDestination call. A nil field leaves the destination unchanged.
type APIDestinationUpdate struct {
	Name                         string
	Description                  *string
	ConnectionARN                *string
	InvocationEndpoint           *string
	HTTPMethod                   *string
	InvocationRateLimitPerSecond *int
}

// APIDestinationFilter narrows ListApiDestinations.
type APIDestinationFilter struct {
	NamePrefix    string
	ConnectionARN string
}
