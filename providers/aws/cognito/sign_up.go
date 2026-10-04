package cognito

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"math/big"
	"slices"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/cognito/driver"
)

// Confirmation codes are six digits and valid for 24 hours, like the codes
// Cognito sends by email or SMS.
const (
	codeDigits   = 6
	codeSpace    = 1_000_000
	codeValidity = 24 * time.Hour
	phoneTailLen = 4
)

// pendingCode is the confirmation code outstanding for a user. Delivery is
// emulated: nothing is sent, and the code is read back through
// ConfirmationCode (the /_cloudemu/cognito/codes endpoint).
type pendingCode struct {
	Code      string                      `json:"code"`
	ExpiresAt time.Time                   `json:"expiresAt"`
	Delivery  *driver.CodeDeliveryDetails `json:"delivery,omitempty"`
}

func (c *pendingCode) clone() *pendingCode {
	if c == nil {
		return nil
	}

	out := *c

	if c.Delivery != nil {
		d := *c.Delivery
		out.Delivery = &d
	}

	return &out
}

func codeMismatch() error {
	//nolint:revive // exact Cognito message, surfaced verbatim to the SDK
	return &driver.APIError{
		Exception: driver.ExCodeMismatch,
		Err:       errors.New(errors.InvalidArgument, "Invalid verification code provided, please try again."),
	}
}

func expiredCode() error {
	//nolint:revive // exact Cognito message, surfaced verbatim to the SDK
	return &driver.APIError{
		Exception: driver.ExExpiredCode,
		Err:       errors.New(errors.InvalidArgument, "Invalid code provided, please request a code again."),
	}
}

// clientUserNotFound is the UserNotFoundException the client-side operations
// return for an unknown username.
func clientUserNotFound() error {
	//nolint:revive // exact Cognito message, surfaced verbatim to the SDK
	return &driver.APIError{
		Exception: driver.ExUserNotFound,
		Err:       errors.New(errors.NotFound, "Username/client id combination not found."),
	}
}

func cannotConfirm(status string) error {
	return notAuthorized("User cannot be confirmed. Current status is " + status)
}

// clientByID finds an app client by id alone, as the client-side operations
// address it. Client ids are unique across pools.
func (m *Mock) clientByID(clientID string) (driver.UserPoolClient, error) {
	if clientID != "" {
		suffix := clientKeySep + clientID

		for _, key := range m.clients.Keys() {
			if !strings.HasSuffix(key, suffix) {
				continue
			}

			if c, ok := m.clients.Get(key); ok {
				return copyUserPoolClient(c), nil
			}
		}
	}

	return driver.UserPoolClient{}, resourceNotFound("User pool client %s does not exist.", clientID)
}

// secretHash is Base64(HMAC-SHA256(clientSecret, username+clientID)), the
// SECRET_HASH a client with a secret must send.
func secretHash(secret, username, clientID string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(username + clientID))

	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// checkSecretHash verifies SECRET_HASH for a client that has a secret. The
// hash may be computed over any of the names the user is known by. missing is
// the message for an absent hash, which differs between operations.
//
//nolint:gocritic // hugeParam: the client is a stored copy passed by value
func checkSecretHash(client driver.UserPoolClient, got, missing string, names ...string) error {
	if client.ClientSecret == "" {
		return nil
	}

	if got == "" {
		return notAuthorized(missing)
	}

	for _, name := range names {
		if name == "" {
			continue
		}

		if hmac.Equal([]byte(got), []byte(secretHash(client.ClientSecret, name, client.ClientID))) {
			return nil
		}
	}

	return badSecretHash(client.ClientID)
}

func badSecretHash(clientID string) error {
	return notAuthorized("Unable to verify secret hash for client " + clientID)
}

// newCode returns a random six-digit code.
func newCode() string {
	n, err := rand.Int(rand.Reader, big.NewInt(codeSpace))
	if err != nil {
		return "000000"
	}

	s := n.String()

	return strings.Repeat("0", codeDigits-len(s)) + s
}

// codeDelivery picks where a confirmation code goes: the phone number when it
// is auto-verified and set, else the email. Nil means the pool verifies
// neither, so no code is delivered.
func codeDelivery(pool *driver.UserPool, attrs []driver.Attribute) *driver.CodeDeliveryDetails {
	if slices.Contains(pool.AutoVerifiedAttributes, attrPhoneNumber) {
		if v := attrValue(attrs, attrPhoneNumber); v != "" {
			return &driver.CodeDeliveryDetails{
				Destination: maskPhone(v), DeliveryMedium: driver.DeliveryMediumSMS, AttributeName: attrPhoneNumber,
			}
		}
	}

	if slices.Contains(pool.AutoVerifiedAttributes, attrEmail) {
		if v := attrValue(attrs, attrEmail); v != "" {
			return &driver.CodeDeliveryDetails{
				Destination: maskEmail(v), DeliveryMedium: driver.DeliveryMediumEmail, AttributeName: attrEmail,
			}
		}
	}

	return nil
}

// maskEmail renders alice@example.com as a***@e***.
func maskEmail(v string) string {
	local, domain, ok := strings.Cut(v, "@")
	if !ok || local == "" || domain == "" {
		return "***"
	}

	return local[:1] + "***@" + domain[:1] + "***"
}

// maskPhone keeps the plus sign and the last four digits.
func maskPhone(v string) string {
	if len(v) <= phoneTailLen+1 {
		return v
	}

	return "+" + strings.Repeat("*", len(v)-phoneTailLen-1) + v[len(v)-phoneTailLen:]
}

func (m *Mock) issueCode(pool *driver.UserPool, attrs []driver.Attribute) *pendingCode {
	return &pendingCode{Code: newCode(), ExpiresAt: m.now().Add(codeValidity), Delivery: codeDelivery(pool, attrs)}
}

// checkRequired rejects a sign-up that leaves out a schema attribute the pool
// marks required.
func checkRequired(pool *driver.UserPool, attrs []driver.Attribute) error {
	for _, a := range pool.SchemaAttributes {
		if a.Required && a.Name != attrSub && attrValue(attrs, a.Name) == "" {
			return schemaError(a.Name, "The attribute is required")
		}
	}

	return nil
}

// SignUp registers an UNCONFIRMED user through an app client and issues a
// confirmation code.
//
//nolint:gocritic // hugeParam: taken by value to match the driver interface
func (m *Mock) SignUp(_ context.Context, in driver.SignUpInput) (*driver.SignUpOutput, error) {
	if err := checkUsername(in.Username); err != nil {
		return nil, err
	}

	if in.Password == "" {
		return nil, invalidParameter("1 validation error detected: Value null at 'password' failed to satisfy constraint: " +
			"Member must not be null")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	client, err := m.clientByID(in.ClientID)
	if err != nil {
		return nil, err
	}

	if err = checkSecretHash(client, in.SecretHash, "Unable to verify secret hash for client "+client.ClientID, in.Username); err != nil {
		return nil, err
	}

	pool, ok := m.userPools.Get(client.UserPoolID)
	if !ok {
		return nil, poolNotFound(client.UserPoolID)
	}

	rec, err := m.newSignUpRecord(&pool, in)
	if err != nil {
		return nil, err
	}

	m.users.Set(userKey(pool.ID, rec.User.Username), copyUserRecord(rec))

	return &driver.SignUpOutput{
		UserConfirmed:       false,
		UserSub:             attrValue(rec.User.Attributes, attrSub),
		CodeDeliveryDetails: rec.Code.clone().deliveryOrNil(),
	}, nil
}

func (c *pendingCode) deliveryOrNil() *driver.CodeDeliveryDetails {
	if c == nil {
		return nil
	}

	return c.Delivery
}

//nolint:gocritic // hugeParam: in is the caller's input, passed through by value
func (m *Mock) newSignUpRecord(pool *driver.UserPool, in driver.SignUpInput) (userRecord, error) {
	if err := validateAttributes(pool, in.UserAttributes, false); err != nil {
		return userRecord{}, err
	}

	if len(pool.UsernameAttributes) == 0 && m.users.Has(userKey(pool.ID, in.Username)) {
		return userRecord{}, usernameExists("User already exists")
	}

	sub := idgen.UUID()
	attrs := mergeAttributes([]driver.Attribute{{Name: attrSub, Value: sub}}, in.UserAttributes)

	username, attrs, err := m.newUsername(pool, in.Username, sub, attrs)
	if err != nil {
		return userRecord{}, err
	}

	if err := checkRequired(pool, attrs); err != nil {
		return userRecord{}, err
	}

	if err := checkPassword(in.Password, pool.Policies.PasswordPolicy); err != nil {
		return userRecord{}, err
	}

	if err := m.claimSignIns(pool, userKey(pool.ID, username), attrs, false, true); err != nil {
		return userRecord{}, err
	}

	now := m.now()
	rec := userRecord{
		PoolID: pool.ID,
		User: driver.User{
			Username:             username,
			Attributes:           attrs,
			UserCreateDate:       now,
			UserLastModifiedDate: now,
			Enabled:              true,
			UserStatus:           driver.UserStatusUnconfirmed,
		},
		Code: m.issueCode(pool, attrs),
	}
	rec.PasswordSalt, rec.PasswordHash = hashPassword(in.Password)

	return rec, nil
}

// clientTarget is the client, pool and (when found) user a client-side
// operation names.
type clientTarget struct {
	client driver.UserPoolClient
	pool   driver.UserPool
	key    string
	rec    userRecord
	found  bool
}

// hidesUsers reports whether the client hides whether a user exists
// (PreventUserExistenceErrors ENABLED).
func (t *clientTarget) hidesUsers() bool {
	return t.client.PreventUserExistenceErrors == existenceEnabled
}

// clientUser resolves the client, verifies SECRET_HASH, and looks up the user a
// client-side operation names. A missing user is reported through found, so
// the caller can answer the way the client's existence-error setting asks. It
// must run under m.mu.
func (m *Mock) clientUser(in driver.ClientUserInput) (clientTarget, error) {
	client, err := m.clientByID(in.ClientID)
	if err != nil {
		return clientTarget{}, err
	}

	pool, ok := m.userPools.Get(client.UserPoolID)
	if !ok {
		return clientTarget{}, poolNotFound(client.UserPoolID)
	}

	key, rec, found := m.resolveUser(&pool, in.Username)

	names := []string{in.Username}
	if found {
		names = append(names, rec.User.Username)
	}

	if err := checkSecretHash(client, in.SecretHash, "Unable to verify secret hash for client "+client.ClientID, names...); err != nil {
		return clientTarget{}, err
	}

	return clientTarget{client: client, pool: pool, key: key, rec: copyUserRecord(rec), found: found}, nil
}

// simulatedDelivery is the CodeDeliveryDetails a client that hides user
// existence returns for an unknown username, shaped like a real delivery to
// the attribute the pool verifies.
func simulatedDelivery(pool *driver.UserPool, username string) *driver.CodeDeliveryDetails {
	email := username
	if !isEmailFormat(email) {
		email = username + "@example.com"
	}

	attrs := []driver.Attribute{{Name: attrEmail, Value: email}}
	if isPhoneFormat(username) {
		attrs = append(attrs, driver.Attribute{Name: attrPhoneNumber, Value: username})
	}

	return codeDelivery(pool, attrs)
}

// ConfirmSignUp confirms an UNCONFIRMED user with the code that was sent. The
// attribute the code went to becomes verified.
func (m *Mock) ConfirmSignUp(_ context.Context, in driver.ConfirmSignUpInput) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	t, err := m.clientUser(in.ClientUserInput)
	if err != nil {
		return err
	}

	if !t.found {
		if t.hidesUsers() {
			return codeMismatch()
		}

		return clientUserNotFound()
	}

	pool, key, rec := t.pool, t.key, t.rec

	if rec.User.UserStatus != driver.UserStatusUnconfirmed {
		return cannotConfirm(rec.User.UserStatus)
	}

	if err := m.checkCode(rec.Code, in.ConfirmationCode); err != nil {
		return err
	}

	if d := rec.Code.Delivery; d != nil {
		if flag, ok := verifiedFlag(d.AttributeName); ok {
			attrs := mergeAttributes(rec.User.Attributes, []driver.Attribute{{Name: flag, Value: attrTrue}})
			if err := m.claimSignIns(&pool, key, attrs, in.ForceAliasCreation, false); err != nil {
				return err
			}

			rec.User.Attributes = attrs
		}
	}

	rec.Code = nil
	rec.User.UserStatus = driver.UserStatusConfirmed
	rec.User.UserLastModifiedDate = m.now()
	m.users.Set(key, rec)

	return nil
}

// checkCode compares a confirmation code with the outstanding one.
func (m *Mock) checkCode(want *pendingCode, got string) error {
	if want == nil || !hmac.Equal([]byte(want.Code), []byte(got)) {
		return codeMismatch()
	}

	if !m.now().Before(want.ExpiresAt) {
		return expiredCode()
	}

	return nil
}

// ResendConfirmationCode issues a fresh code to an UNCONFIRMED user.
func (m *Mock) ResendConfirmationCode(_ context.Context, in driver.ClientUserInput) (*driver.CodeDeliveryDetails, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	t, err := m.clientUser(in)
	if err != nil {
		return nil, err
	}

	if !t.found {
		if t.hidesUsers() {
			return simulatedDelivery(&t.pool, in.Username), nil
		}

		return nil, clientUserNotFound()
	}

	pool, key, rec := t.pool, t.key, t.rec

	if rec.User.UserStatus != driver.UserStatusUnconfirmed {
		return nil, invalidParameter("User is already confirmed.")
	}

	rec.Code = m.issueCode(&pool, rec.User.Attributes)
	m.users.Set(key, rec)

	return rec.Code.clone().deliveryOrNil(), nil
}

// AdminConfirmSignUp confirms an UNCONFIRMED user without a code.
func (m *Mock) AdminConfirmSignUp(_ context.Context, userPoolID, username string) error {
	return m.updateUser(userPoolID, username, func(_ *driver.UserPool, _ string, rec *userRecord) error {
		if rec.User.UserStatus != driver.UserStatusUnconfirmed {
			return cannotConfirm(rec.User.UserStatus)
		}

		rec.Code = nil
		rec.User.UserStatus = driver.UserStatusConfirmed

		return nil
	})
}

// ConfirmationCode returns the code outstanding for a user.
func (m *Mock) ConfirmationCode(_ context.Context, userPoolID, username string) (*driver.IssuedCode, error) {
	pool, ok := m.userPools.Get(userPoolID)
	if !ok {
		return nil, poolNotFound(userPoolID)
	}

	_, rec, ok := m.resolveUser(&pool, username)
	if !ok {
		return nil, userNotFound()
	}

	c := rec.Code.clone()
	if c == nil {
		return nil, resourceNotFound("No confirmation code is outstanding for user %s.", username)
	}

	return &driver.IssuedCode{Code: c.Code, ExpiresAt: c.ExpiresAt, Delivery: c.Delivery}, nil
}
