package privateca

import (
	"crypto/sha256"
	"encoding/base64"
)

// pemLineWidth is the column width PEM base64 bodies are wrapped at, matching the
// classic 64-character PEM line.
const pemLineWidth = 64

// deterministicCACert returns a stable, PEM-looking self-signed CA certificate
// derived from the resource key. No real X.509 signing is performed; the value is
// byte-stable across reads so a Terraform refresh never drifts on it.
func deterministicCACert(key string) string {
	return pemBlock("CERTIFICATE", "ca-cert:"+key)
}

// deterministicCSR returns a stable, PEM-looking certificate signing request for
// a subordinate CA awaiting activation.
func deterministicCSR(key string) string {
	return pemBlock("CERTIFICATE REQUEST", "csr:"+key)
}

// deterministicCert returns a stable, PEM-looking leaf certificate for an issued
// certificate.
func deterministicCert(key string) string {
	return pemBlock("CERTIFICATE", "leaf-cert:"+key)
}

// deterministicIssuer returns a stable issuer identifier standing in for the CA
// that issued a certificate.
func deterministicIssuer(key string) string {
	return "cloudemu-issuer-" + shortDigest(key)
}

// pemBlock renders seed as a deterministic PEM block with the given type label.
func pemBlock(label, seed string) string {
	sum := sha256.Sum256([]byte(seed))
	body := base64.StdEncoding.EncodeToString(append(sum[:], sum[:]...))

	wrapped := ""

	for i := 0; i < len(body); i += pemLineWidth {
		end := i + pemLineWidth
		if end > len(body) {
			end = len(body)
		}

		wrapped += body[i:end] + "\n"
	}

	return "-----BEGIN " + label + "-----\n" + wrapped + "-----END " + label + "-----\n"
}

// shortDigest returns a stable short hex digest of s for use in identifiers.
func shortDigest(s string) string {
	sum := sha256.Sum256([]byte(s))

	return base64.RawURLEncoding.EncodeToString(sum[:8])
}
