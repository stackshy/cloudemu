package azure

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/server/authctx"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

// invalidTokenCode is the error code Azure AD returns for a missing, malformed,
// expired or wrong-audience bearer token.
const invalidTokenCode = "InvalidAuthenticationToken"

// bearerChallengeHeader is the WWW-Authenticate challenge sent with a 401 so an
// Azure SDK knows to acquire a token and retry, mirroring real Azure AD.
const bearerChallengeHeader = `Bearer authorization_uri="https://login.microsoftonline.com/common"`

// newAuthGate builds the Azure claims-based authentication pre-dispatch hook.
//
// cloudemu cannot verify a real Azure token's signature (it does not hold Azure
// AD's private key), so the gate validates the token's STRUCTURE and CLAIMS —
// well-formed three-part JWT, an accepted Azure audience, an un-expired "exp"
// and a principal claim — and NOT the signature. On success it attaches the
// resolved principal to the request context; on failure it writes a 401 and
// stops dispatch. It only reads the Authorization header, so the request body is
// left untouched for the downstream handler.
func newAuthGate(clock config.Clock) func(http.ResponseWriter, *http.Request) (*http.Request, bool) {
	if clock == nil {
		clock = config.RealClock{}
	}

	return func(w http.ResponseWriter, r *http.Request) (*http.Request, bool) {
		token, ok := bearerToken(r)
		if !ok {
			writeAuthError(w, "Request is missing a Bearer authentication token")
			return r, false
		}

		claims, err := parseClaims(token)
		if err != nil {
			writeAuthError(w, "Bearer token is malformed: "+err.Error())
			return r, false
		}

		if !acceptedAudience(claims.audiences()) {
			writeAuthError(w, "Bearer token audience is not an accepted Azure audience")
			return r, false
		}

		if claims.expired(clock.Now()) {
			writeAuthError(w, "Bearer token has expired")
			return r, false
		}

		principal, ok := claims.principal()
		if !ok {
			writeAuthError(w, "Bearer token carries no principal claim (oid, appid, azp or sub)")
			return r, false
		}

		return r.WithContext(authctx.WithAzurePrincipal(r.Context(), principal)), true
	}
}

// bearerToken returns the token from an "Authorization: Bearer <jwt>" header.
// The scheme match is case-insensitive; ok is false when the header is absent or
// carries a different scheme.
func bearerToken(r *http.Request) (string, bool) {
	const prefix = "bearer "

	h := r.Header.Get("Authorization")
	if len(h) < len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return "", false
	}

	token := strings.TrimSpace(h[len(prefix):])
	if token == "" {
		return "", false
	}

	return token, true
}

// azureClaims is the subset of Azure AD JWT claims the gate validates. "aud" is
// kept raw because Azure tokens carry it as either a single string (v1) or an
// array of strings (v2/multi-resource).
type azureClaims struct {
	Aud   json.RawMessage `json:"aud"`
	Exp   *float64        `json:"exp"`
	Oid   string          `json:"oid"`
	Appid string          `json:"appid"`
	Azp   string          `json:"azp"`
	Sub   string          `json:"sub"`
	Tid   string          `json:"tid"`
}

// parseClaims decodes the claims payload (the second segment) of a three-part
// JWT without verifying its signature. It errors when the token is not a
// well-formed three-part JWT or the payload is not valid base64url JSON.
func parseClaims(token string) (*azureClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return nil, errNotThreeParts
	}

	payload, err := decodeSegment(parts[1])
	if err != nil {
		return nil, err
	}

	var c azureClaims
	if err := json.Unmarshal(payload, &c); err != nil {
		return nil, err
	}

	return &c, nil
}

// errValue is a lightweight sentinel error type so the gate can report parse
// failures without pulling in a heavier error package.
type errValue string

func (e errValue) Error() string { return string(e) }

// errNotThreeParts marks a token that is not a well-formed three-part JWT.
const errNotThreeParts errValue = "not a three-part JWT"

// decodeSegment base64url-decodes a JWT segment, tolerating the presence or
// absence of "=" padding (JWTs are unpadded, but some issuers pad).
func decodeSegment(seg string) ([]byte, error) {
	if b, err := base64.RawURLEncoding.DecodeString(seg); err == nil {
		return b, nil
	}

	return base64.URLEncoding.DecodeString(seg)
}

// audiences returns the token's audience values, decoding "aud" as either a
// single string or an array of strings.
func (c *azureClaims) audiences() []string {
	if len(c.Aud) == 0 {
		return nil
	}

	var single string
	if json.Unmarshal(c.Aud, &single) == nil {
		return []string{single}
	}

	var many []string
	if json.Unmarshal(c.Aud, &many) == nil {
		return many
	}

	return nil
}

// expired reports whether a clearly-set "exp" is in the past relative to now.
// It is deliberately lenient: an absent or non-positive "exp" is accepted, so a
// token minted under a fixed test clock without an expiry still passes.
func (c *azureClaims) expired(now time.Time) bool {
	if c.Exp == nil || *c.Exp <= 0 {
		return false
	}

	return time.Unix(int64(*c.Exp), 0).Before(now)
}

// principal resolves the caller identity from the claims, preferring the object
// id, then the application id, then the subject. ok is false when none is set.
func (c *azureClaims) principal() (authctx.AzurePrincipal, bool) {
	appID := c.Appid
	if appID == "" {
		appID = c.Azp
	}

	switch {
	case c.Oid != "":
		return authctx.AzurePrincipal{ObjectID: c.Oid, AppID: appID, TenantID: c.Tid}, true
	case appID != "":
		return authctx.AzurePrincipal{AppID: appID, TenantID: c.Tid}, true
	case c.Sub != "":
		return authctx.AzurePrincipal{ObjectID: c.Sub, AppID: appID, TenantID: c.Tid}, true
	default:
		return authctx.AzurePrincipal{}, false
	}
}

// acceptedAudience reports whether any audience is an accepted Azure audience:
// the ARM audience (with or without a trailing slash) or a data-plane resource
// audience under a known Azure host suffix (vault/storage/cognitiveservices/…).
// The set is intentionally permissive; a foreign audience (e.g. an attacker's
// own resource) is still rejected.
func acceptedAudience(auds []string) bool {
	armAudiences := map[string]bool{
		"https://management.azure.com":        true,
		"https://management.core.windows.net": true,
		"https://graph.microsoft.com":         true,
		"https://graph.windows.net":           true,
	}

	azureHostSuffixes := []string{
		".azure.com", ".azure.net", ".windows.net",
		".microsoftonline.com", ".microsoft.com", ".azurewebsites.net",
	}

	for _, aud := range auds {
		normalized := strings.ToLower(strings.TrimRight(strings.TrimSpace(aud), "/"))
		if normalized == "" {
			continue
		}

		if armAudiences[normalized] {
			return true
		}

		u, err := url.Parse(normalized)
		if err != nil || u.Host == "" {
			continue
		}

		for _, suffix := range azureHostSuffixes {
			if strings.HasSuffix(u.Host, suffix) {
				return true
			}
		}
	}

	return false
}

// writeAuthError renders a 401 in the ARM error envelope with the bearer
// challenge header, matching Azure AD's InvalidAuthenticationToken response.
func writeAuthError(w http.ResponseWriter, message string) {
	w.Header().Set("WWW-Authenticate", bearerChallengeHeader)
	azurearm.WriteError(w, http.StatusUnauthorized, invalidTokenCode, message)
}
