package kms

import (
	"encoding/json"
	"strings"
)

// Cloud KMS enums are transmitted on the wire as either the canonical STRING
// name (REST / Terraform) or the protobuf INTEGER value (GAPIC gRPC-transcoded
// clients). The maps below mirror the google.cloud.kms.v1 protobuf enum values
// exactly (cloud.google.com/go/kms apiv1/kmspb), so an int is normalized to the
// same canonical name a string request would carry, and both round-trip to the
// STRING form real Cloud KMS emits.

// purposeNames maps CryptoKeyPurpose integers to canonical names.
//
//nolint:gochecknoglobals // immutable protobuf enum lookup table
var purposeNames = map[int32]string{
	0:  "CRYPTO_KEY_PURPOSE_UNSPECIFIED",
	1:  "ENCRYPT_DECRYPT",
	5:  "ASYMMETRIC_SIGN",
	6:  "ASYMMETRIC_DECRYPT",
	7:  "RAW_ENCRYPT_DECRYPT",
	9:  "MAC",
	10: "KEY_ENCAPSULATION",
	11: "AES_WRAPPING",
}

// protectionLevelNames maps ProtectionLevel integers to canonical names.
//
//nolint:gochecknoglobals // immutable protobuf enum lookup table
var protectionLevelNames = map[int32]string{
	0: "PROTECTION_LEVEL_UNSPECIFIED",
	1: "SOFTWARE",
	2: "HSM",
	3: "EXTERNAL",
	4: "EXTERNAL_VPC",
	5: "HSM_SINGLE_TENANT",
}

// stateNames maps CryptoKeyVersionState integers to canonical names.
//
//nolint:gochecknoglobals // immutable protobuf enum lookup table
var stateNames = map[int32]string{
	0:  "CRYPTO_KEY_VERSION_STATE_UNSPECIFIED",
	1:  "ENABLED",
	2:  "DISABLED",
	3:  "DESTROYED",
	4:  "DESTROY_SCHEDULED",
	5:  "PENDING_GENERATION",
	6:  "PENDING_IMPORT",
	7:  "IMPORT_FAILED",
	8:  "GENERATION_FAILED",
	9:  "PENDING_EXTERNAL_DESTRUCTION",
	10: "EXTERNAL_DESTRUCTION_FAILED",
}

// algorithmNames maps CryptoKeyVersionAlgorithm integers to canonical names.
//
//nolint:gochecknoglobals // immutable protobuf enum lookup table
var algorithmNames = map[int32]string{
	0:  "CRYPTO_KEY_VERSION_ALGORITHM_UNSPECIFIED",
	1:  "GOOGLE_SYMMETRIC_ENCRYPTION",
	41: "AES_128_GCM",
	19: "AES_256_GCM",
	42: "AES_128_CBC",
	43: "AES_256_CBC",
	44: "AES_128_CTR",
	45: "AES_256_CTR",
	2:  "RSA_SIGN_PSS_2048_SHA256",
	3:  "RSA_SIGN_PSS_3072_SHA256",
	4:  "RSA_SIGN_PSS_4096_SHA256",
	15: "RSA_SIGN_PSS_4096_SHA512",
	5:  "RSA_SIGN_PKCS1_2048_SHA256",
	6:  "RSA_SIGN_PKCS1_3072_SHA256",
	7:  "RSA_SIGN_PKCS1_4096_SHA256",
	16: "RSA_SIGN_PKCS1_4096_SHA512",
	28: "RSA_SIGN_RAW_PKCS1_2048",
	29: "RSA_SIGN_RAW_PKCS1_3072",
	30: "RSA_SIGN_RAW_PKCS1_4096",
	8:  "RSA_DECRYPT_OAEP_2048_SHA256",
	9:  "RSA_DECRYPT_OAEP_3072_SHA256",
	10: "RSA_DECRYPT_OAEP_4096_SHA256",
	17: "RSA_DECRYPT_OAEP_4096_SHA512",
	37: "RSA_DECRYPT_OAEP_2048_SHA1",
	38: "RSA_DECRYPT_OAEP_3072_SHA1",
	39: "RSA_DECRYPT_OAEP_4096_SHA1",
	12: "EC_SIGN_P256_SHA256",
	13: "EC_SIGN_P384_SHA384",
	31: "EC_SIGN_SECP256K1_SHA256",
	40: "EC_SIGN_ED25519",
	32: "HMAC_SHA256",
	33: "HMAC_SHA1",
	34: "HMAC_SHA384",
	35: "HMAC_SHA512",
	36: "HMAC_SHA224",
	18: "EXTERNAL_SYMMETRIC_ENCRYPTION",
}

// rawEnum captures an enum field that arrives on the wire as either a JSON
// string (canonical name) or a JSON number (protobuf value). It defers
// name resolution to normalize, which knows the field's value table.
type rawEnum struct {
	str   string
	num   int32
	isNum bool
	set   bool
}

// UnmarshalJSON accepts a quoted enum name or a bare integer.
func (e *rawEnum) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "" || s == "null" {
		return nil
	}

	e.set = true

	if s[0] == '"' {
		return json.Unmarshal(b, &e.str)
	}

	e.isNum = true

	return json.Unmarshal(b, &e.num)
}

// normalize resolves the raw token to a canonical enum name using the field's
// value table. present reports whether the field was supplied at all; ok
// reports whether the supplied token names a known enum value.
func (e rawEnum) normalize(names map[int32]string) (canonical string, ok, present bool) {
	if !e.set {
		return "", false, false
	}

	if e.isNum {
		name, exists := names[e.num]
		return name, exists, true
	}

	up := strings.ToUpper(strings.TrimSpace(e.str))
	for _, name := range names {
		if name == up {
			return up, true, true
		}
	}

	return "", false, true
}
