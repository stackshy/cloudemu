package secretsmanager

import (
	"crypto/rand"
	"encoding/base64"
	"math/big"
	"strconv"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	secretsdriver "github.com/stackshy/cloudemu/v2/services/secrets/driver"
)

// defaultPasswordLength is GetRandomPassword's default when PasswordLength is
// unset, matching the real service.
const defaultPasswordLength = 32

// isBinary reports whether a request carried its payload as SecretBinary
// (SecretString empty, SecretBinary present).
func isBinary(secretString string, secretBinary []byte) bool {
	return secretString == "" && len(secretBinary) > 0
}

// pageWindow resolves an opaque NextToken + MaxResults into a [start,end) window
// over a stable set of length total, returning the token for the next page
// ("" on the last page). The caller sorts before paginating so the offset token
// stays valid across pages.
func pageWindow(token string, maxResults int32, total int) (start, end int, next string, err error) {
	start, err = decodePageToken(token)
	if err != nil {
		return 0, 0, "", err
	}

	if start > total {
		start = total
	}

	limit := int(maxResults)
	if limit <= 0 {
		limit = total - start
	}

	end = start + limit
	if end >= total {
		return start, total, "", nil
	}

	return start, end, encodePageToken(end), nil
}

func encodePageToken(offset int) string {
	return base64.StdEncoding.EncodeToString([]byte(strconv.Itoa(offset)))
}

func decodePageToken(token string) (int, error) {
	if token == "" {
		return 0, nil
	}

	b, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		return 0, cerrors.New(cerrors.InvalidArgument, "invalid NextToken")
	}

	n, err := strconv.Atoi(string(b))
	if err != nil || n < 0 {
		return 0, cerrors.New(cerrors.InvalidArgument, "invalid NextToken")
	}

	return n, nil
}

// matchesSecretFilters reports whether a secret satisfies every ListSecrets
// filter (filters are AND'd, values within a filter OR'd). Supported keys are
// name, description, tag-key, tag-value, and all — matching is substring-based
// as the real service does.
func matchesSecretFilters(info *secretsdriver.SecretInfo, filters []secretFilter) bool {
	for i := range filters {
		if !matchesSecretFilter(info, &filters[i]) {
			return false
		}
	}

	return true
}

func matchesSecretFilter(info *secretsdriver.SecretInfo, f *secretFilter) bool {
	if len(f.Values) == 0 {
		return true
	}

	for _, v := range f.Values {
		if secretFieldContains(info, f.Key, v) {
			return true
		}
	}

	return false
}

func secretFieldContains(info *secretsdriver.SecretInfo, key, value string) bool {
	switch key {
	case "name":
		return strings.Contains(info.Name, value)
	case "description":
		return strings.Contains(info.Description, value)
	case "tag-key":
		return anyTagContains(info.Tags, value, true)
	case "tag-value":
		return anyTagContains(info.Tags, value, false)
	case "all":
		return secretFieldContains(info, "name", value) ||
			secretFieldContains(info, "description", value) ||
			secretFieldContains(info, "tag-key", value) ||
			secretFieldContains(info, "tag-value", value)
	default:
		// Unsupported key: do not exclude on it.
		return true
	}
}

// anyTagContains reports whether any tag key (matchKey=true) or value contains
// the substring.
func anyTagContains(tags map[string]string, value string, matchKey bool) bool {
	for k, v := range tags {
		field := v
		if matchKey {
			field = k
		}

		if strings.Contains(field, value) {
			return true
		}
	}

	return false
}

// randomPassword generates a password honoring GetRandomPassword's character-set
// toggles. RequireEachIncludedType is not enforced (best-effort emulation).
func randomPassword(req getRandomPasswordRequest) (string, error) {
	const (
		lower = "abcdefghijklmnopqrstuvwxyz"
		upper = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
		digit = "0123456789"
		punct = "!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~"
		space = " "
	)

	var b strings.Builder

	if !req.ExcludeLowercase {
		b.WriteString(lower)
	}

	if !req.ExcludeUppercase {
		b.WriteString(upper)
	}

	if !req.ExcludeNumbers {
		b.WriteString(digit)
	}

	if !req.ExcludePunctuation {
		b.WriteString(punct)
	}

	if req.IncludeSpace {
		b.WriteString(space)
	}

	pool := stripExcluded(b.String(), req.ExcludeCharacters)
	if pool == "" {
		return "", cerrors.New(cerrors.InvalidArgument, "no characters available to generate password")
	}

	length := req.PasswordLength
	if length <= 0 {
		length = defaultPasswordLength
	}

	runes := []rune(pool)
	out := make([]rune, 0, length)

	for i := int64(0); i < length; i++ {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(runes))))
		if err != nil {
			return "", cerrors.New(cerrors.Internal, "random source failure")
		}

		out = append(out, runes[n.Int64()])
	}

	return string(out), nil
}

func stripExcluded(pool, exclude string) string {
	if exclude == "" {
		return pool
	}

	var b strings.Builder

	for _, c := range pool {
		if !strings.ContainsRune(exclude, c) {
			b.WriteRune(c)
		}
	}

	return b.String()
}
