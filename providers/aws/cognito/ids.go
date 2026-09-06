package cognito

import "crypto/rand"

// Cognito identifier alphabets and lengths.
const (
	// poolSuffixLen is the number of alphanumeric characters after "region_" in a
	// user-pool id (e.g. us-east-1_aB3dE9fG1).
	poolSuffixLen = 9
	// clientIDLen is the length of an app-client id (lowercase alphanumeric).
	clientIDLen = 26
	// clientSecretLen is the length of a generated app-client secret.
	clientSecretLen = 51
)

// alnumMixed is the case-mixed alphanumeric alphabet used for pool-id suffixes.
const alnumMixed = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

// alnumLower is the lowercase alphanumeric alphabet used for client ids.
const alnumLower = "abcdefghijklmnopqrstuvwxyz0123456789"

// randString returns a random string of n characters drawn from alphabet using
// crypto/rand. A random-source failure falls back to the alphabet's first
// character so the function never panics.
func randString(n int, alphabet string) string {
	out := make([]byte, n)

	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		for i := range out {
			out[i] = alphabet[0]
		}

		return string(out)
	}

	for i, v := range b {
		out[i] = alphabet[int(v)%len(alphabet)]
	}

	return string(out)
}

// newPoolID builds a user-pool id of the form "<region>_<9 alphanumeric>".
func newPoolID(region string) string {
	return region + "_" + randString(poolSuffixLen, alnumMixed)
}

// newClientID returns a fresh 26-character lowercase-alphanumeric client id.
func newClientID() string { return randString(clientIDLen, alnumLower) }

// newClientSecret returns a fresh 51-character client secret.
func newClientSecret() string { return randString(clientSecretLen, alnumLower) }
