package bedrockagentruntime

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/stackshy/cloudemu/v2/errors"
)

// s3Scheme prefixes an S3 URI.
const s3Scheme = "s3://"

// readS3Text reads the text of an S3 object named by an s3:// URI. A missing
// object, or one that isn't UTF-8 text, is a ValidationException, as Bedrock
// rejects an external source it can't read.
func (m *Mock) readS3Text(ctx context.Context, uri string) (string, error) {
	bucket, key, ok := strings.Cut(strings.TrimPrefix(uri, s3Scheme), "/")
	if !strings.HasPrefix(uri, s3Scheme) || !ok || key == "" {
		return "", errors.Newf(errors.InvalidArgument, "%s is not a valid S3 object URI", uri)
	}

	if m.objects == nil {
		return "", errors.Newf(errors.InvalidArgument, "The S3 source %s can't be read: no S3 backend is linked", uri)
	}

	obj, err := m.objects.GetObject(ctx, bucket, key)
	if err != nil {
		return "", errors.Newf(errors.InvalidArgument, "The S3 source %s can't be read: %s", uri, errors.Message(err))
	}

	if !utf8.Valid(obj.Data) {
		return "", errors.Newf(errors.InvalidArgument, "The S3 source %s is not a text document", uri)
	}

	return string(obj.Data), nil
}
