package kms

import (
	"context"
	"net/http"

	kmsdriver "github.com/stackshy/cloudemu/v2/services/kms/driver"
)

// xksCredentialJSON is the XksProxyAuthenticationCredentialType wire shape.
type xksCredentialJSON struct {
	AccessKeyID        string `json:"AccessKeyId"`
	RawSecretAccessKey string `json:"RawSecretAccessKey"`
}

func (c *xksCredentialJSON) toDriver() *kmsdriver.XksProxyAuthenticationCredential {
	if c == nil {
		return nil
	}

	return &kmsdriver.XksProxyAuthenticationCredential{
		AccessKeyID: c.AccessKeyID, RawSecretAccessKey: c.RawSecretAccessKey,
	}
}

// xksProxyConfigJSON is the XksProxyConfigurationType wire shape (no secret).
type xksProxyConfigJSON struct {
	Connectivity            string `json:"Connectivity,omitempty"`
	AccessKeyID             string `json:"AccessKeyId,omitempty"`
	URIEndpoint             string `json:"UriEndpoint,omitempty"`
	URIPath                 string `json:"UriPath,omitempty"`
	VpcEndpointServiceName  string `json:"VpcEndpointServiceName,omitempty"`
	VpcEndpointServiceOwner string `json:"VpcEndpointServiceOwner,omitempty"`
}

// customKeyStoreJSON is the CustomKeyStoresListEntry wire shape. The kmsuser
// password and XKS secret are never returned.
type customKeyStoreJSON struct {
	CustomKeyStoreID       string              `json:"CustomKeyStoreId"`
	CustomKeyStoreName     string              `json:"CustomKeyStoreName"`
	CustomKeyStoreType     string              `json:"CustomKeyStoreType"`
	CloudHsmClusterID      string              `json:"CloudHsmClusterId,omitempty"`
	TrustAnchorCertificate string              `json:"TrustAnchorCertificate,omitempty"`
	ConnectionState        string              `json:"ConnectionState"`
	ConnectionErrorCode    string              `json:"ConnectionErrorCode,omitempty"`
	CreationDate           *float64            `json:"CreationDate,omitempty"`
	XksProxyConfiguration  *xksProxyConfigJSON `json:"XksProxyConfiguration,omitempty"`
}

func customKeyStoreToJSON(s *kmsdriver.CustomKeyStore) customKeyStoreJSON {
	out := customKeyStoreJSON{
		CustomKeyStoreID:       s.CustomKeyStoreID,
		CustomKeyStoreName:     s.CustomKeyStoreName,
		CustomKeyStoreType:     s.CustomKeyStoreType,
		CloudHsmClusterID:      s.CloudHsmClusterID,
		TrustAnchorCertificate: s.TrustAnchorCertificate,
		ConnectionState:        s.ConnectionState,
		ConnectionErrorCode:    s.ConnectionErrorCode,
		CreationDate:           epochOrNil(s.CreationDate),
	}

	if c := s.XksProxyConfiguration; c != nil {
		out.XksProxyConfiguration = &xksProxyConfigJSON{
			Connectivity:            c.Connectivity,
			AccessKeyID:             c.AccessKeyID,
			URIEndpoint:             c.URIEndpoint,
			URIPath:                 c.URIPath,
			VpcEndpointServiceName:  c.VpcEndpointServiceName,
			VpcEndpointServiceOwner: c.VpcEndpointServiceOwner,
		}
	}

	return out
}

type createCustomKeyStoreRequest struct {
	CustomKeyStoreName               string             `json:"CustomKeyStoreName"`
	CloudHsmClusterID                string             `json:"CloudHsmClusterId"`
	TrustAnchorCertificate           string             `json:"TrustAnchorCertificate"`
	KeyStorePassword                 string             `json:"KeyStorePassword"`
	CustomKeyStoreType               string             `json:"CustomKeyStoreType"`
	XksProxyURIEndpoint              string             `json:"XksProxyUriEndpoint"`
	XksProxyURIPath                  string             `json:"XksProxyUriPath"`
	XksProxyVpcEndpointServiceName   string             `json:"XksProxyVpcEndpointServiceName"`
	XksProxyVpcEndpointServiceOwner  string             `json:"XksProxyVpcEndpointServiceOwner"`
	XksProxyAuthenticationCredential *xksCredentialJSON `json:"XksProxyAuthenticationCredential"`
	XksProxyConnectivity             string             `json:"XksProxyConnectivity"`
}

type createCustomKeyStoreResponse struct {
	CustomKeyStoreID string `json:"CustomKeyStoreId"`
}

type describeCustomKeyStoresRequest struct {
	CustomKeyStoreID   string `json:"CustomKeyStoreId"`
	CustomKeyStoreName string `json:"CustomKeyStoreName"`
	Limit              int32  `json:"Limit"`
	Marker             string `json:"Marker"`
}

type describeCustomKeyStoresResponse struct {
	CustomKeyStores []customKeyStoreJSON `json:"CustomKeyStores"`
	NextMarker      string               `json:"NextMarker,omitempty"`
	Truncated       bool                 `json:"Truncated"`
}

type updateCustomKeyStoreRequest struct {
	CustomKeyStoreID                 string             `json:"CustomKeyStoreId"`
	NewCustomKeyStoreName            string             `json:"NewCustomKeyStoreName"`
	KeyStorePassword                 string             `json:"KeyStorePassword"`
	CloudHsmClusterID                string             `json:"CloudHsmClusterId"`
	XksProxyURIEndpoint              string             `json:"XksProxyUriEndpoint"`
	XksProxyURIPath                  string             `json:"XksProxyUriPath"`
	XksProxyVpcEndpointServiceName   string             `json:"XksProxyVpcEndpointServiceName"`
	XksProxyVpcEndpointServiceOwner  string             `json:"XksProxyVpcEndpointServiceOwner"`
	XksProxyAuthenticationCredential *xksCredentialJSON `json:"XksProxyAuthenticationCredential"`
	XksProxyConnectivity             string             `json:"XksProxyConnectivity"`
}

type customKeyStoreIDRequest struct {
	CustomKeyStoreID string `json:"CustomKeyStoreId"`
}

func (h *Handler) createCustomKeyStore(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *createCustomKeyStoreRequest) (any, error) {
		id, err := h.kms.CreateCustomKeyStore(ctx, kmsdriver.CreateCustomKeyStoreInput{
			CustomKeyStoreName:               req.CustomKeyStoreName,
			CustomKeyStoreType:               req.CustomKeyStoreType,
			CloudHsmClusterID:                req.CloudHsmClusterID,
			TrustAnchorCertificate:           req.TrustAnchorCertificate,
			KeyStorePassword:                 req.KeyStorePassword,
			XksProxyURIEndpoint:              req.XksProxyURIEndpoint,
			XksProxyURIPath:                  req.XksProxyURIPath,
			XksProxyVpcEndpointServiceName:   req.XksProxyVpcEndpointServiceName,
			XksProxyVpcEndpointServiceOwner:  req.XksProxyVpcEndpointServiceOwner,
			XksProxyAuthenticationCredential: req.XksProxyAuthenticationCredential.toDriver(),
			XksProxyConnectivity:             req.XksProxyConnectivity,
		})
		if err != nil {
			return nil, err
		}

		return createCustomKeyStoreResponse{CustomKeyStoreID: id}, nil
	})
}

func (h *Handler) describeCustomKeyStores(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *describeCustomKeyStoresRequest) (any, error) {
		stores, err := h.kms.DescribeCustomKeyStores(ctx, req.CustomKeyStoreID, req.CustomKeyStoreName)
		if err != nil {
			return nil, err
		}

		start, end, next, truncated, err := pageWindow(req.Marker, req.Limit, len(stores))
		if err != nil {
			return nil, err
		}

		entries := make([]customKeyStoreJSON, 0, end-start)
		for i := start; i < end; i++ {
			entries = append(entries, customKeyStoreToJSON(&stores[i]))
		}

		return describeCustomKeyStoresResponse{CustomKeyStores: entries, NextMarker: next, Truncated: truncated}, nil
	})
}

func (h *Handler) updateCustomKeyStore(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *updateCustomKeyStoreRequest) (any, error) {
		err := h.kms.UpdateCustomKeyStore(ctx, kmsdriver.UpdateCustomKeyStoreInput{
			CustomKeyStoreID:                 req.CustomKeyStoreID,
			NewCustomKeyStoreName:            req.NewCustomKeyStoreName,
			KeyStorePassword:                 req.KeyStorePassword,
			CloudHsmClusterID:                req.CloudHsmClusterID,
			XksProxyURIEndpoint:              req.XksProxyURIEndpoint,
			XksProxyURIPath:                  req.XksProxyURIPath,
			XksProxyVpcEndpointServiceName:   req.XksProxyVpcEndpointServiceName,
			XksProxyVpcEndpointServiceOwner:  req.XksProxyVpcEndpointServiceOwner,
			XksProxyAuthenticationCredential: req.XksProxyAuthenticationCredential.toDriver(),
			XksProxyConnectivity:             req.XksProxyConnectivity,
		})
		if err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

func (h *Handler) deleteCustomKeyStore(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *customKeyStoreIDRequest) (any, error) {
		return struct{}{}, h.kms.DeleteCustomKeyStore(ctx, req.CustomKeyStoreID)
	})
}

func (h *Handler) connectCustomKeyStore(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *customKeyStoreIDRequest) (any, error) {
		return struct{}{}, h.kms.ConnectCustomKeyStore(ctx, req.CustomKeyStoreID)
	})
}

func (h *Handler) disconnectCustomKeyStore(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *customKeyStoreIDRequest) (any, error) {
		return struct{}{}, h.kms.DisconnectCustomKeyStore(ctx, req.CustomKeyStoreID)
	})
}
