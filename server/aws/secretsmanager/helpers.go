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

// maxPasswordLength is GetRandomPassword's documented maximum PasswordLength.
// A larger request is a ValidationException in the real service.
const maxPasswordLength = 4096

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
// name, description, tag-key, tag-value, and all. Matching mirrors the real
// service: name/tag-key/tag-value are case-sensitive prefix matches, description
// is a case-insensitive prefix match, and all tokenizes the value into words and
// searches every attribute case-insensitively.
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
		if secretFieldMatches(info, f.Key, v) {
			return true
		}
	}

	return false
}

func secretFieldMatches(info *secretsdriver.SecretInfo, key, value string) bool {
	switch key {
	case "name":
		return strings.HasPrefix(info.Name, value)
	case "description":
		return hasPrefixFold(info.Description, value)
	case "tag-key":
		return anyTagHasPrefix(info.Tags, value, true)
	case "tag-value":
		return anyTagHasPrefix(info.Tags, value, false)
	case "all":
		return matchesAllWords(info, value)
	default:
		// Unsupported key: do not exclude on it.
		return true
	}
}

// hasPrefixFold reports whether s begins with prefix, ignoring case.
func hasPrefixFold(s, prefix string) bool {
	return strings.HasPrefix(strings.ToLower(s), strings.ToLower(prefix))
}

// anyTagHasPrefix reports whether any tag key (matchKey=true) or value begins
// with value (case-sensitive prefix, as the real service does).
func anyTagHasPrefix(tags map[string]string, value string, matchKey bool) bool {
	for k, v := range tags {
		field := v
		if matchKey {
			field = k
		}

		if strings.HasPrefix(field, value) {
			return true
		}
	}

	return false
}

// matchesAllWords implements the 'all' filter key: the value is split into
// words and the secret matches only when every word appears (case-insensitive
// substring) in the name, description, or any tag key/value.
func matchesAllWords(info *secretsdriver.SecretInfo, value string) bool {
	words := strings.Fields(value)
	for _, w := range words {
		if !anyAttrContainsFold(info, w) {
			return false
		}
	}

	return true
}

func anyAttrContainsFold(info *secretsdriver.SecretInfo, word string) bool {
	lw := strings.ToLower(word)
	if strings.Contains(strings.ToLower(info.Name), lw) ||
		strings.Contains(strings.ToLower(info.Description), lw) {
		return true
	}

	for k, v := range info.Tags {
		if strings.Contains(strings.ToLower(k), lw) || strings.Contains(strings.ToLower(v), lw) {
			return true
		}
	}

	return false
}

// passwordClasses returns the included character classes for a
// GetRandomPassword request, each already stripped of ExcludeCharacters, in the
// order lower, upper, digit, punctuation, space. Empty classes are omitted.
func passwordClasses(req getRandomPasswordRequest) []string {
	const (
		lower = "abcdefghijklmnopqrstuvwxyz"
		upper = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
		digit = "0123456789"
		punct = "!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~"
		space = " "
	)

	candidates := []struct {
		set     string
		exclude bool
	}{
		{lower, req.ExcludeLowercase},
		{upper, req.ExcludeUppercase},
		{digit, req.ExcludeNumbers},
		{punct, req.ExcludePunctuation},
		{space, !req.IncludeSpace},
	}

	var classes []string

	for _, c := range candidates {
		if c.exclude {
			continue
		}

		if s := stripExcluded(c.set, req.ExcludeCharacters); s != "" {
			classes = append(classes, s)
		}
	}

	return classes
}

// randomPassword generates a password honoring GetRandomPassword's character-set
// toggles. When RequireEachIncludedType is set the result is guaranteed to carry
// at least one character from every included class, and a length shorter than
// the number of included classes is rejected.
func randomPassword(req getRandomPasswordRequest) (string, error) {
	classes := passwordClasses(req)
	if len(classes) == 0 {
		return "", cerrors.New(cerrors.InvalidArgument, "no characters available to generate password")
	}

	length := req.PasswordLength
	if length <= 0 {
		length = defaultPasswordLength
	}

	// Real GetRandomPassword caps PasswordLength at 4096 (ValidationException
	// beyond it); enforce the same bound so an oversized length can't drive an
	// unbounded allocation.
	if length > maxPasswordLength {
		return "", cerrors.Newf(cerrors.InvalidArgument,
			"PasswordLength must be between 1 and %d", maxPasswordLength)
	}

	if req.RequireEachIncludedType && length < int64(len(classes)) {
		return "", cerrors.New(cerrors.InvalidArgument,
			"PasswordLength too short to include one of each required character type")
	}

	out, err := buildPassword(classes, length, req.RequireEachIncludedType)
	if err != nil {
		return "", err
	}

	if err := shuffleRunes(out); err != nil {
		return "", err
	}

	return string(out), nil
}

// buildPassword assembles the character slice: when requireEach is set it seeds
// one guaranteed rune from each class, then fills the remainder from the pooled
// classes. The result is unshuffled.
func buildPassword(classes []string, length int64, requireEach bool) ([]rune, error) {
	pool := []rune(strings.Join(classes, ""))
	out := make([]rune, 0, length)

	if requireEach {
		for _, class := range classes {
			c, err := pickRune([]rune(class))
			if err != nil {
				return nil, err
			}

			out = append(out, c)
		}
	}

	for int64(len(out)) < length {
		c, err := pickRune(pool)
		if err != nil {
			return nil, err
		}

		out = append(out, c)
	}

	return out, nil
}

// pickRune returns a uniformly random rune from runes.
func pickRune(runes []rune) (rune, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(runes))))
	if err != nil {
		return 0, cerrors.New(cerrors.Internal, "random source failure")
	}

	return runes[n.Int64()], nil
}

// shuffleRunes performs a Fisher-Yates shuffle so the guaranteed seed characters
// aren't clustered at the front.
func shuffleRunes(r []rune) error {
	for i := len(r) - 1; i > 0; i-- {
		j, err := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
		if err != nil {
			return cerrors.New(cerrors.Internal, "random source failure")
		}

		k := j.Int64()
		r[i], r[k] = r[k], r[i]
	}

	return nil
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
