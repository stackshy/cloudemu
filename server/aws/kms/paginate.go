package kms

import (
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
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

	return start, end, wire.EncodeOffset(end), true, nil
}

func decodeMarker(marker string) (int, error) {
	n, err := wire.DecodeOffset(marker)
	if err != nil {
		return 0, cerrors.New(cerrors.InvalidArgument, "invalid Marker")
	}

	return n, nil
}
