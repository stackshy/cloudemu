package cognito

import (
	"context"
	"fmt"
	"strings"

	"github.com/stackshy/cloudemu/v2/internal/jwtsign"
)

// poolKeys are a pool's two RS256 signing keys. Cognito signs ID and access
// tokens with different keys, so the two token kinds have different kids.
type poolKeys struct {
	id     *jwtsign.Key
	access *jwtsign.Key
}

// keysFor returns a pool's signing keys, creating them on first use.
func (m *Mock) keysFor(poolID string) (*poolKeys, error) {
	m.keysMu.Lock()
	defer m.keysMu.Unlock()

	if k, ok := m.keys[poolID]; ok {
		return k, nil
	}

	id, err := jwtsign.NewRSAKey()
	if err != nil {
		return nil, fmt.Errorf("cognito: id token key: %w", err)
	}

	access, err := jwtsign.NewRSAKey()
	if err != nil {
		return nil, fmt.Errorf("cognito: access token key: %w", err)
	}

	k := &poolKeys{id: id, access: access}
	m.keys[poolID] = k

	return k, nil
}

// existingKeys returns a pool's keys without creating them.
func (m *Mock) existingKeys(poolID string) (*poolKeys, bool) {
	m.keysMu.Lock()
	defer m.keysMu.Unlock()

	k, ok := m.keys[poolID]

	return k, ok
}

func (m *Mock) deletePoolKeys(poolID string) {
	m.keysMu.Lock()
	delete(m.keys, poolID)
	m.keysMu.Unlock()
}

// issuer is the iss claim of a pool's tokens. The region comes from the pool
// id, which is "<region>_<suffix>".
func issuer(poolID string) string {
	region, _, _ := strings.Cut(poolID, "_")

	return "https://cognito-idp." + region + ".amazonaws.com/" + poolID
}

// poolFromIssuer returns the pool id an iss claim names.
func poolFromIssuer(iss string) string {
	i := strings.LastIndexByte(iss, '/')
	if i < 0 {
		return ""
	}

	return iss[i+1:]
}

// SigningKeys returns a pool's issuer and the JWKS that verifies its tokens.
func (m *Mock) SigningKeys(_ context.Context, userPoolID string) (string, jwtsign.JWKSet, error) {
	if !m.userPools.Has(userPoolID) {
		return "", jwtsign.JWKSet{}, poolNotFound(userPoolID)
	}

	k, err := m.keysFor(userPoolID)
	if err != nil {
		return "", jwtsign.JWKSet{}, err
	}

	return issuer(userPoolID), jwtsign.JWKS(k.id, k.access), nil
}
