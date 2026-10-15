package driver

import (
	"context"
	"time"
)

// Custom key store types.
const (
	CustomKeyStoreTypeCloudHSM = "AWS_CLOUDHSM"
	CustomKeyStoreTypeExternal = "EXTERNAL_KEY_STORE"
)

// Custom key store connection states.
const (
	ConnectionStateConnected     = "CONNECTED"
	ConnectionStateConnecting    = "CONNECTING"
	ConnectionStateFailed        = "FAILED"
	ConnectionStateDisconnected  = "DISCONNECTED"
	ConnectionStateDisconnecting = "DISCONNECTING"
)

// External key store proxy connectivity values.
const (
	XksConnectivityPublicEndpoint     = "PUBLIC_ENDPOINT"
	XksConnectivityVpcEndpointService = "VPC_ENDPOINT_SERVICE"
)

// XksProxyAuthenticationCredential is the credential KMS uses to sign requests
// to an external key store proxy. RawSecretAccessKey is write-only: it is never
// returned by DescribeCustomKeyStores.
type XksProxyAuthenticationCredential struct {
	AccessKeyID        string
	RawSecretAccessKey string
}

// XksProxyConfiguration is the external key store proxy configuration of an
// EXTERNAL_KEY_STORE custom key store.
type XksProxyConfiguration struct {
	Connectivity            string
	AccessKeyID             string
	URIEndpoint             string
	URIPath                 string
	VpcEndpointServiceName  string
	VpcEndpointServiceOwner string
}

// CustomKeyStore describes a custom key store, mirroring
// CustomKeyStoresListEntry. KeyStorePassword and the XKS secret are kept for
// the backend's bookkeeping but are never part of a Describe response.
type CustomKeyStore struct {
	CustomKeyStoreID       string
	CustomKeyStoreName     string
	CustomKeyStoreType     string
	CloudHsmClusterID      string
	TrustAnchorCertificate string
	KeyStorePassword       string
	ConnectionState        string
	ConnectionErrorCode    string
	CreationDate           time.Time
	XksProxyConfiguration  *XksProxyConfiguration
	XksRawSecretAccessKey  string
}

// CreateCustomKeyStoreInput describes a CreateCustomKeyStore request.
type CreateCustomKeyStoreInput struct {
	CustomKeyStoreName               string
	CustomKeyStoreType               string // defaults to AWS_CLOUDHSM
	CloudHsmClusterID                string
	TrustAnchorCertificate           string
	KeyStorePassword                 string
	XksProxyURIEndpoint              string
	XksProxyURIPath                  string
	XksProxyVpcEndpointServiceName   string
	XksProxyVpcEndpointServiceOwner  string
	XksProxyAuthenticationCredential *XksProxyAuthenticationCredential
	XksProxyConnectivity             string
}

// UpdateCustomKeyStoreInput describes an UpdateCustomKeyStore request. Empty
// fields (nil credential) are left unchanged.
type UpdateCustomKeyStoreInput struct {
	CustomKeyStoreID                 string
	NewCustomKeyStoreName            string
	KeyStorePassword                 string
	CloudHsmClusterID                string
	XksProxyURIEndpoint              string
	XksProxyURIPath                  string
	XksProxyVpcEndpointServiceName   string
	XksProxyVpcEndpointServiceOwner  string
	XksProxyAuthenticationCredential *XksProxyAuthenticationCredential
	XksProxyConnectivity             string
}

// CustomKeyStores is the custom key store surface of KMS.
type CustomKeyStores interface {
	CreateCustomKeyStore(ctx context.Context, in CreateCustomKeyStoreInput) (string, error)
	// DescribeCustomKeyStores returns every store, or only the one matching id
	// or name (at most one of which may be set).
	DescribeCustomKeyStores(ctx context.Context, id, name string) ([]CustomKeyStore, error)
	UpdateCustomKeyStore(ctx context.Context, in UpdateCustomKeyStoreInput) error
	DeleteCustomKeyStore(ctx context.Context, id string) error
	ConnectCustomKeyStore(ctx context.Context, id string) error
	DisconnectCustomKeyStore(ctx context.Context, id string) error
}
