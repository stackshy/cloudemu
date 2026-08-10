package configservice

import (
	"sort"
	"strconv"
	"time"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/configservice/driver"
)

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
		n, err := strconv.Atoi(page.NextToken)
		if err != nil || n < 0 || n > len(items) {
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
		next = strconv.Itoa(end)
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
