package kendra

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"regexp"

	"github.com/stackshy/cloudemu/v2/services/kendra/driver"
)

// Documented Kendra identifier and name limits.
const (
	indexIDLength   = 36
	maxChildIDLen   = 100
	maxIndexNameLen = 1000
	maxChildName    = 100
	maxTokenLen     = 800

	defaultPageSize = 10
	maxPageSize     = 100
	maxSyncPageSize = 10
)

// errBadTokenText is the message for a NextToken the mock did not mint.
const errBadTokenText = "NextToken is not valid"

// idPattern is the shape of an index id and of the ids of an index's child
// resources: alphanumeric first, then alphanumerics or hyphens. The data source
// id and name patterns additionally allow underscores.
var (
	idPattern      = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9-]*$`)
	idUnderscoreRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)
	namePatternRE  = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)
	featuredNameRE = regexp.MustCompile(`^[a-zA-Z0-9][ a-zA-Z0-9_-]*$`)
)

// validateIndexID rejects a malformed IndexId with a ValidationException before
// any lookup, as the real API does (fixed length 36, documented pattern).
func validateIndexID(id string) error {
	if len(id) != indexIDLength || !idPattern.MatchString(id) {
		return validation("1 validation error detected: Value '%s' at 'indexId' failed to satisfy constraint: "+
			"Member must have length 36 and satisfy regular expression pattern: [a-zA-Z0-9][a-zA-Z0-9-]*", id)
	}

	return nil
}

// validateChildID rejects a malformed id of an index child resource or data
// source (length 1-100, documented pattern).
func validateChildID(field, id string, underscore bool) error {
	re := idPattern
	pattern := "[a-zA-Z0-9][a-zA-Z0-9-]*"

	if underscore {
		re = idUnderscoreRE
		pattern = "[a-zA-Z0-9][a-zA-Z0-9_-]*"
	}

	if len(id) < 1 || len(id) > maxChildIDLen || !re.MatchString(id) {
		return validation("1 validation error detected: Value '%s' at '%s' failed to satisfy constraint: "+
			"Member must have length between 1 and 100 and satisfy regular expression pattern: %s", id, field, pattern)
	}

	return nil
}

// validateName checks a resource name against the documented pattern and length
// (1..max).
func validateName(name string, maxLen int) error {
	if len(name) < 1 || len(name) > maxLen || !namePatternRE.MatchString(name) {
		return validation("1 validation error detected: Value '%s' at 'name' failed to satisfy constraint: "+
			"Member must have length between 1 and %d and satisfy regular expression pattern: [a-zA-Z0-9][a-zA-Z0-9_-]*",
			name, maxLen)
	}

	return nil
}

// newTokenKey draws the per-mock key that signs pagination tokens.
func newTokenKey() []byte {
	k := make([]byte, sha256.Size)
	if _, err := rand.Read(k); err != nil {
		// crypto/rand.Read never fails on supported platforms; a zero key only
		// makes tokens guessable, never wrong.
		return k
	}

	return k
}

// tokenMACLen is the number of MAC bytes kept in a token.
const (
	tokenMACLen    = 16
	tokenOffsetLen = 8
)

// encodeToken renders a list offset as an opaque, signed token bound to scope
// (the list it belongs to), so it cannot be guessed, edited or replayed on a
// different list.
func (m *Mock) encodeToken(scope string, offset int) string {
	buf := make([]byte, tokenOffsetLen+tokenMACLen)
	binary.BigEndian.PutUint64(buf[:tokenOffsetLen], uint64(offset)) //nolint:gosec // offset is a non-negative list index
	copy(buf[tokenOffsetLen:], m.tokenMAC(scope, buf[:tokenOffsetLen]))

	return base64.RawURLEncoding.EncodeToString(buf)
}

func (m *Mock) tokenMAC(scope string, offset []byte) []byte {
	h := hmac.New(sha256.New, m.tokenKey)
	h.Write([]byte(scope))
	h.Write([]byte{0})
	h.Write(offset)

	return h.Sum(nil)[:tokenMACLen]
}

// decodeToken returns the offset a token carries, or false when the token is not
// one this mock minted for scope.
func (m *Mock) decodeToken(scope, token string) (int, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != tokenOffsetLen+tokenMACLen {
		return 0, false
	}

	if !hmac.Equal(raw[tokenOffsetLen:], m.tokenMAC(scope, raw[:tokenOffsetLen])) {
		return 0, false
	}

	return int(binary.BigEndian.Uint64(raw[:tokenOffsetLen])), true //nolint:gosec // offset was minted by encodeToken from an int
}

// paginate returns the window and next token for a list of n items. MaxResults
// outside 1..maxAllowed and a NextToken that is oversized, unsigned or minted for
// another list are ValidationExceptions.
func (m *Mock) paginate(scope string, n int, page driver.Page, maxAllowed int32) (start, end int, next string, err error) {
	limit := defaultPageSize

	if page.MaxResults != 0 {
		if page.MaxResults < 1 || page.MaxResults > maxAllowed {
			return 0, 0, "", validation("1 validation error detected: Value '%d' at 'maxResults' failed to satisfy "+
				"constraint: Member must have value between 1 and %d", page.MaxResults, maxAllowed)
		}

		limit = int(page.MaxResults)
	}

	if page.NextToken != "" {
		if len(page.NextToken) > maxTokenLen {
			return 0, 0, "", validation("%s", errBadTokenText)
		}

		off, ok := m.decodeToken(scope, page.NextToken)
		if !ok {
			return 0, 0, "", validation("%s", errBadTokenText)
		}

		start = min(off, n)
	}

	end = start + limit
	if end >= n {
		return start, n, "", nil
	}

	return start, end, m.encodeToken(scope, end), nil
}
