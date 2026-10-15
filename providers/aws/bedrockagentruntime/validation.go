package bedrockagentruntime

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/pagination"
	"github.com/stackshy/cloudemu/v2/services/bedrockagentruntime/driver"
)

// Paging bounds shared by the runtime List operations.
const (
	minPageResults = 1
	maxPageResults = 1000
)

// violations collects constraint failures and renders them the way AWS does:
// "N validation error(s) detected: <v1>; <v2>".
type violations []string

func (v *violations) addf(format string, args ...any) {
	*v = append(*v, fmt.Sprintf(format, args...))
}

func (v *violations) required(field string, missing bool) {
	if missing {
		v.addf("Value null at '%s' failed to satisfy constraint: Member must not be null", field)
	}
}

func (v *violations) pattern(field, value string, re *regexp.Regexp, text string) {
	if value != "" && !re.MatchString(value) {
		v.addf("Value '%s' at '%s' failed to satisfy constraint: Member must satisfy regular expression pattern: %s",
			value, field, text)
	}
}

// numberOfResults checks a retrieval numberOfResults (1..100) when it is set.
func (v *violations) numberOfResults(field string, n *int32) {
	if n != nil && (*n < 1 || *n > maxNumberOfResults) {
		v.addf("Value '%d' at '%s' failed to satisfy constraint: Member must have value between 1 and %d", *n, field, maxNumberOfResults)
	}
}

func (v *violations) maxLen(field, value string, limit int) {
	if len(value) > limit {
		v.addf("Value '%s' at '%s' failed to satisfy constraint: Member must have length less than or equal to %d",
			value, field, limit)
	}
}

func (v *violations) enum(field, value string, allowed ...string) {
	for _, a := range allowed {
		if a == value {
			return
		}
	}

	if value != "" {
		v.addf("Value '%s' at '%s' failed to satisfy constraint: Member must satisfy enum value set: [%s]",
			value, field, strings.Join(allowed, ", "))
	}
}

// agentID checks a required agentId or agentAliasId member (up to 10
// letters and digits).
func (v *violations) agentID(field, value string) {
	v.required(field, value == "")
	v.pattern(field, value, agentIDRE, agentIDPattern)
	v.maxLen(field, value, maxAgentIDLen)
}

// sessionID checks a required agent sessionId (2..100, [0-9a-zA-Z._:-]+).
func (v *violations) sessionID(value string) {
	const field = "sessionId"

	v.required(field, value == "")

	if value != "" && len(value) < minSessionIDLen {
		v.addf("Value '%s' at '%s' failed to satisfy constraint: Member must have length greater than or equal to %d",
			value, field, minSessionIDLen)
	}

	v.maxLen(field, value, maxSessionIDLen)
	v.pattern(field, value, sessionIDRE, sessionIDPattern)
}

func (v violations) err() error {
	switch len(v) {
	case 0:
		return nil
	case 1:
		return errors.New(errors.InvalidArgument, "1 validation error detected: "+v[0])
	default:
		return errors.Newf(errors.InvalidArgument, "%d validation errors detected: %s", len(v), strings.Join(v, "; "))
	}
}

// paginate slices one page of items (already in a stable order), enforcing the
// 1..1000 bound; an absent maxResults returns up to defaultSize.
func paginate[T any](items []T, page driver.Page, defaultSize int) (pageItems []T, nextToken string, err error) {
	size := defaultSize

	if page.MaxResults != nil {
		n := *page.MaxResults
		if n < minPageResults || n > maxPageResults {
			var v violations

			v.addf("Value '%d' at 'maxResults' failed to satisfy constraint: Member must have value between %d and %d",
				n, minPageResults, maxPageResults)

			return nil, "", v.err()
		}

		size = int(n)
	}

	p, perr := pagination.Paginate(items, page.NextToken, size)
	if perr != nil {
		return nil, "", errors.New(errors.InvalidArgument, "invalid nextToken")
	}

	if p.Items == nil {
		p.Items = []T{}
	}

	return p.Items, p.NextPageToken, nil
}
