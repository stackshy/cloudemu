package persist_test

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"testing"

	cloudemu "github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

func kmsCall(t *testing.T, h http.Handler, c wireCall) map[string]any {
	t.Helper()

	code, body := doWire(t, h, c)
	if code != http.StatusOK {
		t.Fatalf("%s %s = %d %s", c.method, c.path, code, body)
	}

	var out map[string]any
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}

	return out
}

// TestKMSKeysSurviveRestore covers GKMS-N1: key rings, crypto keys, versions
// and their key material used to live only in the wire handler, so a
// serve --persist restart lost them and stored ciphertexts became
// undecryptable.
func TestKMSKeysSurviveRestore(t *testing.T) {
	const (
		loc  = "/v1/projects/p1/locations/us-central1"
		ring = loc + "/keyRings/r1"
		sym  = ring + "/cryptoKeys/sym"
		sig  = ring + "/cryptoKeys/sig/cryptoKeyVersions/1"
	)

	src := cloudemu.NewGCP()
	srcSrv := gcpserver.NewFromProvider(src)

	mustWire(t, srcSrv, []wireCall{
		{http.MethodPost, loc + "/keyRings?keyRingId=r1", `{}`},
		{http.MethodPost, ring + "/cryptoKeys?cryptoKeyId=sym", `{"purpose":"ENCRYPT_DECRYPT"}`},
		{http.MethodPost, ring + "/cryptoKeys?cryptoKeyId=sig",
			`{"purpose":"ASYMMETRIC_SIGN","versionTemplate":{"algorithm":"EC_SIGN_P256_SHA256"}}`},
		{http.MethodPost, ring + ":setIamPolicy",
			`{"policy":{"bindings":[{"role":"roles/cloudkms.admin","members":["user:a@example.com"]}]}}`},
	})

	aad := base64.StdEncoding.EncodeToString([]byte("ctx"))
	enc := kmsCall(t, srcSrv, wireCall{http.MethodPost, sym + ":encrypt",
		`{"plaintext":"` + base64.StdEncoding.EncodeToString([]byte("hello")) + `","additionalAuthenticatedData":"` + aad + `"}`})

	digest := sha256.Sum256([]byte("msg"))
	signed := kmsCall(t, srcSrv, wireCall{http.MethodPost, sig + ":asymmetricSign",
		`{"digest":{"sha256":"` + base64.StdEncoding.EncodeToString(digest[:]) + `"}}`})
	pubBefore := kmsCall(t, srcSrv, wireCall{http.MethodGet, sig + "/publicKey", ""})

	dst := cloudemu.NewGCP()
	roundTrip(t, "gcp", src.SnapshotServices(), dst.SnapshotServices())
	dstSrv := gcpserver.NewFromProvider(dst)

	assertSameReads(t, srcSrv, dstSrv, []string{ring, sym, loc + "/keyRings", sym + "/cryptoKeyVersions", ring + ":getIamPolicy"})

	dec := kmsCall(t, dstSrv, wireCall{http.MethodPost, sym + ":decrypt",
		`{"ciphertext":"` + enc["ciphertext"].(string) + `","additionalAuthenticatedData":"` + aad + `"}`})
	if got, _ := base64.StdEncoding.DecodeString(dec["plaintext"].(string)); string(got) != "hello" {
		t.Fatalf("decrypt after restore = %q, want hello", got)
	}

	pubAfter := kmsCall(t, dstSrv, wireCall{http.MethodGet, sig + "/publicKey", ""})
	if pubAfter["pem"] != pubBefore["pem"] {
		t.Fatalf("public key changed across restore")
	}

	block, _ := pem.Decode([]byte(pubAfter["pem"].(string)))

	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		t.Fatalf("ParsePKIXPublicKey: %v", err)
	}

	sigBytes, _ := base64.StdEncoding.DecodeString(signed["signature"].(string))
	if !ecdsa.VerifyASN1(pub.(*ecdsa.PublicKey), digest[:], sigBytes) {
		t.Fatal("signature made before restore does not verify with the restored public key")
	}

	// A key restored from a snapshot keeps signing with the same private key.
	again := kmsCall(t, dstSrv, wireCall{http.MethodPost, sig + ":asymmetricSign",
		`{"digest":{"sha256":"` + base64.StdEncoding.EncodeToString(digest[:]) + `"}}`})

	sigAgain, _ := base64.StdEncoding.DecodeString(again["signature"].(string))
	if !ecdsa.VerifyASN1(pub.(*ecdsa.PublicKey), digest[:], sigAgain) {
		t.Fatal("signature made after restore does not verify with the original public key")
	}
}
