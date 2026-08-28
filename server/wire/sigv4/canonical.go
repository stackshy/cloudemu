package sigv4

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

// canonicalRequest rebuilds the SigV4 canonical request:
//
//	METHOD\n{canonicalURI}\n{canonicalQuery}\n{canonicalHeaders}\n{signedHeaders}\n{hashedPayload}
func canonicalRequest(r *http.Request, body []byte, in *signInputs) string {
	headers, signed := canonicalHeaders(r, in.signedHeaders)

	return strings.Join([]string{
		r.Method,
		canonicalURI(r, in.service),
		canonicalQuery(r, in.presigned),
		headers,
		signed,
		hashedPayload(r, body, in.presigned),
	}, "\n")
}

// canonicalURI is the URI-encoded path. S3 signs the raw escaped path;
// every other service URI-encodes it a second time (matching aws-sdk-go-v2's
// EscapePath, which the SDK applies unless DisableURIPathEscaping is set — and
// it is set only for S3).
func canonicalURI(r *http.Request, service string) string {
	path := r.URL.EscapedPath()
	if path == "" {
		path = "/"
	}

	if service == "s3" {
		return path
	}

	return uriEncode(path, false)
}

// canonicalQuery is the sorted, URI-encoded query string. For the presigned
// form the X-Amz-Signature parameter is excluded (it is the value being
// verified).
func canonicalQuery(r *http.Request, presigned bool) string {
	q := r.URL.Query()

	pairs := make([]string, 0, len(q))

	for key, vals := range q {
		if presigned && key == "X-Amz-Signature" {
			continue
		}

		encKey := uriEncode(key, true)
		for _, v := range vals {
			pairs = append(pairs, encKey+"="+uriEncode(v, true))
		}
	}

	sort.Strings(pairs)

	return strings.Join(pairs, "&")
}

// canonicalHeaders returns the canonical header block and the ";"-joined signed
// header list. Only the signed headers are included; names are lowercased and
// sorted, values trimmed.
func canonicalHeaders(r *http.Request, signed []string) (headerBlock, signedList string) {
	names := make([]string, len(signed))
	copy(names, signed)
	sort.Strings(names)

	var b strings.Builder

	for _, name := range names {
		b.WriteString(name)
		b.WriteByte(':')
		b.WriteString(headerValue(r, name))
		b.WriteByte('\n')
	}

	return b.String(), strings.Join(names, ";")
}

// headerValue returns the canonical value for a signed header. Go lifts Host
// and Content-Length out of r.Header into dedicated fields, so those are read
// back explicitly.
func headerValue(r *http.Request, name string) string {
	switch name {
	case "host":
		return trimAll(r.Host)
	case "content-length":
		return strconv.FormatInt(r.ContentLength, 10)
	}

	vals := r.Header.Values(name) // Values canonicalizes the lookup key
	out := make([]string, len(vals))

	for i, v := range vals {
		out[i] = trimAll(v)
	}

	return strings.Join(out, ",")
}

// hashedPayload is the x-amz-content-sha256 header when present (S3 sends it,
// possibly as the literal UNSIGNED-PAYLOAD), the presigned sentinel, or the hex
// SHA-256 of the body.
func hashedPayload(r *http.Request, body []byte, presigned bool) string {
	if v := r.Header.Get("X-Amz-Content-Sha256"); v != "" {
		return v
	}

	if presigned {
		return "UNSIGNED-PAYLOAD"
	}

	sum := sha256.Sum256(body)

	return hex.EncodeToString(sum[:])
}

// uriEncode percent-encodes s per RFC 3986 as AWS SigV4 requires: unreserved
// characters (A-Z a-z 0-9 - _ . ~) pass through, '/' is preserved when
// encodeSlash is false (path segments), and everything else is percent-encoded
// with uppercase hex.
func uriEncode(s string, encodeSlash bool) string {
	var b strings.Builder

	b.Grow(len(s))

	for i := 0; i < len(s); i++ {
		c := s[i]

		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9',
			c == '-', c == '_', c == '.', c == '~':
			b.WriteByte(c)
		case c == '/' && !encodeSlash:
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			b.WriteByte(hexUpper[c>>4])
			b.WriteByte(hexUpper[c&0x0f])
		}
	}

	return b.String()
}

const hexUpper = "0123456789ABCDEF"

// trimAll trims surrounding whitespace and collapses internal runs of spaces to
// a single space, matching SigV4 header-value canonicalization.
func trimAll(s string) string {
	s = strings.TrimSpace(s)
	if !strings.Contains(s, "  ") {
		return s
	}

	return strings.Join(strings.Fields(s), " ")
}
