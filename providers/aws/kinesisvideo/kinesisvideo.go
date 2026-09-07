// Package kinesisvideo provides an in-memory mock implementation of the Amazon
// Kinesis Video Streams control plane: video streams and signaling channels
// plus their resource tags. A stream and a signaling channel are created
// immediately in the ACTIVE state with a stable StreamARN/ChannelARN (which
// embeds the creation Unix timestamp), Version token, Status and CreationTime;
// an update rotates the Version token while every other computed field stays
// stable, so repeated reads and IaC plans never drift. Ingesting or serving
// video (the GetMedia/PutMedia data plane) is out of scope: this is a
// control-plane-only surface.
//
// Distinct from the kinesis package (Kinesis Data Streams), which is a separate
// service and is not touched here.
package kinesisvideo

import (
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/kinesisvideo/driver"
)

// Compile-time check that Mock implements driver.KinesisVideo.
var _ driver.KinesisVideo = (*Mock)(nil)

const (
	// defaultMaxResults caps a page when the caller requests none.
	defaultMaxResults = 100

	// versionBytes is the number of random bytes rendered into a Version token
	// (16 hex characters), matching real Kinesis Video's opaque version string.
	versionBytes = 8

	// resourceStream and resourceChannel are the ARN resource-type prefixes.
	resourceStream  = "stream"
	resourceChannel = "channel"

	// arnResourceParts is the segment count of a Kinesis Video ARN resource part
	// (<type>/<name>/<creation-unix>).
	arnResourceParts = 3
)

// Mock is an in-memory implementation of the Amazon Kinesis Video Streams
// control plane.
type Mock struct {
	streams  *memstore.Store[driver.StreamInfo]
	channels *memstore.Store[driver.ChannelInfo]
	opts     *config.Options
}

// New creates a new Kinesis Video mock with the given configuration options.
func New(opts *config.Options) *Mock {
	return &Mock{
		streams:  memstore.New[driver.StreamInfo](),
		channels: memstore.New[driver.ChannelInfo](),
		opts:     opts,
	}
}

func (m *Mock) now() time.Time {
	return m.opts.Clock.Now().UTC()
}

// newVersion mints a fresh opaque version token. It is rotated on every mutation
// so a client can detect concurrent changes, matching real Kinesis Video.
func newVersion() string {
	b := make([]byte, versionBytes)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand.Read never fails on supported platforms; fall back to a
		// fixed token rather than panicking in a control-plane emulator.
		return "0000000000000000"
	}

	return hex.EncodeToString(b)
}

// streamARN mints the stable ARN for a stream. The resource part embeds the
// creation Unix timestamp (arn:aws:kinesisvideo:<region>:<acct>:stream/<name>/<unix>),
// matching the real Kinesis Video ARN shape, and is minted once and stored.
func (m *Mock) streamARN(name string, created time.Time) string {
	res := resourceStream + "/" + name + "/" + strconv.FormatInt(created.Unix(), 10)

	return idgen.AWSARN("kinesisvideo", m.opts.Region, m.opts.AccountID, res)
}

// channelARN mints the stable ARN for a signaling channel.
func (m *Mock) channelARN(name string, created time.Time) string {
	res := resourceChannel + "/" + name + "/" + strconv.FormatInt(created.Unix(), 10)

	return idgen.AWSARN("kinesisvideo", m.opts.Region, m.opts.AccountID, res)
}

// nameFromARN extracts the resource name from a Kinesis Video ARN of the form
// arn:...:kinesisvideo:...:<type>/<name>/<unix>. It returns the name and the
// resource type, or ok=false when the ARN is not a well-formed Kinesis Video
// resource ARN.
func nameFromARN(arn string) (name, resType string, ok bool) {
	// The resource part follows the sixth colon-delimited field.
	const arnFields = 6

	parts := strings.SplitN(arn, ":", arnFields)
	if len(parts) < arnFields {
		return "", "", false
	}

	seg := strings.Split(parts[arnFields-1], "/")
	if len(seg) != arnResourceParts {
		return "", "", false
	}

	if seg[0] != resourceStream && seg[0] != resourceChannel {
		return "", "", false
	}

	return seg[1], seg[0], true
}

func copyTags(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}

	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}

	return out
}

// copyStream returns an alias-free copy so callers cannot mutate stored state
// through the result.
func copyStream(s *driver.StreamInfo) driver.StreamInfo {
	out := *s
	out.Tags = copyTags(s.Tags)

	return out
}

// copyChannel returns an alias-free copy of a channel.
func copyChannel(c *driver.ChannelInfo) driver.ChannelInfo {
	out := *c
	out.Tags = copyTags(c.Tags)

	return out
}

// paginate returns the offset window and next token for a slice of length n,
// honoring an opaque numeric offset token.
func paginate(n int, page driver.Page) (start, end int, next string) {
	start = decodeToken(page.NextToken)
	if start > n {
		start = n
	}

	limit := int(page.MaxResults)
	if limit <= 0 {
		limit = defaultMaxResults
	}

	end = start + limit
	if end >= n {
		return start, n, ""
	}

	return start, end, encodeToken(end)
}

// encodeToken encodes a numeric list offset as an opaque pagination token.
func encodeToken(offset int) string {
	return strconv.Itoa(offset)
}

// decodeToken decodes an opaque pagination token to a numeric offset. An empty
// or malformed token decodes to 0 (start from the beginning).
func decodeToken(token string) int {
	if token == "" {
		return 0
	}

	n, err := strconv.Atoi(token)
	if err != nil || n < 0 {
		return 0
	}

	return n
}

// tagMapFromList folds a Tag list (used by the resource-level tagging
// operations) into the string-to-string map the store holds.
func tagMapFromList(list []driver.Tag) map[string]string {
	if list == nil {
		return nil
	}

	out := make(map[string]string, len(list))
	for _, t := range list {
		out[t.Key] = t.Value
	}

	return out
}
