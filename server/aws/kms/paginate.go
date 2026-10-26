package kms

import (
	"unicode/utf8"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
	kmsdriver "github.com/stackshy/cloudemu/v2/services/kms/driver"
)

// defaultPageLimit is the page size KMS list operations use when Limit is unset.
const defaultPageLimit = 100

// maxMarkerLen is the modeled MarkerType length limit (1-1024).
const maxMarkerLen = 1024

// markerFault says which exception a well-formed Marker that this server did
// not issue produces. Most KMS list operations model InvalidMarkerException;
// ListKeyPolicies does not, so it answers ValidationException.
type markerFault int

const (
	// markerInvalid answers InvalidMarkerException.
	markerInvalid markerFault = iota
	// markerValidation answers ValidationException, for operations whose error
	// list has no InvalidMarkerException.
	markerValidation
)

// pageWindow resolves an opaque Marker + Limit into a [start,end) slice window
// over a stable result set of length total. It returns the NextMarker to hand
// back ("" on the last page) and whether the result was truncated. KMS keys its
// pagination on Marker/Limit and echoes Truncated/NextMarker; an offset-based
// marker is stable because each list op returns a deterministically ordered set.
// fault picks the exception for a Marker that decodes to no position.
func pageWindow(
	marker string, limit int32, total int, fault markerFault,
) (start, end int, nextMarker string, truncated bool, err error) {
	start, err = decodeMarker(marker, fault)
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

// decodeMarker turns a Marker back into an offset. A Marker that breaks the
// modeled MarkerType constraints (length up to 1024, characters U+0020-U+00FF)
// fails request validation with ValidationException on every operation. A
// Marker that passes them but was not issued by this server is
// InvalidMarkerException where the operation models it.
func decodeMarker(marker string, fault markerFault) (int, error) {
	if err := validateMarkerConstraint(marker); err != nil {
		return 0, err
	}

	n, err := wire.DecodeOffset(marker)
	if err == nil {
		return n, nil
	}

	if fault == markerValidation {
		return 0, cerrors.New(cerrors.InvalidArgument, "invalid Marker")
	}

	return 0, kmsdriver.NewAPIError(kmsdriver.ExInvalidMarker, cerrors.InvalidArgument,
		"The request was rejected because the marker that specifies where pagination should next begin is not valid.")
}

// validateMarkerConstraint enforces the MarkerType length and pattern, with
// the message AWS returns for a failed constraint.
func validateMarkerConstraint(marker string) error {
	const prefix = "1 validation error detected: Value '%s' at 'marker' failed to satisfy constraint: "

	if utf8.RuneCountInString(marker) > maxMarkerLen {
		return cerrors.Newf(cerrors.InvalidArgument,
			prefix+"Member must have length less than or equal to %d", marker, maxMarkerLen)
	}

	for _, r := range marker {
		if r < 0x20 || r > 0xFF {
			return cerrors.Newf(cerrors.InvalidArgument,
				prefix+"Member must satisfy regular expression pattern: [\\u0020-\\u00FF]*", marker)
		}
	}

	return nil
}
