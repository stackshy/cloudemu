// Package jwtsign issues and verifies RS256 JSON Web Tokens and publishes the
// matching JSON Web Key Set. It is the shared signer for the emulated identity
// services (Cognito user pools first) that must hand out tokens a real client
// library can verify against the service's JWKS endpoint.
//
// Each issuer owns one or more Keys. A Key's ID (the JWT "kid") is its RFC 7638
// JWK thumbprint, so it is stable across persistence and never collides between
// keys. Rotation is adding a new Key and signing with it while the old one stays
// in the verification set (and the JWKS) until its tokens have expired.
package jwtsign

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
)

// algRS256 is the only signing algorithm this package issues or accepts.
const algRS256 = "RS256"

const (
	keyBits     = 2048
	jwtSegments = 3

	// iatLeewaySeconds tolerates an issuer clock slightly ahead of the
	// verifier: a token whose iat is at most this far in the future passes.
	iatLeewaySeconds = 60
)

// Errors returned by Verify. ErrExpired is kept apart from ErrInvalidToken so a
// service can answer with its distinct "token has expired" message.
var (
	ErrInvalidToken = errors.New("jwtsign: invalid token")
	ErrUnknownKey   = errors.New("jwtsign: unknown signing key")
	ErrExpired      = errors.New("jwtsign: token has expired")
	ErrNotYetValid  = errors.New("jwtsign: token is not yet valid")

	errNotRSA = errors.New("jwtsign: parse key: not an RSA key")
)

// Key is an RS256 signing key and the kid it is published under.
type Key struct {
	ID      string
	Private *rsa.PrivateKey
}

// NewRSAKey generates a 2048-bit RSA key whose ID is its JWK thumbprint.
func NewRSAKey() (*Key, error) {
	priv, err := rsa.GenerateKey(rand.Reader, keyBits)
	if err != nil {
		return nil, fmt.Errorf("jwtsign: generate key: %w", err)
	}

	return &Key{ID: thumbprint(&priv.PublicKey), Private: priv}, nil
}

// MarshalPKCS8 encodes the private key as PKCS#8 DER, the form snapshots store.
func MarshalPKCS8(k *Key) ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(k.Private)
	if err != nil {
		return nil, fmt.Errorf("jwtsign: marshal key: %w", err)
	}

	return der, nil
}

// ParsePKCS8 decodes a PKCS#8 DER RSA private key. The kid is recomputed from
// the public key, so it matches the one the key had when it was marshaled.
func ParsePKCS8(der []byte) (*Key, error) {
	parsed, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, fmt.Errorf("jwtsign: parse key: %w", err)
	}

	priv, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("%w: got %T", errNotRSA, parsed)
	}

	return &Key{ID: thumbprint(&priv.PublicKey), Private: priv}, nil
}

type header struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
}

// Sign returns the compact RS256 JWT for claims, with k.ID as the header kid.
func Sign(k *Key, claims map[string]any) (string, error) {
	h, err := json.Marshal(header{Alg: algRS256, Kid: k.ID})
	if err != nil {
		return "", fmt.Errorf("jwtsign: header: %w", err)
	}

	p, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("jwtsign: claims: %w", err)
	}

	signingInput := b64(h) + "." + b64(p)
	sum := sha256.Sum256([]byte(signingInput))

	sig, err := rsa.SignPKCS1v15(rand.Reader, k.Private, crypto.SHA256, sum[:])
	if err != nil {
		return "", fmt.Errorf("jwtsign: sign: %w", err)
	}

	return signingInput + "." + b64(sig), nil
}

// Verify checks token against keys and the clock and returns its claims. It
// requires alg RS256, a kid present in keys, a valid signature, and an exp
// claim. It rejects a token at or after exp, before nbf, or with iat more than
// 60 seconds in the future. Numeric claims come back as json.Number.
func Verify(token string, keys []*Key, clock config.Clock) (map[string]any, error) {
	parts := strings.Split(token, ".")
	if len(parts) != jwtSegments {
		return nil, fmt.Errorf("%w: want %d segments", ErrInvalidToken, jwtSegments)
	}

	var h header
	if err := decodeJSON(parts[0], &h); err != nil {
		return nil, err
	}

	if h.Alg != algRS256 {
		return nil, fmt.Errorf("%w: alg %q", ErrInvalidToken, h.Alg)
	}

	key := findKey(keys, h.Kid)
	if key == nil {
		return nil, fmt.Errorf("%w: kid %q", ErrUnknownKey, h.Kid)
	}

	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, fmt.Errorf("%w: signature encoding", ErrInvalidToken)
	}

	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(&key.Private.PublicKey, crypto.SHA256, sum[:], sig); err != nil {
		return nil, fmt.Errorf("%w: signature", ErrInvalidToken)
	}

	var claims map[string]any
	if err := decodeJSON(parts[1], &claims); err != nil {
		return nil, err
	}

	if err := checkTimes(claims, clock.Now()); err != nil {
		return nil, err
	}

	return claims, nil
}

func checkTimes(claims map[string]any, now time.Time) error {
	exp, ok := numericDate(claims, "exp")
	if !ok {
		return fmt.Errorf("%w: missing or non-numeric exp", ErrInvalidToken)
	}

	unix := now.Unix()

	if unix >= exp {
		return ErrExpired
	}

	if nbf, ok := numericDate(claims, "nbf"); ok && unix < nbf {
		return ErrNotYetValid
	}

	if iat, ok := numericDate(claims, "iat"); ok && unix+iatLeewaySeconds < iat {
		return ErrNotYetValid
	}

	return nil
}

func numericDate(claims map[string]any, name string) (int64, bool) {
	n, ok := claims[name].(json.Number)
	if !ok {
		return 0, false
	}

	v, err := n.Int64()
	if err != nil {
		f, ferr := n.Float64()
		if ferr != nil {
			return 0, false
		}

		v = int64(f)
	}

	return v, true
}

func findKey(keys []*Key, kid string) *Key {
	for _, k := range keys {
		if k.ID == kid {
			return k
		}
	}

	return nil
}

// JWK is one public key in a JSON Web Key Set, in the field set Cognito's
// jwks.json publishes.
type JWK struct {
	Alg string `json:"alg"`
	E   string `json:"e"`
	Kid string `json:"kid"`
	Kty string `json:"kty"`
	N   string `json:"n"`
	Use string `json:"use"`
}

// JWKSet is a JSON Web Key Set document ({"keys": [...]}).
type JWKSet struct {
	Keys []JWK `json:"keys"`
}

// JWKS returns the public half of keys, in order, as a JSON Web Key Set.
func JWKS(keys ...*Key) JWKSet {
	set := JWKSet{Keys: make([]JWK, 0, len(keys))}

	for _, k := range keys {
		pub := &k.Private.PublicKey
		set.Keys = append(set.Keys, JWK{
			Alg: algRS256,
			E:   b64(big.NewInt(int64(pub.E)).Bytes()),
			Kid: k.ID,
			Kty: "RSA",
			N:   b64(pub.N.Bytes()),
			Use: "sig",
		})
	}

	return set
}

// thumbprint is the RFC 7638 JWK thumbprint of an RSA public key: the base64url
// SHA-256 of the canonical {"e","kty","n"} member set.
func thumbprint(pub *rsa.PublicKey) string {
	canonical := `{"e":"` + b64(big.NewInt(int64(pub.E)).Bytes()) + `","kty":"RSA","n":"` + b64(pub.N.Bytes()) + `"}`
	sum := sha256.Sum256([]byte(canonical))

	return b64(sum[:])
}

func decodeJSON(seg string, v any) error {
	raw, err := base64.RawURLEncoding.DecodeString(seg)
	if err != nil {
		return fmt.Errorf("%w: segment encoding", ErrInvalidToken)
	}

	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()

	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("%w: segment json", ErrInvalidToken)
	}

	return nil
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
