// Package aad serves the two Azure Active Directory bootstrap endpoints an
// unmodified Terraform azurerm provider (and other Azure SDKs that resolve a
// custom cloud via ARM_METADATA_HOSTNAME / metadata_host) needs before it can
// talk to CloudEmu's ARM wire server:
//
//   - GET  /metadata/endpoints        — the environment-discovery document that
//     tells the client where the Resource Manager and login endpoints live. The
//     document points both back at the emulator (derived from the request Host),
//     so every subsequent ARM and token call lands on the same listener.
//   - POST /{tenant}/oauth2/v2.0/token — the OAuth2 client-credentials token
//     endpoint. It returns a well-formed, fake-signed JWT bearer. CloudEmu's ARM
//     layer accepts any credentials (the signature is never verified), so the
//     only requirement is that the client SUCCESSFULLY OBTAINS a token whose
//     claims it can decode.
//
// Neither endpoint lives under /subscriptions/, so both are registered on the
// Azure server ahead of the permissive blob-storage fallback; their exact-path
// and path-suffix matches are disjoint from every ARM and data-plane handler.
package aad

import (
	"encoding/json"
	"net/http"
	"path"
)

// metadataPath is the ARM environment-discovery path azurerm fetches from the
// host given in ARM_METADATA_HOSTNAME / metadata_host.
const metadataPath = "/metadata/endpoints"

// contentTypeJSON is the response content type for both endpoints.
const contentTypeJSON = "application/json"

// IsBootstrapPath reports whether path is one of the AAD bootstrap endpoints
// (metadata discovery or an OAuth2 token endpoint). The Azure auth gate uses it
// to exempt these paths from bearer-token enforcement: a caller cannot present a
// token to the endpoint that issues tokens, and discovery precedes auth.
func IsBootstrapPath(p string) bool {
	return path.Clean(p) == metadataPath || isTokenPath(p)
}

// MetadataHandler serves GET /metadata/endpoints. It returns an Azure
// environment document whose resourceManager and login endpoints reference the
// emulator itself, so the client routes all later ARM and token traffic to the
// same host it fetched the metadata from.
type MetadataHandler struct{}

// NewMetadata returns a metadata-endpoints handler.
func NewMetadata() *MetadataHandler {
	return &MetadataHandler{}
}

// Matches claims GET /metadata/endpoints. The path is cleaned first because
// go-azure-sdk's metadata client sometimes joins its base and path into a
// doubled leading slash (//metadata/endpoints); path.Clean collapses that to the
// canonical form so the discovery request is served on the first try instead of
// forcing the SDK to retry a 501.
func (*MetadataHandler) Matches(r *http.Request) bool {
	return r.Method == http.MethodGet && path.Clean(r.URL.Path) == metadataPath
}

// ServeHTTP writes the environment-discovery document, self-referential to the
// host the request arrived on.
func (*MetadataHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	base := baseURL(r)
	baseSlash := base + "/"

	doc := metadataDocument{
		Portal:        base,
		Name:          environmentName,
		Media:         baseSlash,
		GraphAudience: baseSlash,
		Graph:         baseSlash,
		Authentication: authenticationBlock{
			LoginEndpoint: base,
			Audiences:     []string{baseSlash},
			// "common" and identity provider "AAD" are what real Azure public-cloud
			// metadata returns; go-azure-sdk's Environment.IsAzureStack() treats any
			// other tenant or identity provider as Azure Stack, which the azurerm
			// provider then refuses to configure. The tenant actually used to mint a
			// token comes from the client's ARM_TENANT_ID, not this field.
			Tenant:           "common",
			IdentityProvider: "AAD",
		},
		Suffixes: suffixesBlock{
			AcrLoginServer:    "azurecr.io",
			SQLServerHostname: "database.windows.net",
			KeyVaultDNS:       "vault.azure.net",
			Storage:           "core.windows.net",
		},
		Batch:                    baseSlash,
		ResourceManager:          baseSlash,
		ActiveDirectoryDataLake:  baseSlash,
		SQLManagement:            baseSlash,
		MicrosoftGraphResourceID: baseSlash,
		Gallery:                  baseSlash,
	}

	writeJSON(w, http.StatusOK, doc)
}

// environmentName is the environment identifier reported by the document. The
// public-cloud name keeps azurerm on its default (public) code path.
const environmentName = "AzureCloud"

// metadataDocument is the /metadata/endpoints response, matching the field names
// go-azure-sdk's metadata client unmarshals. Only name, resourceManager and
// microsoftGraphResourceId are strictly required by the SDK; the rest are filled
// so an SDK that reads them stays fully offline (every URL points at the
// emulator, every suffix at a public DNS suffix that is never dialed for
// ARM-only resources).
type metadataDocument struct {
	Portal                   string              `json:"portal"`
	Authentication           authenticationBlock `json:"authentication"`
	Media                    string              `json:"media"`
	GraphAudience            string              `json:"graphAudience"`
	Graph                    string              `json:"graph"`
	Name                     string              `json:"name"`
	Suffixes                 suffixesBlock       `json:"suffixes"`
	Batch                    string              `json:"batch"`
	ResourceManager          string              `json:"resourceManager"`
	ActiveDirectoryDataLake  string              `json:"activeDirectoryDataLake"`
	SQLManagement            string              `json:"sqlManagement"`
	MicrosoftGraphResourceID string              `json:"microsoftGraphResourceId"`
	Gallery                  string              `json:"gallery"`
}

// authenticationBlock is the document's authentication object.
type authenticationBlock struct {
	LoginEndpoint    string   `json:"loginEndpoint"`
	Audiences        []string `json:"audiences"`
	Tenant           string   `json:"tenant"`
	IdentityProvider string   `json:"identityProvider"`
}

// suffixesBlock is the document's DNS-suffixes object (only the fields commonly
// consulted are populated).
type suffixesBlock struct {
	AcrLoginServer    string `json:"acrLoginServer"`
	SQLServerHostname string `json:"sqlServerHostname"`
	KeyVaultDNS       string `json:"keyVaultDns"`
	Storage           string `json:"storage"`
}

// baseURL reconstructs the emulator's own base URL (scheme + host) from the
// incoming request, so the metadata document self-references whatever address
// the client reached it on.
func baseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}

	return scheme + "://" + r.Host
}

// writeJSON marshals v and writes it as a JSON response with the given status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	w.Header().Set("Content-Type", contentTypeJSON)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
