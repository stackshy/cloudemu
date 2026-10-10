package s3

import (
	"context"
	"strings"
	"unicode/utf16"

	"github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// S3 tag limits. Object tag sets hold at most 10 tags; keys are at most 128
// and values at most 256 Unicode characters, counted in UTF-16 code units as
// S3 stores tags internally. Keys starting with aws: are reserved for system
// tags.
const (
	maxObjectTags     = 10
	maxTagKeyLength   = 128
	maxTagValueLength = 256
	reservedTagPrefix = "aws:"
)

// S3 error codes for a rejected tag set.
const (
	tagErrInvalidTag = "InvalidTag"
	tagErrBadRequest = "BadRequest"
)

var (
	_ driver.ObjectTagValidator = (*Mock)(nil)
	_ driver.BucketCreator      = (*Mock)(nil)
)

// ValidateObjectTags implements driver.ObjectTagValidator: it applies the S3
// object tag rules to tags and returns a *driver.TagError on the first
// violation. A nil or empty set is valid.
func (*Mock) ValidateObjectTags(tags map[string]string) error {
	return validateObjectTags(tags)
}

// validateObjectTags checks an object tag set: more than 10 tags is
// BadRequest; an aws: key prefix or an over-long key or value is InvalidTag.
func validateObjectTags(tags map[string]string) error {
	if len(tags) > maxObjectTags {
		return &driver.TagError{Code: tagErrBadRequest, Message: "Object tags cannot be greater than 10"}
	}

	for k, v := range tags {
		if strings.HasPrefix(k, reservedTagPrefix) {
			return &driver.TagError{Code: tagErrInvalidTag, Message: "Your TagKey cannot be prefixed with aws:"}
		}

		if err := checkTagLengths(k, v); err != nil {
			return err
		}
	}

	return nil
}

// validateCreateBucketTags checks the tag set sent in a CreateBucket
// configuration: no aws: key prefix and the key/value length limits.
func validateCreateBucketTags(tags map[string]string) error {
	for k, v := range tags {
		if strings.HasPrefix(k, reservedTagPrefix) {
			return &driver.TagError{
				Code: tagErrInvalidTag,
				Message: `User-defined tag keys can't start with "aws:". This prefix is reserved for system tags. ` +
					`Remove "aws:" from your tag keys and try again.`,
			}
		}

		if err := checkTagLengths(k, v); err != nil {
			return err
		}
	}

	return nil
}

// checkTagLengths enforces the 128 (key) / 256 (value) UTF-16 length limits.
func checkTagLengths(k, v string) error {
	if utf16Len(k) > maxTagKeyLength {
		return &driver.TagError{Code: tagErrInvalidTag, Message: "The TagKey you have provided is too long, max 128"}
	}

	if utf16Len(v) > maxTagValueLength {
		return &driver.TagError{Code: tagErrInvalidTag, Message: "The TagValue you have provided is too long, max 256"}
	}

	return nil
}

// utf16Len is the length of s in UTF-16 code units.
func utf16Len(s string) int {
	return len(utf16.Encode([]rune(s)))
}

// CreateBucketWithOptions implements driver.BucketCreator: it validates the
// CreateBucketConfiguration tag set, creates the bucket in opts.Region, and
// stores the tags, so an invalid tag set never leaves a bucket behind.
func (m *Mock) CreateBucketWithOptions(ctx context.Context, name string, opts driver.CreateBucketOptions) error {
	if err := validateCreateBucketTags(opts.Tags); err != nil {
		return err
	}

	if err := m.CreateBucketInRegion(ctx, name, opts.Region); err != nil {
		return err
	}

	if len(opts.Tags) == 0 {
		return nil
	}

	return m.PutBucketTagging(ctx, name, opts.Tags)
}
