package jwtsign

import (
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
)

var epoch = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) //nolint:gochecknoglobals // fixed test instant

func mustKey(t *testing.T) *Key {
	t.Helper()

	k, err := NewRSAKey()
	if err != nil {
		t.Fatalf("NewRSAKey: %v", err)
	}

	return k
}

func claimsAt(now time.Time, ttl time.Duration) map[string]any {
	return map[string]any{
		"sub":       "user-1",
		"token_use": "access",
		"iat":       now.Unix(),
		"nbf":       now.Unix(),
		"exp":       now.Add(ttl).Unix(),
	}
}

func TestSignVerifyRoundTrip(t *testing.T) {
	k := mustKey(t)
	clock := config.NewFakeClock(epoch)

	tok, err := Sign(k, claimsAt(epoch, time.Hour))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	if n := strings.Count(tok, "."); n != 2 {
		t.Fatalf("token has %d dots, want 2", n)
	}

	got, err := Verify(tok, []*Key{k}, clock)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}

	if got["sub"] != "user-1" || got["token_use"] != "access" {
		t.Fatalf("claims = %v", got)
	}

	hdr := decodeSegment(t, strings.Split(tok, ".")[0])
	if hdr["alg"] != "RS256" || hdr["kid"] != k.ID {
		t.Fatalf("header = %v, want alg RS256 kid %s", hdr, k.ID)
	}
}

func TestVerifyRejectsTamperedPayload(t *testing.T) {
	k := mustKey(t)

	tok, err := Sign(k, claimsAt(epoch, time.Hour))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	parts := strings.Split(tok, ".")
	forged := claimsAt(epoch, time.Hour)
	forged["sub"] = "admin"
	b, _ := json.Marshal(forged)
	parts[1] = base64.RawURLEncoding.EncodeToString(b)

	_, err = Verify(strings.Join(parts, "."), []*Key{k}, config.NewFakeClock(epoch))
	if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("tampered token: err = %v, want ErrInvalidToken", err)
	}
}

func TestVerifyRejectsWrongKeyWithSameKID(t *testing.T) {
	signer := mustKey(t)
	other := mustKey(t)
	other.ID = signer.ID

	tok, _ := Sign(signer, claimsAt(epoch, time.Hour))

	if _, err := Verify(tok, []*Key{other}, config.NewFakeClock(epoch)); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("err = %v, want ErrInvalidToken", err)
	}
}

func TestVerifyRejectsAlgNoneAndHS256(t *testing.T) {
	k := mustKey(t)
	payload := segmentOf(t, claimsAt(epoch, time.Hour))

	for _, alg := range []string{"none", "HS256", ""} {
		hdr := segmentOf(t, map[string]any{"alg": alg, "kid": k.ID})
		tok := hdr + "." + payload + "."

		if _, err := Verify(tok, []*Key{k}, config.NewFakeClock(epoch)); !errors.Is(err, ErrInvalidToken) {
			t.Fatalf("alg %q: err = %v, want ErrInvalidToken", alg, err)
		}
	}
}

func TestVerifyRejectsMalformed(t *testing.T) {
	k := mustKey(t)

	for _, tok := range []string{"", "a.b", "a.b.c.d", "!!.!!.!!", "e30.e30.e30"} {
		if _, err := Verify(tok, []*Key{k}, config.NewFakeClock(epoch)); !errors.Is(err, ErrInvalidToken) &&
			!errors.Is(err, ErrUnknownKey) {
			t.Fatalf("token %q: err = %v, want rejection", tok, err)
		}
	}
}

func TestVerifyExpiry(t *testing.T) {
	k := mustKey(t)
	clock := config.NewFakeClock(epoch)

	tok, _ := Sign(k, claimsAt(epoch, time.Hour))

	clock.Advance(time.Hour - time.Second)

	if _, err := Verify(tok, []*Key{k}, clock); err != nil {
		t.Fatalf("one second before exp: %v", err)
	}

	clock.Advance(time.Second)

	if _, err := Verify(tok, []*Key{k}, clock); !errors.Is(err, ErrExpired) {
		t.Fatalf("at exp: err = %v, want ErrExpired", err)
	}
}

func TestVerifyNotBeforeAndIssuedInFuture(t *testing.T) {
	k := mustKey(t)
	future := epoch.Add(time.Minute)

	nbf := claimsAt(epoch, time.Hour)
	nbf["nbf"] = future.Unix()
	tok, _ := Sign(k, nbf)

	if _, err := Verify(tok, []*Key{k}, config.NewFakeClock(epoch)); !errors.Is(err, ErrNotYetValid) {
		t.Fatalf("nbf in future: err = %v, want ErrNotYetValid", err)
	}

	iat := claimsAt(epoch, time.Hour)
	delete(iat, "nbf")

	iat["iat"] = epoch.Add(2 * time.Minute).Unix()
	tok, _ = Sign(k, iat)

	if _, err := Verify(tok, []*Key{k}, config.NewFakeClock(epoch)); !errors.Is(err, ErrNotYetValid) {
		t.Fatalf("iat 2m in future: err = %v, want ErrNotYetValid", err)
	}

	// A small issuer clock skew is tolerated.
	iat["iat"] = epoch.Add(time.Minute).Unix()
	tok, _ = Sign(k, iat)

	if _, err := Verify(tok, []*Key{k}, config.NewFakeClock(epoch)); err != nil {
		t.Fatalf("iat within the 60s leeway: %v", err)
	}
}

func TestVerifyRequiresExp(t *testing.T) {
	k := mustKey(t)
	c := claimsAt(epoch, time.Hour)
	delete(c, "exp")
	tok, _ := Sign(k, c)

	if _, err := Verify(tok, []*Key{k}, config.NewFakeClock(epoch)); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("missing exp: err = %v, want ErrInvalidToken", err)
	}
}

func TestVerifyUnknownKID(t *testing.T) {
	signer := mustKey(t)
	other := mustKey(t)
	tok, _ := Sign(signer, claimsAt(epoch, time.Hour))

	if _, err := Verify(tok, []*Key{other}, config.NewFakeClock(epoch)); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("err = %v, want ErrUnknownKey", err)
	}
}

// TestKIDRotation: after a new key is added, tokens signed with the old key
// still verify while the old key stays in the set, new tokens carry the new
// kid, and dropping the old key retires its tokens.
func TestKIDRotation(t *testing.T) {
	oldKey := mustKey(t)
	newKey := mustKey(t)
	clock := config.NewFakeClock(epoch)

	if oldKey.ID == newKey.ID {
		t.Fatalf("two keys share kid %q", oldKey.ID)
	}

	oldTok, _ := Sign(oldKey, claimsAt(epoch, time.Hour))
	newTok, _ := Sign(newKey, claimsAt(epoch, time.Hour))

	both := []*Key{newKey, oldKey}
	for _, tok := range []string{oldTok, newTok} {
		if _, err := Verify(tok, both, clock); err != nil {
			t.Fatalf("verify during rotation: %v", err)
		}
	}

	if _, err := Verify(oldTok, []*Key{newKey}, clock); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("retired key: err = %v, want ErrUnknownKey", err)
	}
}

func TestJWKSPublishesPublicKeys(t *testing.T) {
	k1, k2 := mustKey(t), mustKey(t)

	set := JWKS(k1, k2)

	raw, err := json.Marshal(set)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	if strings.Contains(string(raw), `"d"`) || strings.Contains(string(raw), `"p"`) {
		t.Fatalf("JWKS leaks private material: %s", raw)
	}

	var doc struct {
		Keys []map[string]string `json:"keys"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(doc.Keys) != 2 {
		t.Fatalf("got %d keys, want 2", len(doc.Keys))
	}

	for i, k := range []*Key{k1, k2} {
		j := doc.Keys[i]
		if j["kid"] != k.ID || j["kty"] != "RSA" || j["alg"] != "RS256" || j["use"] != "sig" || j["e"] != "AQAB" {
			t.Fatalf("jwk %d = %v", i, j)
		}

		n, err := base64.RawURLEncoding.DecodeString(j["n"])
		if err != nil {
			t.Fatalf("n not base64url: %v", err)
		}

		pub := &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: k.Private.E}
		if !pub.Equal(&k.Private.PublicKey) {
			t.Fatalf("jwk %d modulus does not match the key", i)
		}
	}
}

func TestPKCS8RoundTripKeepsKID(t *testing.T) {
	k := mustKey(t)

	der, err := MarshalPKCS8(k)
	if err != nil {
		t.Fatalf("MarshalPKCS8: %v", err)
	}

	back, err := ParsePKCS8(der)
	if err != nil {
		t.Fatalf("ParsePKCS8: %v", err)
	}

	if back.ID != k.ID || !back.Private.Equal(k.Private) {
		t.Fatalf("round trip changed the key (kid %q -> %q)", k.ID, back.ID)
	}

	tok, _ := Sign(k, claimsAt(epoch, time.Hour))
	if _, err := Verify(tok, []*Key{back}, config.NewFakeClock(epoch)); err != nil {
		t.Fatalf("token from original does not verify with restored key: %v", err)
	}

	if _, err := ParsePKCS8([]byte("junk")); err == nil {
		t.Fatalf("ParsePKCS8(junk) succeeded")
	}
}

func decodeSegment(t *testing.T, seg string) map[string]any {
	t.Helper()

	b, err := base64.RawURLEncoding.DecodeString(seg)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	return m
}

func segmentOf(t *testing.T, v any) string {
	t.Helper()

	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	return base64.RawURLEncoding.EncodeToString(b)
}
