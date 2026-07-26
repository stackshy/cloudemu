package ssm

import (
	"encoding/base64"
	"strconv"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// AWS MaxResults ceilings (and defaults when unset) for the paginated SSM
// operations, matching the real service limits.
const (
	maxResultsByPath   = 10 // GetParametersByPath: 1..10
	maxResultsDescribe = 50 // DescribeParameters:  1..50
)

// pageWindow resolves an opaque NextToken + MaxResults into a [start,end) slice
// window over a stable, fully-sorted result set of length total, and the token
// to return for the next page ("" once the last page is reached). The driver
// already returns results sorted by name, so an offset-based token is stable.
// A malformed token is a validation error the caller maps to the wire response.
func pageWindow(token string, maxResults int32, defaultMax, total int) (start, end int, next string, err error) {
	start, err = decodePageToken(token)
	if err != nil {
		return 0, 0, "", err
	}

	if start > total {
		start = total
	}

	limit := int(maxResults)
	if limit <= 0 || limit > defaultMax {
		limit = defaultMax
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
