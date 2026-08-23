package kms

import (
	"encoding/base64"
	"strconv"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// defaultPageLimit is the page size KMS list operations use when Limit is unset.
const defaultPageLimit = 100

// pageWindow resolves an opaque Marker + Limit into a [start,end) slice window
// over a stable result set of length total. It returns the NextMarker to hand
// back ("" on the last page) and whether the result was truncated. KMS keys its
// pagination on Marker/Limit and echoes Truncated/NextMarker; an offset-based
// marker is stable because each list op returns a deterministically ordered set.
func pageWindow(marker string, limit int32, total int) (start, end int, nextMarker string, truncated bool, err error) {
	start, err = decodeMarker(marker)
	if err != nil {
		return 0, 0, "", false, err
	}

	if start > total {
		start = total
	}

	size := int(limit)
	if size <= 0 {
		size = defaultPageLimit
	}

	end = start + size
	if end >= total {
		return start, total, "", false, nil
	}

	return start, end, encodeMarker(end), true, nil
}

func encodeMarker(offset int) string {
	return base64.StdEncoding.EncodeToString([]byte(strconv.Itoa(offset)))
}

func decodeMarker(marker string) (int, error) {
	if marker == "" {
		return 0, nil
	}

	b, err := base64.StdEncoding.DecodeString(marker)
	if err != nil {
		return 0, cerrors.New(cerrors.InvalidArgument, "invalid Marker")
	}

	n, err := strconv.Atoi(string(b))
	if err != nil || n < 0 {
		return 0, cerrors.New(cerrors.InvalidArgument, "invalid Marker")
	}

	return n, nil
}
