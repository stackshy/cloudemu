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
// segment. Real Secrets Manager draws a new suffix on every CreateSecret call,
// including when a secret is deleted and recreated under the same name, so
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
// security identifier: the same seed always yields the same GUID so tests are
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

// Counter returns the current value of the shared id counter, the suffix of
// the most recently minted GenerateID or OCID. A snapshot records it so a
// restore can move the counter past every id it brings back.
func Counter() uint64 {
	return atomic.LoadUint64(&counter)
}

// AdvanceTo moves the shared id counter up to n when it is lower, so the next
// GenerateID or OCID is above n. It never moves the counter back: loading an
// older snapshot into a process that has already minted more ids keeps the
// higher value. Safe for concurrent use with GenerateID.
func AdvanceTo(n uint64) {
	for {
		cur := atomic.LoadUint64(&counter)
		if cur >= n || atomic.CompareAndSwapUint64(&counter, cur, n) {
			return
		}
	}
}

// GenerateID generates an ID with the given prefix (e.g., "i-", "vpc-", "sg-").
func GenerateID(prefix string) string {
	return fmt.Sprintf("%s%08x", prefix, next())
}

// Character sets for AWS-shaped random identifiers.
const (
	base32Upper   = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"
	lowerAlphaNum = "abcdefghijklmnopqrstuvwxyz0123456789"
	upperAlphaNum = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	hexLower      = "0123456789abcdef"
)

// randString returns n characters drawn from alphabet via crypto/rand. A
// random source failure is returned rather than papered over with a constant,
// so no caller can end up with a predictable id.
func randString(n int, alphabet string) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate id: %w", err)
	}

	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}

	return string(b), nil
}

// prefixed returns prefix followed by n random characters from alphabet.
func prefixed(prefix string, n int, alphabet string) (string, error) {
	r, err := randString(n, alphabet)
	if err != nil {
		return "", err
	}

	return prefix + r, nil
}

// accessKeyRandLen is the number of characters after the AKIA/ASIA prefix in an
// AWS access key id (prefix + 16 = 20 chars total).
const accessKeyRandLen = 16

// AccessKeyID returns an AWS-shaped IAM access key id: the "AKIA" prefix plus 16
// uppercase base32 characters (20 chars total). The AWS SDKs and CLI validate the
// shape client-side (minimum length 16) before sending UpdateAccessKey /
// DeleteAccessKey, so a shorter id makes key rotation/deletion impossible through
// the real tooling.
func AccessKeyID() (string, error) { return prefixed("AKIA", accessKeyRandLen, base32Upper) }

// TempAccessKeyID is the STS temporary-credential variant of AccessKeyID (ASIA
// prefix), used for assumed-role / session credentials.
func TempAccessKeyID() (string, error) { return prefixed("ASIA", accessKeyRandLen, base32Upper) }

// iamUniqueIDRandLen is the number of characters after the 4-letter prefix of
// an IAM unique id (AIDA..., AROA...), 21 characters in all.
const iamUniqueIDRandLen = 17

// IAMUniqueID returns an IAM unique id: prefix (AIDA for a user, AROA for a
// role, AGPA for a group, AIPA for an instance profile) followed by 17 random
// uppercase base32 characters, like AIDAJQABLZS4A3QDU576Q. The id must never
// repeat, even across a restart, because a policy that names a user or role
// is bound to its unique id: a later entity with the same name must not
// inherit what the policy granted the old one.
func IAMUniqueID(prefix string) (string, error) {
	return prefixed(prefix, iamUniqueIDRandLen, base32Upper)
}

// Signing secrets. These authenticate callers, so unlike the ids above they
// never fall back to a predictable value: a crypto/rand failure is returned.
const (
	base64Alphabet  = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	ociTokenSymbols = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789:;<>()#_.-+/"

	secretAccessKeyLen = 40
	sessionTokenLen    = 356
	ociAuthTokenLen    = 20

	byteRange = 256
)

// secureString returns n characters drawn uniformly from alphabet via
// crypto/rand. Rejection sampling keeps alphabets whose size does not divide
// 256 free of modulo bias.
func secureString(n int, alphabet string) (string, error) {
	// Bytes at or above limit would favor the first 256%len characters.
	limit := byteRange - byteRange%len(alphabet)

	out := make([]byte, 0, n)
	buf := make([]byte, n)

	for len(out) < n {
		if _, err := rand.Read(buf); err != nil {
			return "", fmt.Errorf("generate secret: %w", err)
		}

		for _, b := range buf {
			if int(b) >= limit {
				continue
			}

			out = append(out, alphabet[int(b)%len(alphabet)])
			if len(out) == n {
				break
			}
		}
	}

	return string(out), nil
}

// SecretAccessKey returns a 40-character secret drawn from the base64
// alphabet, the shape of an AWS secret access key.
func SecretAccessKey() (string, error) { return secureString(secretAccessKeyLen, base64Alphabet) }

// SessionToken returns a long random token from the base64 alphabet, standing
// in for the opaque session token STS issues with temporary credentials.
func SessionToken() (string, error) { return secureString(sessionTokenLen, base64Alphabet) }

// OCIAuthToken returns a 20-character OCI auth token, which mixes letters,
// digits and punctuation.
func OCIAuthToken() (string, error) { return secureString(ociAuthTokenLen, ociTokenSymbols) }

// longIDRandLen is the hex-suffix length AWS's newer resource ids use.
const longIDRandLen = 17

// GenerateLongID returns prefix followed by a 17-character lowercase hex suffix,
// the length AWS's newer resource ids use (e.g. VPC Lattice svc-/sn-/tg-/rule-).
// The SDKs validate these client-side, so the legacy 8-char GenerateID is too
// short and is rejected before the request is sent.
func GenerateLongID(prefix string) (string, error) { return prefixed(prefix, longIDRandLen, hexLower) }

// appSyncAPIIDLen is the length of an AppSync GraphQL API id.
const appSyncAPIIDLen = 26

// AppSyncAPIID returns a 26-character lowercase-alphanumeric id matching the shape
// AppSync mints for a GraphQL API. The SDKs embed it in ARNs the CLI validates, so
// the legacy 8-char id breaks TagResource/ListTagsForResource client-side.
func AppSyncAPIID() (string, error) { return randString(appSyncAPIIDLen, lowerAlphaNum) }

// bedrockProfileIDLen is the length of a Bedrock application inference profile id.
const bedrockProfileIDLen = 12

// BedrockInferenceProfileID returns a 12-character lowercase-alphanumeric id,
// the shape Bedrock mints for an application inference profile.
func BedrockInferenceProfileID() (string, error) {
	return randString(bedrockProfileIDLen, lowerAlphaNum)
}

// bedrockAgentIDLen is the length of every Bedrock Agents resource id.
const bedrockAgentIDLen = 10

// BedrockAgentResourceID returns a 10-character uppercase-alphanumeric id, the
// shape Bedrock Agents mints for agents, aliases, knowledge bases, data sources,
// ingestion jobs, flows and prompts. Their ARNs embed it and the tagging API's
// ARN pattern requires exactly this shape.
func BedrockAgentResourceID() (string, error) { return randString(bedrockAgentIDLen, upperAlphaNum) }

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
