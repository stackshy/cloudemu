package keyspaces

import (
	"context"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	ksdriver "github.com/stackshy/cloudemu/v2/services/keyspaces/driver"
)

// setTags merges tags onto a resource ARN. The caller holds the write lock.
func (m *Mock) setTags(arn string, tags map[string]string) {
	if len(tags) == 0 {
		return
	}

	cur := m.tags[arn]
	if cur == nil {
		cur = make(map[string]string, len(tags))
	}

	for k, v := range tags {
		cur[k] = v
	}

	m.tags[arn] = cur
}

// resourceARNPattern is the documented resourceArn pattern for the tagging
// operations (arn:(aws[a-zA-Z0-9-]*):cassandra:.+.*, length 20-1000).
var resourceARNPattern = regexp.MustCompile(`^arn:aws[a-zA-Z0-9-]*:cassandra:.+`)

// tagCharsPattern is the documented tag key/value character set: letters,
// white space, numbers, and the characters + - = . _ : and /.
var tagCharsPattern = regexp.MustCompile(`^[\p{L}\p{Z}\p{N}+\-=._:/]+$`)

const (
	minResourceARNLen = 20
	maxResourceARNLen = 1000
	maxTagKeyLen      = 128
	maxTagValueLen    = 256
	// maxTagListLen is the documented TagList maximum (Array Members: Maximum
	// number of 60 items) on TagResource, UntagResource, CreateKeyspace,
	// CreateTable and RestoreTable.
	maxTagListLen     = 60
	reservedTagPrefix = "aws:"
	arnResourceMarker = ":/keyspace/"
	arnTableSegment   = "table"
)

// validateTags applies the documented Amazon Keyspaces tag restrictions: key
// 1-128 and value 1-256 Unicode characters (a value can't be empty), only
// letters, white space, numbers and + - = . _ : /, and no user-assigned keys
// with the reserved "aws:" prefix. A request carries at most 60 tags.
func validateTags(tags map[string]string) error {
	if len(tags) > maxTagListLen {
		return cerrors.Newf(cerrors.InvalidArgument, "tags must contain at most %d items", maxTagListLen)
	}

	for k, v := range tags {
		if n := utf8.RuneCountInString(k); n == 0 || n > maxTagKeyLen {
			return cerrors.Newf(cerrors.InvalidArgument, "tag key %q must be 1-%d characters", k, maxTagKeyLen)
		}

		if n := utf8.RuneCountInString(v); n == 0 || n > maxTagValueLen {
			return cerrors.Newf(cerrors.InvalidArgument,
				"tag value for key %q must be 1-%d characters", k, maxTagValueLen)
		}

		if strings.HasPrefix(strings.ToLower(k), reservedTagPrefix) {
			return cerrors.Newf(cerrors.InvalidArgument, "tag key %q uses the reserved prefix %q", k, reservedTagPrefix)
		}

		if !tagCharsPattern.MatchString(k) || !tagCharsPattern.MatchString(v) {
			return cerrors.Newf(cerrors.InvalidArgument,
				"tag %q contains characters outside letters, numbers, white space and + - = . _ : /", k)
		}
	}

	return nil
}

// resolveTaggableLocked validates a resource ARN and returns the canonical ARN
// of the existing keyspace or table it names. Real Keyspaces rejects a
// malformed ARN with ValidationException and an unknown resource with
// ResourceNotFoundException. The trailing "/" is optional, so both the form
// this emulator returns and the slash-less table form in the AWS docs resolve
// to the same resource. The caller holds the lock.
func (m *Mock) resolveTaggableLocked(arn string) (string, error) {
	if err := validateResourceARN(arn); err != nil {
		return "", err
	}

	canonical := m.existingResourceARN(arn)

	// The region/account (and partition) must match this emulator's too.
	if canonical == "" || (arn != canonical && arn+"/" != canonical) {
		return "", cerrors.Newf(cerrors.NotFound, "resource %q not found", arn)
	}

	return canonical, nil
}

// validateResourceARN checks the documented resourceArn length and pattern.
func validateResourceARN(arn string) error {
	if arn == "" {
		return cerrors.New(cerrors.InvalidArgument, "resourceArn is required")
	}

	if len(arn) < minResourceARNLen || len(arn) > maxResourceARNLen || !resourceARNPattern.MatchString(arn) {
		return cerrors.Newf(cerrors.InvalidArgument, "resourceArn %q is not a valid Amazon Keyspaces ARN", arn)
	}

	return nil
}

// existingResourceARN returns the canonical ARN of the keyspace
// (".../keyspace/<ks>/") or table (".../keyspace/<ks>/table/<t>/") that arn's
// resource path names, or "" when the path names no existing keyspace or
// table. The caller holds the lock.
func (m *Mock) existingResourceARN(arn string) string {
	idx := strings.Index(arn, arnResourceMarker)
	if idx < 0 {
		return ""
	}

	parts := strings.Split(strings.TrimSuffix(arn[idx+len(arnResourceMarker):], "/"), "/")

	switch {
	case len(parts) == 1 && m.keyspaces.Has(parts[0]):
		return m.keyspaceARN(parts[0])
	case len(parts) == 3 && parts[1] == arnTableSegment && m.tables.Has(tableKey(parts[0], parts[2])):
		return m.tableARN(parts[0], parts[2])
	default:
		return ""
	}
}

// TagResource adds tags to an existing keyspace or table by ARN.
func (m *Mock) TagResource(_ context.Context, arn string, tags map[string]string) error {
	if len(tags) == 0 {
		return cerrors.New(cerrors.InvalidArgument, "tags must contain at least one tag")
	}

	if err := validateTags(tags); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	canonical, err := m.resolveTaggableLocked(arn)
	if err != nil {
		return err
	}

	m.setTags(canonical, tags)

	return nil
}

// UntagResource removes tag keys from an existing keyspace or table by ARN.
func (m *Mock) UntagResource(_ context.Context, arn string, keys []string) error {
	if len(keys) == 0 {
		return cerrors.New(cerrors.InvalidArgument, "tags must contain at least one tag")
	}

	if len(keys) > maxTagListLen {
		return cerrors.Newf(cerrors.InvalidArgument, "tags must contain at most %d items", maxTagListLen)
	}

	for _, k := range keys {
		if n := utf8.RuneCountInString(k); n == 0 || n > maxTagKeyLen {
			return cerrors.Newf(cerrors.InvalidArgument, "tag key %q must be 1-%d characters", k, maxTagKeyLen)
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	canonical, err := m.resolveTaggableLocked(arn)
	if err != nil {
		return err
	}

	cur := m.tags[canonical]
	for _, k := range keys {
		delete(cur, k)
	}

	return nil
}

// ListTagsForResource returns an existing resource's tags in deterministic
// (sorted) order (AWS leaves tag order unspecified).
func (m *Mock) ListTagsForResource(_ context.Context, arn string) ([]ksdriver.Tag, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	canonical, err := m.resolveTaggableLocked(arn)
	if err != nil {
		return nil, err
	}

	tags := m.tags[canonical]

	names := make([]string, 0, len(tags))
	for k := range tags {
		names = append(names, k)
	}

	sort.Strings(names)

	out := make([]ksdriver.Tag, 0, len(names))
	for _, k := range names {
		out = append(out, ksdriver.Tag{Key: k, Value: tags[k]})
	}

	return out, nil
}
