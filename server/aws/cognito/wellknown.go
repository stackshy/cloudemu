package cognito

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	cognitodriver "github.com/stackshy/cloudemu/v2/services/cognito/driver"
)

// wellKnownPath matches GET /{poolId}/.well-known/{jwks.json|openid-configuration}.
// A pool id such as us-east-1_AbC123 contains an underscore and uppercase
// letters, so it is never a valid S3 bucket name and the route cannot shadow a
// bucket.
var wellKnownPath = regexp.MustCompile(`^/([a-z]{2}(?:-[a-z]+)+-\d_[0-9A-Za-z]+)/\.well-known/(jwks\.json|openid-configuration)$`)

const jwksDoc = "jwks.json"

// WellKnown serves a user pool's public signing keys and OpenID Connect
// discovery document, the endpoints a JWT library fetches to verify Cognito
// tokens. Both are unauthenticated GETs on the real service.
type WellKnown struct {
	keys cognitodriver.KeySetProvider
}

// NewWellKnown returns the .well-known handler backed by keys.
func NewWellKnown(keys cognitodriver.KeySetProvider) *WellKnown {
	return &WellKnown{keys: keys}
}

// Matches claims GET requests for a pool's .well-known documents.
func (*WellKnown) Matches(r *http.Request) bool {
	return r.Method == http.MethodGet && wellKnownPath.MatchString(r.URL.Path)
}

// PublicRequest reports that the .well-known documents need no credentials.
// It answers true only for the exact routes Matches claims.
func (w *WellKnown) PublicRequest(r *http.Request) bool { return w.Matches(r) }

// IAMService returns the IAM service prefix of the user-pool documents. They
// are public, so no request reaches IAM authorization.
func (*WellKnown) IAMService() string { return "cognito-idp" }

// openIDConfiguration is the discovery document Cognito publishes for a pool.
type openIDConfiguration struct {
	Issuer                           string   `json:"issuer"`
	JWKSURI                          string   `json:"jwks_uri"`
	IDTokenSigningAlgValuesSupported []string `json:"id_token_signing_alg_values_supported"`
	ResponseTypesSupported           []string `json:"response_types_supported"`
	ScopesSupported                  []string `json:"scopes_supported"`
	SubjectTypesSupported            []string `json:"subject_types_supported"`
	TokenEndpointAuthMethods         []string `json:"token_endpoint_auth_methods_supported"`
}

// ServeHTTP writes the JWKS or the discovery document.
func (w *WellKnown) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	m := wellKnownPath.FindStringSubmatch(r.URL.Path)
	if m == nil {
		http.NotFound(rw, r)

		return
	}

	poolID, doc := m[1], m[2]

	iss, set, err := w.keys.SigningKeys(r.Context(), poolID)
	if err != nil {
		writeWellKnownError(rw, err)

		return
	}

	var body any = set

	if doc != jwksDoc {
		body = openIDConfiguration{
			Issuer:                           iss,
			JWKSURI:                          requestBase(r) + "/" + poolID + "/.well-known/" + jwksDoc,
			IDTokenSigningAlgValuesSupported: []string{"RS256"},
			ResponseTypesSupported:           []string{"code", "token"},
			ScopesSupported:                  []string{"openid", "email", "phone", "profile"},
			SubjectTypesSupported:            []string{"public"},
			TokenEndpointAuthMethods:         []string{"client_secret_basic", "client_secret_post"},
		}
	}

	rw.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(rw).Encode(body)
}

// requestBase is the scheme and host the client reached the emulator on, so
// jwks_uri points back at this server rather than at AWS.
func requestBase(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}

	return scheme + "://" + r.Host
}

func writeWellKnownError(rw http.ResponseWriter, err error) {
	status := http.StatusInternalServerError

	var apiErr *cognitodriver.APIError
	if errors.As(err, &apiErr) && apiErr.Exception == cognitodriver.ExResourceNotFound {
		status = http.StatusNotFound
	}

	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(status)
	_ = json.NewEncoder(rw).Encode(map[string]string{"message": cerrors.Message(err)})
}
