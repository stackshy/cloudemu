package savingsplans

import (
	"strconv"
	"strings"
	"unicode/utf8"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
)

// CreateSavingsPlan commitment bounds. The API reference documents commitment as
// "a value between 0.001 and 1 million. You cannot specify more than five digits
// after the decimal point."
// https://docs.aws.amazon.com/savingsplans/latest/APIReference/API_CreateSavingsPlan.html
const (
	minCommitment          = 0.001
	maxCommitment          = 1_000_000
	maxCommitmentDecimals  = 5
	maxTagKeyLength        = 128
	maxTagValueLength      = 256
	maxDescribePageResults = 1000
)

// validateCommitment enforces the documented CreateSavingsPlan commitment
// constraints: present, numeric, within [0.001, 1000000], and no more than five
// digits after the decimal point. Violations are ValidationException.
func validateCommitment(commitment string) error {
	if commitment == "" {
		return cerrors.New(cerrors.InvalidArgument, "commitment is required")
	}

	v, err := strconv.ParseFloat(commitment, 64)
	if err != nil {
		return cerrors.Newf(cerrors.InvalidArgument, "invalid commitment: %s", commitment)
	}

	// Written as a negated in-range check so NaN (which compares false to
	// everything) is rejected too.
	if !(v >= minCommitment && v <= maxCommitment) {
		return cerrors.Newf(cerrors.InvalidArgument,
			"commitment must be between 0.001 and 1000000, got %s", commitment)
	}

	if _, frac, ok := strings.Cut(commitment, "."); ok && len(frac) > maxCommitmentDecimals {
		return cerrors.Newf(cerrors.InvalidArgument,
			"commitment cannot have more than 5 digits after the decimal point, got %s", commitment)
	}

	return nil
}

// validateTags enforces the AWS-wide tag naming requirements: a tag key is 1-128
// Unicode characters and a value is 0-256.
// https://docs.aws.amazon.com/tag-editor/latest/userguide/best-practices-and-strats.html#tag-conventions
func validateTags(tags map[string]string) error {
	for k, v := range tags {
		if k == "" {
			return cerrors.New(cerrors.InvalidArgument, "tag key must not be empty")
		}

		if utf8.RuneCountInString(k) > maxTagKeyLength {
			return cerrors.Newf(cerrors.InvalidArgument,
				"tag key %q exceeds the maximum length of %d characters", k, maxTagKeyLength)
		}

		if utf8.RuneCountInString(v) > maxTagValueLength {
			return cerrors.Newf(cerrors.InvalidArgument,
				"tag value for key %q exceeds the maximum length of %d characters", k, maxTagValueLength)
		}
	}

	return nil
}

// paginate returns one page of items starting at the offset encoded in
// nextToken, plus the token for the following page ("" on the last page).
// maxResults 0 (omitted) returns every remaining item; it must otherwise lie in
// [0, 1000], the documented range for both DescribeSavingsPlans and
// DescribeSavingsPlansOfferings. A token that does not decode to an offset is a
// ValidationException.
func paginate[T any](items []T, maxResults int, nextToken string) (page []T, next string, err error) {
	if maxResults < 0 || maxResults > maxDescribePageResults {
		return nil, "", cerrors.Newf(cerrors.InvalidArgument,
			"maxResults must be between 1 and %d, got %d", maxDescribePageResults, maxResults)
	}

	offset, err := wire.DecodeOffset(nextToken)
	if err != nil {
		return nil, "", cerrors.Newf(cerrors.InvalidArgument, "invalid nextToken: %s", nextToken)
	}

	if offset > len(items) {
		offset = len(items)
	}

	end := len(items)
	if maxResults > 0 && offset+maxResults < end {
		end = offset + maxResults
		next = wire.EncodeOffset(end)
	}

	return items[offset:end], next, nil
}
