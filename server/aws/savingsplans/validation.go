package savingsplans

import (
	"encoding/base64"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// CreateSavingsPlan commitment bounds. The API reference documents commitment as
// "a value between 0.001 and 1 million. You cannot specify more than five digits
// after the decimal point."
// https://docs.aws.amazon.com/savingsplans/latest/APIReference/API_CreateSavingsPlan.html
const (
	minCommitment          = 0.001
	maxCommitment          = 1_000_000
	maxTagKeyLength        = 128
	maxTagValueLength      = 256
	maxDescribePageResults = 1000
	reservedTagPrefix      = "aws:"
)

// commitmentPattern is a plain decimal amount with at most five digits after
// the point. It is matched before parsing so the exponent, hex-float and signed
// forms strconv.ParseFloat also accepts ("1e2", "0x1p3", "+5") are rejected
// rather than stored verbatim.
var commitmentPattern = regexp.MustCompile(`^\d+(\.\d{1,5})?$`)

// validateCommitment enforces the documented CreateSavingsPlan commitment
// constraints: present, a plain decimal with at most five fractional digits,
// and within [0.001, 1000000]. Violations are ValidationException.
func validateCommitment(commitment string) error {
	if commitment == "" {
		return cerrors.New(cerrors.InvalidArgument, "commitment is required")
	}

	if !commitmentPattern.MatchString(commitment) {
		return cerrors.Newf(cerrors.InvalidArgument,
			"commitment must be a decimal amount with at most 5 digits after the decimal point, got %s", commitment)
	}

	v, err := strconv.ParseFloat(commitment, 64)
	if err != nil || v < minCommitment || v > maxCommitment {
		return cerrors.Newf(cerrors.InvalidArgument,
			"commitment must be between 0.001 and 1000000, got %s", commitment)
	}

	return nil
}

// validateTags enforces the AWS-wide tag naming requirements: a tag key is 1-128
// Unicode characters and may not use the reserved aws: prefix (in any case),
// and a value is 0-256 characters.
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

		if strings.HasPrefix(strings.ToLower(k), reservedTagPrefix) {
			return cerrors.Newf(cerrors.InvalidArgument,
				"tag key %q uses the reserved prefix %q", k, reservedTagPrefix)
		}

		if utf8.RuneCountInString(v) > maxTagValueLength {
			return cerrors.Newf(cerrors.InvalidArgument,
				"tag value for key %q exceeds the maximum length of %d characters", k, maxTagValueLength)
		}
	}

	return nil
}

// cursorPrefix marks a nextToken as one this handler issued.
const cursorPrefix = "after:"

// pageRequest is a describe call's paging input: the requested page size, the
// operation's documented minimum (1 for DescribeSavingsPlans, 0 for
// DescribeSavingsPlansOfferings), and whether maxResults was sent at all.
type pageRequest struct {
	maxResults int
	minResults int
	set        bool
	nextToken  string
}

// paginate returns one page of items, which must be sorted ascending by key,
// plus the token for the following page ("" on the last page). The token
// encodes the last returned key rather than an index, so items created or
// removed between calls cannot shift the next page (no skips or repeats).
// An omitted maxResults, or 0 where the operation allows it, returns every
// remaining item. A maxResults outside [minResults, 1000] or a token this
// handler did not issue is a ValidationException.
func paginate[T any](items []T, key func(T) string, pr pageRequest) (page []T, next string, err error) {
	if pr.set && (pr.maxResults < pr.minResults || pr.maxResults > maxDescribePageResults) {
		return nil, "", cerrors.Newf(cerrors.InvalidArgument,
			"maxResults must be between %d and %d, got %d", pr.minResults, maxDescribePageResults, pr.maxResults)
	}

	start := 0

	if pr.nextToken != "" {
		after, ok := decodeCursor(pr.nextToken)
		if !ok {
			return nil, "", cerrors.Newf(cerrors.InvalidArgument, "invalid nextToken: %s", pr.nextToken)
		}

		start = sort.Search(len(items), func(i int) bool { return key(items[i]) > after })
	}

	end := len(items)
	if pr.maxResults > 0 && start+pr.maxResults < end {
		end = start + pr.maxResults
		next = encodeCursor(key(items[end-1]))
	}

	return items[start:end], next, nil
}

// encodeCursor renders a key as an opaque nextToken. Standard base64 fits the
// documented PaginationToken pattern ^[A-Za-z0-9/=\+]+$.
func encodeCursor(key string) string {
	return base64.StdEncoding.EncodeToString([]byte(cursorPrefix + key))
}

func decodeCursor(token string) (string, bool) {
	raw, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		return "", false
	}

	return strings.CutPrefix(string(raw), cursorPrefix)
}
