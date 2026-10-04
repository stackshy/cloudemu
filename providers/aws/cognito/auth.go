package cognito

import (
	"context"
	"crypto/hmac"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/cognito/driver"
)

// Auth parameter and challenge response names.
const (
	paramUsername     = "USERNAME"
	paramPassword     = "PASSWORD"
	paramSecretHash   = "SECRET_HASH"
	paramRefreshToken = "REFRESH_TOKEN"
	paramNewPassword  = "NEW_PASSWORD"
	userAttrPrefix    = "userAttributes."
	sessionBytes      = 96
	existenceEnabled  = "ENABLED"
	dummySalt         = "00000000000000000000000000000000"
	allowFlowPrefix   = "ALLOW_"
	legacyAdminNoSRP  = "ADMIN_NO_SRP_AUTH"
	allowAdminUserPwd = "ALLOW_ADMIN_USER_PASSWORD_AUTH"
	allowUserPwd      = "ALLOW_USER_PASSWORD_AUTH"
	allowRefresh      = "ALLOW_REFRESH_TOKEN_AUTH"
)

// authFlows is the AuthFlowType enum, in the order the validation error lists it.
//
//nolint:gochecknoglobals // static protocol enum
var authFlows = []string{
	driver.AuthFlowUserSRP, driver.AuthFlowRefreshTokenAuth, driver.AuthFlowRefreshToken, driver.AuthFlowCustom,
	driver.AuthFlowAdminNoSRP, driver.AuthFlowUserPassword, driver.AuthFlowAdminUserPassword, driver.AuthFlowUser,
}

// challengeSession is an outstanding NEW_PASSWORD_REQUIRED challenge.
type challengeSession struct {
	poolID    string
	clientID  string
	username  string
	sub       string
	challenge string
	expires   time.Time
}

func missingParam(name string) error { return invalidParameter("Missing required parameter %s", name) }

func incorrectCredentials() error { return notAuthorized("Incorrect username or password.") }

func invalidSession() error { return notAuthorized("Invalid session for the user.") }

func invalidRefreshToken() error { return notAuthorized("Invalid Refresh Token") }

// unknownUser is the error for a sign-in naming no user. A client with
// PreventUserExistenceErrors ENABLED hides that the user does not exist.
func unknownUser(client *driver.UserPoolClient) error {
	if client.PreventUserExistenceErrors == existenceEnabled {
		return incorrectCredentials()
	}

	return userNotFound()
}

func notSupported() error { return invalidParameter("Initiate Auth method not supported.") }

func noSecretReceived(clientID string) string {
	return "Client " + clientID + " is configured for secret but secret was not received"
}

func apiErr(exception string, code errors.Code, msg string) error {
	return &driver.APIError{Exception: exception, Err: errors.New(code, msg)}
}

// InitiateAuth starts a client-side sign-in.
func (m *Mock) InitiateAuth(_ context.Context, in driver.InitiateAuthInput) (*driver.AuthResult, error) {
	return m.initiate(&in, false)
}

// AdminInitiateAuth starts a server-side sign-in in a named pool.
func (m *Mock) AdminInitiateAuth(_ context.Context, in driver.InitiateAuthInput) (*driver.AuthResult, error) {
	return m.initiate(&in, true)
}

// authClient resolves the app client of a sign-in. The admin operations also
// name the pool, which must exist and own the client.
func (m *Mock) authClient(poolID, clientID string, admin bool) (driver.UserPoolClient, driver.UserPool, error) {
	if admin && !m.userPools.Has(poolID) {
		return driver.UserPoolClient{}, driver.UserPool{}, poolNotFound(poolID)
	}

	client, err := m.clientByID(clientID)
	if err != nil {
		return driver.UserPoolClient{}, driver.UserPool{}, err
	}

	if admin && client.UserPoolID != poolID {
		return driver.UserPoolClient{}, driver.UserPool{}, resourceNotFound("User pool client %s does not exist.", clientID)
	}

	pool, ok := m.userPools.Get(client.UserPoolID)
	if !ok {
		return driver.UserPoolClient{}, driver.UserPool{}, poolNotFound(client.UserPoolID)
	}

	return client, pool, nil
}

func (m *Mock) initiate(in *driver.InitiateAuthInput, admin bool) (*driver.AuthResult, error) {
	if !slices.Contains(authFlows, in.AuthFlow) {
		return nil, invalidParameter("1 validation error detected: Value '%s' at 'authFlow' failed to satisfy constraint: "+
			"Member must satisfy enum value set: [%s]", in.AuthFlow, strings.Join(authFlows, ", "))
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	client, pool, err := m.authClient(in.UserPoolID, in.ClientID, admin)
	if err != nil {
		return nil, err
	}

	if err := checkFlow(&client, in.AuthFlow, admin); err != nil {
		return nil, err
	}

	switch in.AuthFlow {
	case driver.AuthFlowUserPassword, driver.AuthFlowAdminUserPassword, driver.AuthFlowAdminNoSRP:
		return m.passwordAuth(&pool, client, in.AuthParameters)
	case driver.AuthFlowRefreshToken, driver.AuthFlowRefreshTokenAuth:
		return m.refreshAuth(client, in.AuthParameters)
	default:
		return nil, invalidParameter("%s is not supported by cloudemu yet.", in.AuthFlow)
	}
}

// checkFlow applies the client's ExplicitAuthFlows. The legacy names
// USER_PASSWORD_AUTH and ADMIN_NO_SRP_AUTH enable the matching ALLOW_ flows,
// and a client configured only with legacy names can always refresh.
func checkFlow(client *driver.UserPoolClient, flow string, admin bool) error {
	flows := client.ExplicitAuthFlows
	has := func(names ...string) bool {
		return slices.ContainsFunc(names, func(n string) bool { return slices.Contains(flows, n) })
	}

	if err := checkFlowSide(flow, admin); err != nil {
		return err
	}

	switch flow {
	case driver.AuthFlowUserPassword:
		if !has(allowUserPwd, driver.AuthFlowUserPassword) {
			return invalidParameter("USER_PASSWORD_AUTH flow not enabled for this client")
		}
	case driver.AuthFlowAdminUserPassword, driver.AuthFlowAdminNoSRP:
		if !has(allowAdminUserPwd, legacyAdminNoSRP) {
			return invalidParameter("Auth flow not enabled for this client")
		}
	case driver.AuthFlowRefreshToken, driver.AuthFlowRefreshTokenAuth:
		if !refreshAllowed(flows) {
			return invalidParameter("Refresh Token flow not enabled for this client")
		}
	case driver.AuthFlowUserSRP:
		if !has("ALLOW_USER_SRP_AUTH") {
			return invalidParameter("USER_SRP_AUTH is not enabled for the client.")
		}
	}

	return nil
}

// refreshAllowed reports whether a client may refresh: it lists
// ALLOW_REFRESH_TOKEN_AUTH, or uses only legacy flow names.
func refreshAllowed(flows []string) bool {
	return slices.Contains(flows, allowRefresh) ||
		!slices.ContainsFunc(flows, func(f string) bool { return strings.HasPrefix(f, allowFlowPrefix) })
}

// checkFlowSide rejects a password flow sent to the wrong API: the ADMIN_
// flows only work through AdminInitiateAuth and USER_PASSWORD_AUTH only through
// InitiateAuth.
func checkFlowSide(flow string, admin bool) error {
	adminFlow := flow == driver.AuthFlowAdminUserPassword || flow == driver.AuthFlowAdminNoSRP
	if (adminFlow && !admin) || (flow == driver.AuthFlowUserPassword && admin) {
		return notSupported()
	}

	return nil
}

// passwordAuth checks a username and password and either issues tokens or
// returns the NEW_PASSWORD_REQUIRED challenge.
//
//nolint:gocritic // hugeParam: stored client copy passed by value
func (m *Mock) passwordAuth(pool *driver.UserPool, client driver.UserPoolClient, params map[string]string) (*driver.AuthResult, error) {
	username := params[paramUsername]
	if username == "" {
		return nil, missingParam(paramUsername)
	}

	password := params[paramPassword]
	if password == "" {
		return nil, missingParam(paramPassword)
	}

	_, rec, found := m.resolveUser(pool, username)

	names := []string{username}
	if found {
		names = append(names, rec.User.Username)
	}

	if err := checkSecretHash(client, params[paramSecretHash], noSecretReceived(client.ClientID), names...); err != nil {
		return nil, err
	}

	if !found {
		// Spend the same hashing work as a real check so the response time
		// does not tell an unknown username from a wrong password.
		_ = pbkdf2Hash(dummySalt, password, pbkdf2Iterations)

		return nil, unknownUser(&client)
	}

	if !verifyPassword(rec.PasswordSalt, rec.PasswordHash, password) {
		return nil, incorrectCredentials()
	}

	if err := checkCanSignIn(&rec); err != nil {
		return nil, err
	}

	if rec.User.UserStatus == driver.UserStatusForceChangePassword {
		return m.newPasswordChallenge(pool, &client, &rec), nil
	}

	return m.signIn(client, &rec)
}

// checkCanSignIn rejects a correct password for a user who may not sign in
// yet: disabled, unconfirmed, or due a password reset.
func checkCanSignIn(rec *userRecord) error {
	if !rec.User.Enabled {
		return notAuthorized("User is disabled.")
	}

	switch rec.User.UserStatus {
	case driver.UserStatusUnconfirmed:
		return apiErr(driver.ExUserNotConfirmed, errors.FailedPrecondition, "User is not confirmed.")
	case driver.UserStatusResetRequired:
		return apiErr(driver.ExPasswordResetRequired, errors.FailedPrecondition, "Password reset required for the user")
	}

	return nil
}

// signIn records a login for a user and returns its tokens.
//
//nolint:gocritic // hugeParam: stored client copy passed by value
func (m *Mock) signIn(client driver.UserPoolClient, rec *userRecord) (*driver.AuthResult, error) {
	l, refresh := m.startLogin(client, rec)

	res, err := m.mintTokens(client, rec, &l)
	if err != nil {
		m.logins.Delete(l.OriginJTI)

		return nil, err
	}

	res.RefreshToken = refresh

	return &driver.AuthResult{AuthenticationResult: res, ChallengeParameters: map[string]string{}}, nil
}

// newPasswordChallenge opens a NEW_PASSWORD_REQUIRED session.
func (m *Mock) newPasswordChallenge(pool *driver.UserPool, client *driver.UserPoolClient, rec *userRecord) *driver.AuthResult {
	attrs := map[string]string{}

	for _, a := range rec.User.Attributes {
		if a.Name != attrSub {
			attrs[a.Name] = a.Value
		}
	}

	required := []string{}

	for _, a := range pool.SchemaAttributes {
		if a.Required && a.Name != attrSub && attrValue(rec.User.Attributes, a.Name) == "" {
			required = append(required, userAttrPrefix+a.Name)
		}
	}

	attrJSON, _ := json.Marshal(attrs)
	requiredJSON, _ := json.Marshal(required)

	session := randomB64(sessionBytes)

	m.sessionsMu.Lock()
	m.pruneSessions()
	m.sessions[session] = challengeSession{
		poolID:    pool.ID,
		clientID:  client.ClientID,
		username:  rec.User.Username,
		sub:       attrValue(rec.User.Attributes, attrSub),
		challenge: driver.ChallengeNewPasswordRequired,
		expires:   m.now().Add(time.Duration(client.AuthSessionValidity) * time.Minute),
	}
	m.sessionsMu.Unlock()

	return &driver.AuthResult{
		ChallengeName: driver.ChallengeNewPasswordRequired,
		Session:       session,
		ChallengeParameters: map[string]string{
			"USER_ID_FOR_SRP":    rec.User.Username,
			"requiredAttributes": string(requiredJSON),
			"userAttributes":     string(attrJSON),
		},
	}
}

// pruneSessions drops expired sessions. It must run under sessionsMu.
func (m *Mock) pruneSessions() {
	now := m.now()

	for k, s := range m.sessions {
		if !now.Before(s.expires) {
			delete(m.sessions, k)
		}
	}
}

// refreshAuth mints new access and ID tokens from a refresh token. The login
// keeps its origin_jti and auth_time, and no new refresh token is issued.
//
//nolint:gocritic // hugeParam: stored client copy passed by value
func (m *Mock) refreshAuth(client driver.UserPoolClient, params map[string]string) (*driver.AuthResult, error) {
	token := params[paramRefreshToken]
	if token == "" {
		return nil, missingParam(paramRefreshToken)
	}

	l, ok := m.lookupRefresh(token)
	if !ok || l.ClientID != client.ClientID {
		return nil, invalidRefreshToken()
	}

	if err := checkSecretHash(client, params[paramSecretHash], noSecretReceived(client.ClientID),
		params[paramUsername], l.Username, l.Sub); err != nil {
		return nil, err
	}

	rec, err := m.refreshUser(&l)
	if err != nil {
		return nil, err
	}

	res, err := m.mintTokens(client, &rec, &l)
	if err != nil {
		return nil, err
	}

	return &driver.AuthResult{AuthenticationResult: res, ChallengeParameters: map[string]string{}}, nil
}

// refreshUser checks a login can still refresh and returns its user.
func (m *Mock) refreshUser(l *loginRecord) (userRecord, error) {
	if l.Revoked {
		return userRecord{}, notAuthorized("Refresh Token has been revoked")
	}

	if !m.now().Before(l.RefreshExpires) {
		return userRecord{}, notAuthorized("Refresh Token has expired")
	}

	rec, ok := m.users.Get(userKey(l.PoolID, l.Username))
	if !ok || attrValue(rec.User.Attributes, attrSub) != l.Sub {
		return userRecord{}, invalidRefreshToken()
	}

	if err := checkCanSignIn(&rec); err != nil {
		return userRecord{}, err
	}

	return rec, nil
}

// RespondToAuthChallenge answers a challenge from InitiateAuth.
func (m *Mock) RespondToAuthChallenge(_ context.Context, in driver.RespondToAuthChallengeInput) (*driver.AuthResult, error) {
	return m.respond(&in, false)
}

// AdminRespondToAuthChallenge answers a challenge from AdminInitiateAuth.
func (m *Mock) AdminRespondToAuthChallenge(_ context.Context, in driver.RespondToAuthChallengeInput) (*driver.AuthResult, error) {
	return m.respond(&in, true)
}

func (m *Mock) respond(in *driver.RespondToAuthChallengeInput, admin bool) (*driver.AuthResult, error) {
	if in.ChallengeName != driver.ChallengeNewPasswordRequired {
		return nil, invalidParameter("%s is not supported by cloudemu yet.", in.ChallengeName)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	client, pool, err := m.authClient(in.UserPoolID, in.ClientID, admin)
	if err != nil {
		return nil, err
	}

	username := in.ChallengeResponses[paramUsername]
	if username == "" {
		return nil, missingParam(paramUsername)
	}

	sess, err := m.takeSession(in.Session, client.ClientID, in.ChallengeName)
	if err != nil {
		return nil, err
	}

	key, rec, err := m.challengeUser(&pool, &client, &sess, username, in.ChallengeResponses[paramSecretHash])
	if err != nil {
		return nil, err
	}

	if err := m.applyNewPassword(&pool, key, &rec, in.ChallengeResponses); err != nil {
		return nil, err
	}

	m.endSession(in.Session)
	m.users.Set(key, rec)

	return m.signIn(client, &rec)
}

// challengeUser finds the user a challenge answer names and checks it is still
// the user the session was issued to and still in the challenge's state. An
// admin can disable, reset or confirm the user between the two calls.
func (m *Mock) challengeUser(
	pool *driver.UserPool, client *driver.UserPoolClient, sess *challengeSession, username, hash string,
) (string, userRecord, error) {
	key, rec, found := m.resolveUser(pool, username)
	if !found || rec.User.Username != sess.username || attrValue(rec.User.Attributes, attrSub) != sess.sub {
		return "", userRecord{}, invalidSession()
	}

	if err := checkSecretHash(*client, hash, noSecretReceived(client.ClientID), username, rec.User.Username); err != nil {
		return "", userRecord{}, err
	}

	if err := checkCanSignIn(&rec); err != nil {
		return "", userRecord{}, err
	}

	if rec.User.UserStatus != driver.UserStatusForceChangePassword {
		return "", userRecord{}, invalidSession()
	}

	return key, copyUserRecord(rec), nil
}

// takeSession returns a live session for the client and challenge. An expired
// session is dropped.
func (m *Mock) takeSession(id, clientID, challenge string) (challengeSession, error) {
	m.sessionsMu.Lock()
	defer m.sessionsMu.Unlock()

	sess, ok := m.sessions[id]
	if !ok || sess.clientID != clientID || sess.challenge != challenge {
		return challengeSession{}, invalidSession()
	}

	if !m.now().Before(sess.expires) {
		delete(m.sessions, id)

		return challengeSession{}, notAuthorized("Invalid session for the user, session is expired.")
	}

	return sess, nil
}

func (m *Mock) endSession(id string) {
	m.sessionsMu.Lock()
	delete(m.sessions, id)
	m.sessionsMu.Unlock()
}

// applyNewPassword sets the new password and any userAttributes.* responses,
// and confirms the user.
func (m *Mock) applyNewPassword(pool *driver.UserPool, key string, rec *userRecord, responses map[string]string) error {
	newPassword := responses[paramNewPassword]
	if newPassword == "" {
		return missingParam(paramNewPassword)
	}

	var updates []driver.Attribute

	for k, v := range responses {
		if name, ok := strings.CutPrefix(k, userAttrPrefix); ok {
			updates = append(updates, driver.Attribute{Name: name, Value: v})
		}
	}

	slices.SortFunc(updates, func(a, b driver.Attribute) int { return strings.Compare(a.Name, b.Name) })

	if err := validateAttributes(pool, updates, false); err != nil {
		return err
	}

	attrs := mergeAttributes(rec.User.Attributes, updates)
	if err := checkRequired(pool, attrs); err != nil {
		return err
	}

	if err := checkPassword(newPassword, pool.Policies.PasswordPolicy); err != nil {
		return err
	}

	if err := m.claimSignIns(pool, key, attrs, false, false); err != nil {
		return err
	}

	rec.User.Attributes = attrs
	rec.PasswordSalt, rec.PasswordHash = hashPassword(newPassword)
	rec.User.UserStatus = driver.UserStatusConfirmed
	rec.User.UserLastModifiedDate = m.now()

	return nil
}

// GetUser returns the user an access token belongs to.
func (m *Mock) GetUser(_ context.Context, accessToken string) (*driver.User, error) {
	rec, err := m.verifyAccessToken(accessToken)
	if err != nil {
		return nil, err
	}

	out := copyUserRecord(rec).User

	return &out, nil
}

// GlobalSignOut revokes every login of the access token's user.
func (m *Mock) GlobalSignOut(_ context.Context, accessToken string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	rec, err := m.verifyAccessToken(accessToken)
	if err != nil {
		return err
	}

	m.revokeLogins(rec.PoolID, attrValue(rec.User.Attributes, attrSub))

	return nil
}

// AdminUserGlobalSignOut revokes every login of a user.
func (m *Mock) AdminUserGlobalSignOut(_ context.Context, userPoolID, username string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	pool, ok := m.userPools.Get(userPoolID)
	if !ok {
		return poolNotFound(userPoolID)
	}

	_, rec, ok := m.resolveUser(&pool, username)
	if !ok {
		return userNotFound()
	}

	m.revokeLogins(pool.ID, attrValue(rec.User.Attributes, attrSub))

	return nil
}

// RevokeToken revokes a refresh token and every token minted from the same
// login. An unknown refresh token is accepted silently, as RFC 7009 allows.
func (m *Mock) RevokeToken(_ context.Context, in driver.RevokeTokenInput) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	client, err := m.clientByID(in.ClientID)
	if err != nil {
		return apiErr(driver.ExUnauthorized, errors.PermissionDenied, "Invalid client id "+in.ClientID)
	}

	if client.ClientSecret != "" && !hmac.Equal([]byte(in.ClientSecret), []byte(client.ClientSecret)) {
		return apiErr(driver.ExUnauthorized, errors.PermissionDenied, "Invalid client secret for client "+client.ClientID)
	}

	if !client.EnableTokenRevocation {
		return apiErr(driver.ExUnsupportedOperation, errors.FailedPrecondition, "Token revocation is not enabled for this app client.")
	}

	if len(strings.Split(in.Token, ".")) != refreshSegments {
		return apiErr(driver.ExUnsupportedTokenType, errors.InvalidArgument, "Unsupported token type. Only refresh tokens can be revoked.")
	}

	l, ok := m.lookupRefresh(in.Token)
	if !ok {
		return nil
	}

	if l.ClientID != client.ClientID {
		return apiErr(driver.ExUnauthorized, errors.PermissionDenied, "The refresh token was not issued to client "+client.ClientID)
	}

	l.Revoked = true
	m.logins.Set(l.OriginJTI, l)

	return nil
}
