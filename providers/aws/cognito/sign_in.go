package cognito

import (
	"slices"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/cognito/driver"
)

// Sign-in values other than the username. A pool either signs in with email or
// phone number in place of a username (UsernameAttributes), or keeps usernames
// and lets email, phone number and preferred_username act as aliases
// (AliasAttributes). Either way a value can belong to one user only. Email and
// phone aliases only count once verified; an unverified copy is just data.

// verifiedFlag maps a verifiable alias attribute to its verification flag.
func verifiedFlag(attr string) (string, bool) {
	switch attr {
	case attrEmail:
		return attrEmailVerified, true
	case attrPhoneNumber:
		return attrPhoneNumberVerified, true
	default:
		return "", false
	}
}

// sameSignIn compares two sign-in values. Email addresses ignore case.
func sameSignIn(attr, a, b string) bool {
	if attr == attrEmail {
		return strings.EqualFold(a, b)
	}

	return a == b
}

// activeSignIns returns the attribute values a user can sign in with, keyed by
// attribute name.
func activeSignIns(pool *driver.UserPool, attrs []driver.Attribute) map[string]string {
	out := map[string]string{}

	for _, attr := range pool.UsernameAttributes {
		if v := attrValue(attrs, attr); v != "" {
			out[attr] = v
		}
	}

	for _, attr := range pool.AliasAttributes {
		v := attrValue(attrs, attr)
		if v == "" {
			continue
		}

		if flag, ok := verifiedFlag(attr); ok && attrValue(attrs, flag) != attrTrue {
			continue
		}

		out[attr] = v
	}

	return out
}

// signInMatches reports whether name is one of the user's sign-in values.
func signInMatches(pool *driver.UserPool, u *driver.User, name string) bool {
	for attr, v := range activeSignIns(pool, u.Attributes) {
		if sameSignIn(attr, v, name) {
			return true
		}
	}

	return false
}

// signInClaim is another user holding a sign-in value the caller wants.
type signInClaim struct {
	key  string
	attr string
}

// claimSignIns checks that the sign-in values in attrs (the user's attributes
// after the change) are free, ignoring the user stored under selfKey.
//
// A taken username attribute fails with UsernameExistsException on create and
// AliasExistsException on update. A taken alias fails with AliasExistsException,
// unless force is set and the alias is an email or phone number: then it moves
// to this user and the previous holder is marked unverified. Nothing changes
// unless every value is free or movable.
func (m *Mock) claimSignIns(pool *driver.UserPool, selfKey string, attrs []driver.Attribute, force, create bool) error {
	var moves []signInClaim

	wanted := activeSignIns(pool, attrs)

	for _, key := range m.poolUserKeys(pool.ID) {
		if key == selfKey {
			continue
		}

		other, ok := m.users.Get(key)
		if !ok {
			continue
		}

		for attr, v := range activeSignIns(pool, other.User.Attributes) {
			want, ok := wanted[attr]
			if !ok || !sameSignIn(attr, want, v) {
				continue
			}

			if err := claimError(pool, attr, force, create); err != nil {
				return err
			}

			moves = append(moves, signInClaim{key: key, attr: attr})
		}
	}

	for _, mv := range moves {
		m.unverify(mv.key, mv.attr)
	}

	return nil
}

// claimError returns the error for a sign-in value held by another user, or nil
// when ForceAliasCreation may move it.
func claimError(pool *driver.UserPool, attr string, force, create bool) error {
	if slices.Contains(pool.UsernameAttributes, attr) {
		if create {
			return usernameExists("An account with the given " + attr + " already exists.")
		}

		return aliasExists(attr)
	}

	if _, verifiable := verifiedFlag(attr); force && verifiable {
		return nil
	}

	return aliasExists(attr)
}

// unverify marks a user's email or phone number unverified, which drops it as
// a sign-in alias.
func (m *Mock) unverify(key, attr string) {
	flag, ok := verifiedFlag(attr)
	if !ok {
		return
	}

	rec, ok := m.users.Get(key)
	if !ok {
		return
	}

	rec = copyUserRecord(rec)
	rec.User.Attributes = mergeAttributes(rec.User.Attributes, []driver.Attribute{{Name: flag, Value: attrFalse}})
	rec.User.UserLastModifiedDate = m.now()
	m.users.Set(key, rec)
}
