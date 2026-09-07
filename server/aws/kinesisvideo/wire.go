package kinesisvideo

import (
	"time"

	"github.com/stackshy/cloudemu/v2/services/kinesisvideo/driver"
)

// epochOrNil renders a time as a Unix-epoch-seconds float the Kinesis Video SDK
// decodes into a *time.Time, or nil for the zero time.
func epochOrNil(t time.Time) *float64 {
	if t.IsZero() {
		return nil
	}

	secs := float64(t.Unix())

	return &secs
}

// putNonEmpty sets key to val only when val is non-empty, so an optional field
// the caller never set is omitted from the wire object (read back as null).
func putNonEmpty(m map[string]any, key, val string) {
	if val != "" {
		m[key] = val
	}
}

// streamInfoToWire renders a stream as its StreamInfo restJson1 object.
func streamInfoToWire(s *driver.StreamInfo) map[string]any {
	out := map[string]any{
		"StreamName":           s.StreamName,
		"StreamARN":            s.StreamARN,
		"DataRetentionInHours": s.DataRetentionInHours,
		"Status":               s.Status,
		"Version":              s.Version,
		"CreationTime":         epochOrNil(s.CreationTime),
	}

	putNonEmpty(out, "MediaType", s.MediaType)
	putNonEmpty(out, "KmsKeyId", s.KmsKeyID)
	putNonEmpty(out, "DeviceName", s.DeviceName)

	return out
}

// channelInfoToWire renders a channel as its ChannelInfo restJson1 object.
func channelInfoToWire(c *driver.ChannelInfo) map[string]any {
	return map[string]any{
		"ChannelName":   c.ChannelName,
		"ChannelARN":    c.ChannelARN,
		"ChannelType":   c.ChannelType,
		"ChannelStatus": c.ChannelStatus,
		"Version":       c.Version,
		"CreationTime":  epochOrNil(c.CreationTime),
		"SingleMasterConfiguration": map[string]any{
			"MessageTtlSeconds": c.MessageTTLSeconds,
		},
	}
}
