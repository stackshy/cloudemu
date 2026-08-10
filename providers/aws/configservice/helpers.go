package configservice

import (
	"encoding/base64"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/configservice/driver"
)

// nextTokenPrefix namespaces the opaque pagination token so a token minted for
// one op isn't mistaken for a raw offset.
const nextTokenPrefix = "cfg:"

// encodeNextToken renders a page offset as an opaque base64 token.
func encodeNextToken(offset int) string {
	return base64.StdEncoding.EncodeToString([]byte(nextTokenPrefix + strconv.Itoa(offset)))
}

// decodeNextToken parses an opaque base64 token back to its page offset. A
// malformed token returns ok=false.
func decodeNextToken(token string) (offset int, ok bool) {
	raw, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		return 0, false
	}

	s := string(raw)
	if !strings.HasPrefix(s, nextTokenPrefix) {
		return 0, false
	}

	n, err := strconv.Atoi(strings.TrimPrefix(s, nextTokenPrefix))
	if err != nil || n < 0 {
		return 0, false
	}

	return n, true
}

func (m *Mock) now() time.Time {
	return m.opts.Clock.Now().UTC()
}

// arn builds a real AWS Config ARN for the given resource path
// (arn:aws:config:<region>:<account>:<resource>).
func (m *Mock) arn(resource string) string {
	return idgen.AWSARN("config", m.opts.Region, m.opts.AccountID, resource)
}

func copyTags(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}

	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}

	return out
}

func copyStrings(in []string) []string {
	if in == nil {
		return nil
	}

	return append([]string(nil), in...)
}

// mergeTags merges add into existing (copy-on-write) enforcing the tag cap.
// Returns the new map and an error if the cap would be exceeded.
func mergeTags(existing, add map[string]string) (map[string]string, error) {
	out := copyTags(existing)
	if out == nil {
		out = map[string]string{}
	}

	for k, v := range add {
		out[k] = v
	}

	if len(out) > maxTags {
		return nil, tooManyTags(maxTags)
	}

	return out, nil
}

// paginate applies an opaque offset NextToken and Limit to a slice of already
// deterministically-ordered items, returning the page plus the next token.
// A malformed token is an InvalidNextTokenException rather than a silent page-1.
func paginate[T any](items []T, page driver.Page) (pageItems []T, nextToken string, err error) {
	offset := 0

	if page.NextToken != "" {
		n, ok := decodeNextToken(page.NextToken)
		if !ok || n > len(items) {
			return nil, "", invalidNextToken(page.NextToken)
		}

		offset = n
	}

	limit := int(page.Limit)
	if limit <= 0 || limit > maxPageLim {
		limit = defaultPageLim
	}

	end := offset + limit
	if end > len(items) {
		end = len(items)
	}

	next := ""
	if end < len(items) {
		next = encodeNextToken(end)
	}

	return items[offset:end], next, nil
}

// filterByNames returns items whose name is in want; empty want returns all.
func filterByNames[T any](items []T, name func(T) string, want []string) []T {
	if len(want) == 0 {
		return items
	}

	set := make(map[string]bool, len(want))
	for _, w := range want {
		set[w] = true
	}

	out := make([]T, 0, len(items))

	for _, it := range items {
		if set[name(it)] {
			out = append(out, it)
		}
	}

	return out
}

// sortedKeys returns a store's keys in deterministic order.
func sortedKeys(keys []string) []string {
	sort.Strings(keys)
	return keys
}

func resourceKey(resourceType, resourceID string) string {
	return resourceType + "/" + resourceID
}
