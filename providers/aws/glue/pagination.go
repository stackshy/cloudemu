package glue

import (
	"encoding/base64"
	"sort"
	"strconv"

	"github.com/stackshy/cloudemu/v2/services/glue/driver"
)

// defaultPageSize is the page size Glue applies when MaxResults is unset.
const defaultPageSize = 100

// sortedKeys returns keys in deterministic order so list endpoints and
// pagination are stable across calls (map iteration order is random).
func sortedKeys(keys []string) []string {
	sort.Strings(keys)

	return keys
}

// encodeToken encodes an integer offset as an opaque NextToken.
func encodeToken(offset int) string {
	return base64.StdEncoding.EncodeToString([]byte(strconv.Itoa(offset)))
}

// decodeToken parses a NextToken into an offset. A malformed token is rejected
// with an InvalidInputException rather than silently restarting at page one.
func decodeToken(token string) (int, error) {
	if token == "" {
		return 0, nil
	}

	raw, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		return 0, invalidInput("invalid pagination token")
	}

	offset, err := strconv.Atoi(string(raw))
	if err != nil || offset < 0 {
		return 0, invalidInput("invalid pagination token")
	}

	return offset, nil
}

// paginate slices items per the page's NextToken/MaxResults and returns the
// page plus the next token ("" when the last page is returned). A bad token is
// an InvalidInputException.
//
//nolint:gocritic // unnamedResult: (page, nextToken, err) is idiomatic and self-explanatory
func paginate[T any](items []T, page driver.TablePagination) ([]T, string, error) {
	offset, err := decodeToken(page.NextToken)
	if err != nil {
		return nil, "", err
	}

	size := int(page.MaxResults)
	if size <= 0 {
		size = defaultPageSize
	}

	if offset >= len(items) {
		return []T{}, "", nil
	}

	end := offset + size

	next := ""
	if end < len(items) {
		next = encodeToken(end)
	} else {
		end = len(items)
	}

	return items[offset:end], next, nil
}
