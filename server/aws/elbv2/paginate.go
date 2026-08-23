package elbv2

import (
	"encoding/base64"
	"strconv"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// defaultPageSize is the page size ELBv2 describe operations use when PageSize
// is unset; it is also the AWS-documented maximum.
const defaultPageSize = 400

// pageWindow resolves an opaque Marker + PageSize into a [start,end) slice over
// a stable result set of length total. It returns the NextMarker to echo ("" on
// the last page). ELBv2 keys describe pagination on Marker/PageSize; an
// offset-based marker is stable because each describe returns a
// deterministically ordered set.
func pageWindow(marker string, pageSize, total int) (start, end int, nextMarker string, err error) {
	start, err = decodeMarker(marker)
	if err != nil {
		return 0, 0, "", err
	}

	if start > total {
		start = total
	}

	size := pageSize
	if size <= 0 {
		size = defaultPageSize
	}

	end = start + size
	if end >= total {
		return start, total, "", nil
	}

	return start, end, encodeMarker(end), nil
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
