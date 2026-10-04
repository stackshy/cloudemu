package cognito

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	stderrors "errors"
	"math"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/jwtsign"
	"github.com/stackshy/cloudemu/v2/services/cognito/driver"
)

// Token defaults: access and ID tokens last an hour and refresh tokens 30
// days unless the app client says otherwise.
const (
	defaultTokenValidity = time.Hour
	tokenTypeBearer      = "Bearer"
	scopeSignInAdmin     = "aws.cognito.signin.user.admin"
	tokenUseID           = "id"
	tokenUseAccess       = "access"
	refreshSegments      = 5
	refreshSecretLen     = 32
	refreshKeyLen        = 256
	refreshIVLen         = 12
	refreshTagLen        = 16
	jwtSegments          = 3
	maxDurationSeconds   = math.MaxInt64 / int64(time.Second)
)

// refreshHeader is the JWE protected header Cognito refresh tokens carry. The
// emulator's refresh tokens are opaque, like the real ones, but keep the same
// five-segment shape so clients that sniff the format accept them.
const refreshHeader = "eyJjdHkiOiJKV1QiLCJlbmMiOiJBMjU2R0NNIiwiYWxnIjoiUlNBLU9BRVAifQ"

// loginRecord is one sign-in. Every token minted from it, including those a
// refresh mints later, carries its OriginJTI, so revoking the record revokes
// them all. RefreshHash is the SHA-256 of the refresh token; the token itself
// is never stored.
type loginRecord struct {
	OriginJTI      string    `json:"originJti"`
	PoolID         string    `json:"poolId"`
	ClientID       string    `json:"clientId"`
	Username       string    `json:"username"`
	Sub            string    `json:"sub"`
	AuthTime       time.Time `json:"authTime"`
	RefreshExpires time.Time `json:"refreshExpires"`
	RefreshHash    string    `json:"refreshHash"`
	Revoked        bool      `json:"revoked,omitempty"`
}

//nolint:gocritic // hugeParam: value signature required by the func(V) V copy callback
func copyLoginRecord(in loginRecord) loginRecord { return in }

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))

	return hex.EncodeToString(sum[:])
}

func randomB64(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)

	return base64.RawURLEncoding.EncodeToString(b)
}

// newRefreshToken returns an opaque refresh token that names its login.
func newRefreshToken(originJTI string) string {
	secret := randomB64(refreshSecretLen)
	body := base64.RawURLEncoding.EncodeToString([]byte(originJTI + "." + secret))

	return strings.Join([]string{refreshHeader, randomB64(refreshKeyLen), randomB64(refreshIVLen), body, randomB64(refreshTagLen)}, ".")
}

// lookupRefresh returns the login a refresh token belongs to.
func (m *Mock) lookupRefresh(token string) (loginRecord, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != refreshSegments {
		return loginRecord{}, false
	}

	raw, err := base64.RawURLEncoding.DecodeString(parts[3])
	if err != nil {
		return loginRecord{}, false
	}

	originJTI, _, ok := strings.Cut(string(raw), ".")
	if !ok {
		return loginRecord{}, false
	}

	l, ok := m.logins.Get(originJTI)
	if !ok || !hmac.Equal([]byte(l.RefreshHash), []byte(tokenHash(token))) {
		return loginRecord{}, false
	}

	return l, true
}

// deleteLogins removes the logins match selects.
func (m *Mock) deleteLogins(match func(*loginRecord) bool) {
	for _, key := range m.logins.Keys() {
		if l, ok := m.logins.Get(key); ok && match(&l) {
			m.logins.Delete(key)
		}
	}
}

// revokeLogins marks every login of a user revoked.
func (m *Mock) revokeLogins(poolID, sub string) {
	for _, key := range m.logins.Keys() {
		l, ok := m.logins.Get(key)
		if !ok || l.PoolID != poolID || l.Sub != sub || l.Revoked {
			continue
		}

		l.Revoked = true
		m.logins.Set(key, l)
	}
}

// validity converts a client token-validity value and unit to a duration.
func validity(value *int32, unit string, def time.Duration) time.Duration {
	if value == nil {
		return def
	}

	return unitDuration(*value, unit, driver.TimeUnitHours)
}

// unitDuration converts a validity value in a unit to a duration. The product
// is computed in seconds, which cannot overflow for an int32 value, and a
// result past the largest time.Duration saturates rather than wrapping, so a
// huge value is rejected by the range check instead of becoming a short one.
func unitDuration(value int32, unit, defUnit string) time.Duration {
	if unit == "" {
		unit = defUnit
	}

	per := int64(time.Hour / time.Second)

	switch unit {
	case driver.TimeUnitSeconds:
		per = 1
	case driver.TimeUnitMinutes:
		per = int64(time.Minute / time.Second)
	case driver.TimeUnitDays:
		per = int64(24 * time.Hour / time.Second)
	}

	secs := int64(value) * per

	switch {
	case secs > maxDurationSeconds:
		return time.Duration(math.MaxInt64)
	case secs < -maxDurationSeconds:
		return time.Duration(math.MinInt64)
	default:
		return time.Duration(secs) * time.Second
	}
}

// tokenLifetimes returns the access, ID and refresh token lifetimes of a client.
//
//nolint:gocritic // hugeParam, unnamedResult: stored client copy; the order matches the names
func tokenLifetimes(c driver.UserPoolClient) (time.Duration, time.Duration, time.Duration) {
	units := driver.TokenValidityUnits{}
	if c.TokenValidityUnits != nil {
		units = *c.TokenValidityUnits
	}

	access := validity(c.AccessTokenValidity, units.AccessToken, defaultTokenValidity)
	id := validity(c.IDTokenValidity, units.IDToken, defaultTokenValidity)
	refresh := unitDuration(c.RefreshTokenValidity, units.RefreshToken, driver.TimeUnitDays)

	return access, id, refresh
}

// startLogin records a new sign-in and returns it with its refresh token.
//
//nolint:gocritic // hugeParam: stored client copy passed by value
func (m *Mock) startLogin(client driver.UserPoolClient, rec *userRecord) (loginRecord, string) {
	_, _, refresh := tokenLifetimes(client)
	now := m.now()
	originJTI := idgen.UUID()
	token := newRefreshToken(originJTI)

	l := loginRecord{
		OriginJTI:      originJTI,
		PoolID:         client.UserPoolID,
		ClientID:       client.ClientID,
		Username:       rec.User.Username,
		Sub:            attrValue(rec.User.Attributes, attrSub),
		AuthTime:       now,
		RefreshExpires: now.Add(refresh),
		RefreshHash:    tokenHash(token),
	}
	m.logins.Set(originJTI, l)

	return l, token
}

// mintTokens signs a fresh access and ID token for a login.
//
//nolint:gocritic // hugeParam: stored client copy passed by value
func (m *Mock) mintTokens(client driver.UserPoolClient, rec *userRecord, l *loginRecord) (*driver.AuthenticationResult, error) {
	keys, err := m.keysFor(l.PoolID)
	if err != nil {
		return nil, err
	}

	accessLife, idLife, _ := tokenLifetimes(client)
	now := m.now()
	eventID := idgen.UUID()
	groups := m.userGroupsByPrecedence(l.PoolID, rec.Groups)

	common := func(use string, life time.Duration) map[string]any {
		c := map[string]any{
			attrSub:      l.Sub,
			"iss":        issuer(l.PoolID),
			"origin_jti": l.OriginJTI,
			"event_id":   eventID,
			"token_use":  use,
			"auth_time":  l.AuthTime.Unix(),
			"iat":        now.Unix(),
			"exp":        now.Add(life).Unix(),
			"jti":        idgen.UUID(),
		}

		if len(groups) > 0 {
			c["cognito:groups"] = groupNames(groups)
		}

		return c
	}

	access := common(tokenUseAccess, accessLife)
	access["client_id"] = client.ClientID
	access["scope"] = scopeSignInAdmin
	access["username"] = rec.User.Username

	id := common(tokenUseID, idLife)
	id["aud"] = client.ClientID
	id["cognito:username"] = rec.User.Username
	addRoleClaims(id, groups)
	addAttributeClaims(id, rec.User.Attributes, client.ReadAttributes)

	accessToken, err := jwtsign.Sign(keys.access, access)
	if err != nil {
		return nil, err
	}

	idToken, err := jwtsign.Sign(keys.id, id)
	if err != nil {
		return nil, err
	}

	return &driver.AuthenticationResult{
		AccessToken: accessToken,
		IDToken:     idToken,
		ExpiresIn:   int32(accessLife / time.Second), //nolint:gosec // validity is capped far below int32 seconds
		TokenType:   tokenTypeBearer,
	}, nil
}

// userGroupsByPrecedence orders a user's groups the way cognito:groups lists
// them: lowest precedence first, groups without a precedence last.
func (m *Mock) userGroupsByPrecedence(poolID string, names []string) []driver.Group {
	groups := m.userGroups(poolID, names)

	sort.SliceStable(groups, func(i, j int) bool {
		pi, pj := groups[i].Precedence, groups[j].Precedence

		switch {
		case pi == nil || pj == nil:
			return pi != nil && pj == nil
		default:
			return *pi < *pj
		}
	})

	return groups
}

func groupNames(groups []driver.Group) []string {
	out := make([]string, len(groups))
	for i := range groups {
		out[i] = groups[i].GroupName
	}

	return out
}

// addRoleClaims adds cognito:roles and cognito:preferred_role to an ID token
// when any of the user's groups has an IAM role.
func addRoleClaims(claims map[string]any, groups []driver.Group) {
	var roles []string

	for i := range groups {
		if groups[i].RoleARN != "" {
			roles = append(roles, groups[i].RoleARN)
		}
	}

	if len(roles) == 0 {
		return
	}

	claims["cognito:roles"] = roles
	claims["cognito:preferred_role"] = roles[0]
}

// addAttributeClaims copies user attributes into an ID token. The two
// verification flags are booleans; everything else, custom attributes
// included, stays a string. A client with ReadAttributes only sees those.
func addAttributeClaims(claims map[string]any, attrs []driver.Attribute, readable []string) {
	for _, a := range attrs {
		if a.Name == attrSub || (len(readable) > 0 && !slices.Contains(readable, a.Name)) {
			continue
		}

		if a.Name == attrEmailVerified || a.Name == attrPhoneNumberVerified {
			claims[a.Name] = a.Value == attrTrue

			continue
		}

		claims[a.Name] = a.Value
	}
}

func invalidAccessToken() error { return notAuthorized("Invalid Access Token") }

// verifyAccessToken checks an access token's signature, expiry and use, then
// that its login is still live and its user still exists. It returns the
// user's record.
func (m *Mock) verifyAccessToken(token string) (userRecord, error) {
	poolID, ok := unverifiedPool(token)
	if !ok {
		return userRecord{}, invalidAccessToken()
	}

	keys, ok := m.existingKeys(poolID)
	if !ok {
		return userRecord{}, invalidAccessToken()
	}

	claims, err := jwtsign.Verify(token, []*jwtsign.Key{keys.access}, m.opts.Clock)

	switch {
	case stderrors.Is(err, jwtsign.ErrExpired):
		return userRecord{}, notAuthorized("Access Token has expired")
	case err != nil:
		return userRecord{}, invalidAccessToken()
	}

	if claims["token_use"] != tokenUseAccess || claims["iss"] != issuer(poolID) {
		return userRecord{}, invalidAccessToken()
	}

	return m.liveTokenUser(poolID, claims)
}

// liveTokenUser checks that a verified token's login is not revoked and that
// its user still exists and may still sign in.
func (m *Mock) liveTokenUser(poolID string, claims map[string]any) (userRecord, error) {
	originJTI, _ := claims["origin_jti"].(string)
	sub, _ := claims[attrSub].(string)

	l, ok := m.logins.Get(originJTI)
	if !ok || l.Revoked || l.Sub != sub || l.PoolID != poolID {
		return userRecord{}, notAuthorized("Access Token has been revoked")
	}

	rec, ok := m.users.Get(userKey(poolID, l.Username))
	if !ok || attrValue(rec.User.Attributes, attrSub) != sub {
		return userRecord{}, notAuthorized("Access Token has been revoked")
	}

	if err := checkCanSignIn(&rec); err != nil {
		return userRecord{}, err
	}

	return rec, nil
}

// unverifiedPool reads the pool id from a JWT's iss claim before the signature
// is checked, to pick the key set to check it with.
func unverifiedPool(token string) (string, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != jwtSegments {
		return "", false
	}

	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", false
	}

	var claims struct {
		Iss string `json:"iss"`
	}

	if json.Unmarshal(raw, &claims) != nil || claims.Iss == "" {
		return "", false
	}

	return poolFromIssuer(claims.Iss), true
}
