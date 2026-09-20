// Package idgen provides ID generators for various cloud resource types.
package idgen

import (
	"crypto/rand"
	"fmt"
	"hash/fnv"
	"sync/atomic"
)

// guidNodeMask isolates the low 48 bits used as a GUID's node field.
const guidNodeMask = 0xffffffffffff

// uuidByteLen is the number of random bytes in a version-4 UUID.
const uuidByteLen = 16

// RFC 4122 version/variant bit masks for a version-4 UUID.
const (
	versionMask  = 0x0f
	version4Bits = 0x40
	variantMask  = 0x3f
	variantBits  = 0x80
)

// UUID returns a random RFC 4122 version-4 UUID as a 36-character string
// (8-4-4-4-12 hex with hyphens). AWS Secrets Manager version ids take this
// shape, and callers can pin one via a client request token. It draws from
// crypto/rand; a random source failure falls back to a zero-filled UUID so the
// function never panics.
func UUID() string {
	var b [uuidByteLen]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Degrade to an all-zero UUID rather than panic; still 36 chars.
		b = [uuidByteLen]byte{}
	}

	// RFC 4122: set the version to 4 and the variant to 10xx.
	b[6] = (b[6] & versionMask) | version4Bits
	b[8] = (b[8] & variantMask) | variantBits

	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// suffixAlphabet is the 6-character random-suffix character set AWS appends to a
// Secrets Manager ARN's resource segment (:secret:<name>-<suffix>).
const suffixAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// secretARNSuffixLen is the length of the ARN suffix AWS appends.
const secretARNSuffixLen = 6

// SecretARNSuffix returns a fresh random 6-character alphanumeric suffix,
// matching the trailing "-XXXXXX" AWS adds to a Secrets Manager ARN's resource
// segment. Real Secrets Manager draws a new suffix on every CreateSecret call —
// including when a secret is deleted and recreated under the same name — so
// the old ARN never accidentally resolves to the new secret; callers must
// generate it once at creation time and persist the resulting ARN (it is not
// re-derivable from the name). It draws from crypto/rand; a random source
// failure falls back to an all-'a' suffix so the function never panics.
func SecretARNSuffix() string {
	out := make([]byte, secretARNSuffixLen)

	b := make([]byte, secretARNSuffixLen)
	if _, err := rand.Read(b); err != nil {
		for i := range out {
			out[i] = suffixAlphabet[0]
		}

		return string(out)
	}

	for i, v := range b {
		out[i] = suffixAlphabet[int(v)%len(suffixAlphabet)]
	}

	return string(out)
}

// SyntheticGUID derives a deterministic GUID-shaped string from seed. The value
// is synthetic (a stand-in for an Azure principal/tenant id), not a real
// security identifier — the same seed always yields the same GUID so tests are
// stable.
func SyntheticGUID(seed string) string {
	h1 := fnv.New64a()
	_, _ = h1.Write([]byte(seed))
	a := h1.Sum64()

	h2 := fnv.New64a()
	_, _ = h2.Write([]byte(seed + "#salt"))
	b := h2.Sum64()

	//nolint:gosec,mnd // intentional narrowing + GUID field-width shifts
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		uint32(a>>32), uint16(a>>16), uint16(a), uint16(b>>48), b&guidNodeMask)
}

//nolint:gochecknoglobals // counter must be a package-level variable for atomic operations across all ID generators
var counter uint64

// next returns a monotonically increasing number.
func next() uint64 {
	return atomic.AddUint64(&counter, 1)
}

// GenerateID generates an ID with the given prefix (e.g., "i-", "vpc-", "sg-").
func GenerateID(prefix string) string {
	return fmt.Sprintf("%s%08x", prefix, next())
}

// Character sets for AWS-shaped random identifiers.
const (
	base32Upper   = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"
	lowerAlphaNum = "abcdefghijklmnopqrstuvwxyz0123456789"
	hexLower      = "0123456789abcdef"
)

// randString returns n characters drawn from alphabet via crypto/rand. A random
// source failure degrades to a correctly-shaped constant string rather than
// panicking, so callers always get a valid-length id.
func randString(n int, alphabet string) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		for i := range b {
			b[i] = alphabet[0]
		}

		return string(b)
	}

	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}

	return string(b)
}

// accessKeyRandLen is the number of characters after the AKIA/ASIA prefix in an
// AWS access key id (prefix + 16 = 20 chars total).
const accessKeyRandLen = 16

// AccessKeyID returns an AWS-shaped IAM access key id: the "AKIA" prefix plus 16
// uppercase base32 characters (20 chars total). The AWS SDKs and CLI validate the
// shape client-side (minimum length 16) before sending UpdateAccessKey /
// DeleteAccessKey, so a shorter id makes key rotation/deletion impossible through
// the real tooling.
func AccessKeyID() string { return "AKIA" + randString(accessKeyRandLen, base32Upper) }

// TempAccessKeyID is the STS temporary-credential variant of AccessKeyID (ASIA
// prefix), used for assumed-role / session credentials.
func TempAccessKeyID() string { return "ASIA" + randString(accessKeyRandLen, base32Upper) }

// longIDRandLen is the hex-suffix length AWS's newer resource ids use.
const longIDRandLen = 17

// GenerateLongID returns prefix followed by a 17-character lowercase hex suffix —
// the length AWS's newer resource ids use (e.g. VPC Lattice svc-/sn-/tg-/rule-).
// The SDKs validate these client-side, so the legacy 8-char GenerateID is too
// short and is rejected before the request is sent.
func GenerateLongID(prefix string) string { return prefix + randString(longIDRandLen, hexLower) }

// appSyncAPIIDLen is the length of an AppSync GraphQL API id.
const appSyncAPIIDLen = 26

// AppSyncAPIID returns a 26-character lowercase-alphanumeric id matching the shape
// AppSync mints for a GraphQL API. The SDKs embed it in ARNs the CLI validates, so
// the legacy 8-char id breaks TagResource/ListTagsForResource client-side.
func AppSyncAPIID() string { return randString(appSyncAPIIDLen, lowerAlphaNum) }

// ARN generates an AWS ARN.
func ARN(partition, service, region, accountID, resource string) string {
	return fmt.Sprintf("arn:%s:%s:%s:%s:%s", partition, service, region, accountID, resource)
}

// AWSARN generates an AWS ARN with the standard "aws" partition.
func AWSARN(service, region, accountID, resource string) string {
	return ARN("aws", service, region, accountID, resource)
}

// AzureID generates an Azure resource ID.
func AzureID(subscriptionID, resourceGroup, provider, resourceType, name string) string {
	return fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/%s/%s/%s",
		subscriptionID, resourceGroup, provider, resourceType, name)
}

// GCPID generates a GCP resource self-link.
func GCPID(project, resourceType, name string) string {
	return fmt.Sprintf("projects/%s/%s/%s", project, resourceType, name)
}

// Reset resets the counter (for testing).
func Reset() {
	atomic.StoreUint64(&counter, 0)
}
