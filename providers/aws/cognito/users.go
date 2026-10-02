package cognito

import (
	"context"
	"slices"
	"sort"
	"strings"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/cognito/driver"
)

// Attribute names with special handling.
const (
	attrSub                 = "sub"
	attrEmail               = "email"
	attrEmailVerified       = "email_verified"
	attrPhoneNumber         = "phone_number"
	attrPhoneNumberVerified = "phone_number_verified"
	attrPreferredUsername   = "preferred_username"
	attrTrue                = "true"
	attrFalse               = "false"
)

// maxUsernameLen is the UsernameType length ceiling.
const maxUsernameLen = 128

// userRecord is a stored user: the public view plus the password digest. It
// lives in the users store keyed by userKey(poolID, username).
type userRecord struct {
	PoolID       string      `json:"poolId"`
	User         driver.User `json:"user"`
	PasswordSalt string      `json:"passwordSalt,omitempty"`
	PasswordHash string      `json:"passwordHash,omitempty"`
}

func userKey(poolID, username string) string { return poolID + clientKeySep + username }

//nolint:gocritic // hugeParam: value signature required by the func(V) V copy callback
func copyUserRecord(in userRecord) userRecord {
	out := in
	out.User.Attributes = slices.Clone(in.User.Attributes)

	return out
}

// AdminCreateUser creates a user in FORCE_CHANGE_PASSWORD, or with MessageAction
// RESEND re-invites an existing one.
//
//nolint:gocritic // hugeParam: taken by value to match the driver interface
func (m *Mock) AdminCreateUser(_ context.Context, in driver.AdminCreateUserInput) (*driver.User, error) {
	if err := checkUsername(in.Username); err != nil {
		return nil, err
	}

	if err := checkMessageAction(in.MessageAction); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	pool, ok := m.userPools.Get(in.UserPoolID)
	if !ok {
		return nil, poolNotFound(in.UserPoolID)
	}

	if in.MessageAction == driver.MessageActionResend {
		return m.resendInvite(&pool, in)
	}

	if err := validateAttributes(&pool, in.UserAttributes, false); err != nil {
		return nil, err
	}

	password := in.TemporaryPassword
	if password == "" {
		password = generatePassword(pool.Policies.PasswordPolicy)
	} else if err := checkPassword(password, pool.Policies.PasswordPolicy); err != nil {
		return nil, err
	}

	sub := idgen.UUID()
	attrs := mergeAttributes([]driver.Attribute{{Name: attrSub, Value: sub}}, in.UserAttributes)

	username, attrs, err := m.newUsername(&pool, in.Username, sub, attrs)
	if err != nil {
		return nil, err
	}

	if err := m.claimSignIns(&pool, userKey(pool.ID, username), attrs, in.ForceAliasCreation, true); err != nil {
		return nil, err
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
			UserStatus:           driver.UserStatusForceChangePassword,
		},
	}
	rec.PasswordSalt, rec.PasswordHash = hashPassword(password)

	m.users.Set(userKey(pool.ID, username), copyUserRecord(rec))

	out := copyUserRecord(rec).User

	return &out, nil
}

// resendInvite handles AdminCreateUser with MessageAction RESEND: only a user
// still in FORCE_CHANGE_PASSWORD can be re-invited, and it gets a fresh
// temporary password.
//
//nolint:gocritic // hugeParam: in is the caller's input, passed through by value
func (m *Mock) resendInvite(pool *driver.UserPool, in driver.AdminCreateUserInput) (*driver.User, error) {
	key, rec, ok := m.resolveUser(pool, in.Username)
	if !ok {
		return nil, userNotFound()
	}

	if rec.User.UserStatus != driver.UserStatusForceChangePassword {
		return nil, unsupportedUserState("Resend not possible. %s status is not FORCE_CHANGE_PASSWORD", in.Username)
	}

	password := in.TemporaryPassword
	if password == "" {
		password = generatePassword(pool.Policies.PasswordPolicy)
	} else if err := checkPassword(password, pool.Policies.PasswordPolicy); err != nil {
		return nil, err
	}

	rec = copyUserRecord(rec)
	rec.PasswordSalt, rec.PasswordHash = hashPassword(password)
	rec.User.UserLastModifiedDate = m.now()
	m.users.Set(key, rec)

	out := copyUserRecord(rec).User

	return &out, nil
}

// newUsername decides the stored username for a new user and checks it is
// free. In a pool that signs in with email or phone number, the username must
// be one of those, the stored username is the sub, and the value is copied into
// the matching attribute.
func (m *Mock) newUsername(
	pool *driver.UserPool, username, sub string, attrs []driver.Attribute,
) (string, []driver.Attribute, error) {
	if len(pool.UsernameAttributes) == 0 {
		if err := checkNotAliasFormat(pool, username); err != nil {
			return "", nil, err
		}

		if m.users.Has(userKey(pool.ID, username)) {
			return "", nil, usernameExists("User account already exists")
		}

		return username, attrs, nil
	}

	attr, err := usernameAttributeFor(pool.UsernameAttributes, username)
	if err != nil {
		return "", nil, err
	}

	return sub, mergeAttributes(attrs, []driver.Attribute{{Name: attr, Value: username}}), nil
}

// usernameAttributeFor returns the username attribute (email or phone_number) a
// sign-in name belongs to, or the InvalidParameterException Cognito sends when
// it is neither.
func usernameAttributeFor(allowed []string, username string) (string, error) {
	switch {
	case isEmailFormat(username) && slices.Contains(allowed, attrEmail):
		return attrEmail, nil
	case isPhoneFormat(username) && slices.Contains(allowed, attrPhoneNumber):
		return attrPhoneNumber, nil
	}

	switch {
	case slices.Contains(allowed, attrEmail) && slices.Contains(allowed, attrPhoneNumber):
		return "", invalidParameter("Username should be either an email or a phone number.")
	case slices.Contains(allowed, attrEmail):
		return "", invalidParameter("Username should be an email.")
	default:
		return "", invalidParameter("Username should be a phone number.")
	}
}

// checkNotAliasFormat rejects a username shaped like an email or phone number
// when the pool uses that attribute as a sign-in alias.
func checkNotAliasFormat(pool *driver.UserPool, username string) error {
	if slices.Contains(pool.AliasAttributes, attrEmail) && isEmailFormat(username) {
		return invalidParameter("Username cannot be of email format, since user pool is configured for email alias.")
	}

	if slices.Contains(pool.AliasAttributes, attrPhoneNumber) && isPhoneFormat(username) {
		return invalidParameter("Username cannot be of phone number format, since user pool is configured for phone number alias.")
	}

	return nil
}

// AdminGetUser returns one user.
func (m *Mock) AdminGetUser(_ context.Context, userPoolID, username string) (*driver.User, error) {
	pool, ok := m.userPools.Get(userPoolID)
	if !ok {
		return nil, poolNotFound(userPoolID)
	}

	_, rec, ok := m.resolveUser(&pool, username)
	if !ok {
		return nil, userNotFound()
	}

	out := copyUserRecord(rec).User

	return &out, nil
}

// ListUsers returns a page of a pool's users sorted by username.
//
//nolint:gocritic // hugeParam: taken by value to match the driver interface
func (m *Mock) ListUsers(_ context.Context, in driver.ListUsersInput) ([]driver.User, string, error) {
	if in.Limit > defaultPageSize {
		return nil, "", invalidParameter("1 validation error detected: Value '%d' at 'limit' failed to satisfy constraint: "+
			"Member must have value less than or equal to %d", in.Limit, defaultPageSize)
	}

	if !m.userPools.Has(in.UserPoolID) {
		return nil, "", poolNotFound(in.UserPoolID)
	}

	filter, err := parseUserFilter(in.Filter)
	if err != nil {
		return nil, "", err
	}

	var matched []driver.User

	users := m.poolUsers(in.UserPoolID)
	for i := range users {
		if filter.matches(&users[i].User) {
			u := copyUserRecord(users[i]).User
			u.Attributes = selectAttributes(u.Attributes, in.AttributesToGet)
			matched = append(matched, u)
		}
	}

	page, next, err := paginate(matched, driver.Pagination{NextToken: in.PaginationToken, MaxResults: in.Limit})
	if err != nil {
		return nil, "", err
	}

	return page, next, nil
}

// AdminDeleteUser removes a user.
func (m *Mock) AdminDeleteUser(_ context.Context, userPoolID, username string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	pool, ok := m.userPools.Get(userPoolID)
	if !ok {
		return poolNotFound(userPoolID)
	}

	key, _, ok := m.resolveUser(&pool, username)
	if !ok {
		return userNotFound()
	}

	m.users.Delete(key)

	return nil
}

// AdminUpdateUserAttributes sets attribute values. Changing email or phone
// number without also setting its verified flag marks it unverified, as Cognito
// does. A sign-in value another user already holds fails with
// AliasExistsException.
func (m *Mock) AdminUpdateUserAttributes(_ context.Context, userPoolID, username string, attrs []driver.Attribute) error {
	return m.updateUser(userPoolID, username, func(pool *driver.UserPool, key string, rec *userRecord) error {
		if err := validateAttributes(pool, attrs, true); err != nil {
			return err
		}

		updates := slices.Clone(attrs)
		updates = append(updates, unverifyChanged(rec.User.Attributes, attrs)...)
		merged := mergeAttributes(rec.User.Attributes, updates)

		if err := m.claimSignIns(pool, key, merged, false, false); err != nil {
			return err
		}

		rec.User.Attributes = merged

		return nil
	})
}

// unverifyChanged returns the "<attr>_verified=false" updates for an email or
// phone number that changes value without its verified flag in the same call.
func unverifyChanged(current, updates []driver.Attribute) []driver.Attribute {
	var out []driver.Attribute

	for _, pair := range [][2]string{{attrEmail, attrEmailVerified}, {attrPhoneNumber, attrPhoneNumberVerified}} {
		v, changed := lastValue(updates, pair[0])
		if !changed || v == attrValue(current, pair[0]) {
			continue
		}

		if _, set := lastValue(updates, pair[1]); !set {
			out = append(out, driver.Attribute{Name: pair[1], Value: attrFalse})
		}
	}

	return out
}

// AdminDeleteUserAttributes removes attributes from a user.
func (m *Mock) AdminDeleteUserAttributes(_ context.Context, userPoolID, username string, names []string) error {
	return m.updateUser(userPoolID, username, func(pool *driver.UserPool, _ string, rec *userRecord) error {
		for _, name := range names {
			a, ok := schemaAttribute(pool, name)
			if !ok {
				return schemaError(name, "Attribute does not exist in the schema.")
			}

			if !a.Mutable {
				return schemaError(name, "Attribute cannot be updated. (changing an immutable attribute)")
			}
		}

		rec.User.Attributes = slices.DeleteFunc(rec.User.Attributes, func(a driver.Attribute) bool {
			return slices.Contains(names, a.Name)
		})

		return nil
	})
}

// AdminSetUserPassword sets a user's password.
func (m *Mock) AdminSetUserPassword(_ context.Context, userPoolID, username, password string, permanent bool) error {
	return m.updateUser(userPoolID, username, func(pool *driver.UserPool, _ string, rec *userRecord) error {
		if err := checkPassword(password, pool.Policies.PasswordPolicy); err != nil {
			return err
		}

		rec.PasswordSalt, rec.PasswordHash = hashPassword(password)
		rec.User.UserStatus = driver.UserStatusForceChangePassword

		if permanent {
			rec.User.UserStatus = driver.UserStatusConfirmed
		}

		return nil
	})
}

// AdminEnableUser enables a user.
func (m *Mock) AdminEnableUser(_ context.Context, userPoolID, username string) error {
	return m.updateUser(userPoolID, username, func(_ *driver.UserPool, _ string, rec *userRecord) error {
		rec.User.Enabled = true

		return nil
	})
}

// AdminDisableUser disables a user.
func (m *Mock) AdminDisableUser(_ context.Context, userPoolID, username string) error {
	return m.updateUser(userPoolID, username, func(_ *driver.UserPool, _ string, rec *userRecord) error {
		rec.User.Enabled = false

		return nil
	})
}

// AdminResetUserPassword moves a user to RESET_REQUIRED. A user who has not
// yet replaced the temporary password cannot be reset.
func (m *Mock) AdminResetUserPassword(_ context.Context, userPoolID, username string) error {
	return m.updateUser(userPoolID, username, func(_ *driver.UserPool, _ string, rec *userRecord) error {
		if rec.User.UserStatus == driver.UserStatusForceChangePassword {
			return notAuthorized("User password cannot be reset in the current state.")
		}

		rec.User.UserStatus = driver.UserStatusResetRequired

		return nil
	})
}

// updateUser runs fn on a copy of the resolved user under the mutation lock and
// stores the result with a fresh last-modified time.
func (m *Mock) updateUser(userPoolID, username string, fn func(pool *driver.UserPool, key string, rec *userRecord) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	pool, ok := m.userPools.Get(userPoolID)
	if !ok {
		return poolNotFound(userPoolID)
	}

	key, rec, ok := m.resolveUser(&pool, username)
	if !ok {
		return userNotFound()
	}

	rec = copyUserRecord(rec)
	if err := fn(&pool, key, &rec); err != nil {
		return err
	}

	rec.User.UserLastModifiedDate = m.now()
	m.users.Set(key, rec)

	return nil
}

// resolveUser finds a user by username, then by the sign-in attributes the pool
// allows: a username attribute (email or phone number), or an alias. Email and
// phone aliases only resolve once verified; preferred_username always does.
func (m *Mock) resolveUser(pool *driver.UserPool, name string) (string, userRecord, bool) {
	if rec, ok := m.users.Get(userKey(pool.ID, name)); ok {
		return userKey(pool.ID, name), rec, true
	}

	users := m.poolUsers(pool.ID)
	for i := range users {
		if signInMatches(pool, &users[i].User, name) {
			return userKey(pool.ID, users[i].User.Username), users[i], true
		}
	}

	return "", userRecord{}, false
}

// poolUserKeys returns the store keys of a pool's users, sorted. Pool ids never
// contain the key separator, so the "<poolID>/" prefix selects exactly one pool.
func (m *Mock) poolUserKeys(poolID string) []string {
	prefix := userKey(poolID, "")

	keys := slices.DeleteFunc(m.users.Keys(), func(k string) bool { return !strings.HasPrefix(k, prefix) })
	sort.Strings(keys)

	return keys
}

// poolUsers returns a pool's users sorted by username.
func (m *Mock) poolUsers(poolID string) []userRecord {
	keys := m.poolUserKeys(poolID)
	out := make([]userRecord, 0, len(keys))

	for _, k := range keys {
		if rec, ok := m.users.Get(k); ok {
			out = append(out, rec)
		}
	}

	return out
}

// countUsers returns the number of users in a pool.
func (m *Mock) countUsers(poolID string) int32 {
	return int32(len(m.poolUserKeys(poolID))) //nolint:gosec // user counts stay far below int32 max
}

// deletePoolUsers removes every user of a pool.
func (m *Mock) deletePoolUsers(poolID string) {
	for _, k := range m.poolUserKeys(poolID) {
		m.users.Delete(k)
	}
}

func checkUsername(username string) error {
	if username == "" {
		return invalidParameter("1 validation error detected: Value null at 'username' failed to satisfy constraint: Member must not be null")
	}

	if len(username) > maxUsernameLen || strings.ContainsFunc(username, isSpaceRune) {
		return invalidParameter("1 validation error detected: Value at 'username' failed to satisfy constraint: " +
			"Member must satisfy regular expression pattern: [\\p{L}\\p{M}\\p{S}\\p{N}\\p{P}]+")
	}

	return nil
}

func isSpaceRune(r rune) bool { return r == ' ' || r == '\t' || r == '\n' || r == '\r' }

func checkMessageAction(action string) error {
	switch action {
	case "", driver.MessageActionResend, driver.MessageActionSuppress:
		return nil
	default:
		return invalidParameter("1 validation error detected: Value '%s' at 'messageAction' failed to satisfy constraint: "+
			"Member must satisfy enum value set: [RESEND, SUPPRESS]", action)
	}
}

func isEmailFormat(s string) bool {
	at := strings.IndexByte(s, '@')

	return at > 0 && at < len(s)-1 && !strings.Contains(s[at+1:], "@")
}

func isPhoneFormat(s string) bool {
	if len(s) < 2 || s[0] != '+' {
		return false
	}

	for _, r := range s[1:] {
		if r < '0' || r > '9' {
			return false
		}
	}

	return true
}
