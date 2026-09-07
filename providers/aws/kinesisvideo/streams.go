package kinesisvideo

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/kinesisvideo/driver"
)

// CreateStream provisions a new stream directly in the ACTIVE state with stable
// computed fields (StreamARN embedding the creation Unix timestamp, Version,
// Status, CreationTime). A duplicate name yields ResourceInUseException.
func (m *Mock) CreateStream(_ context.Context, in *driver.CreateStreamInput) (*driver.StreamInfo, error) {
	if in.StreamName == "" {
		return nil, invalid("StreamName is required")
	}

	if m.streams.Has(in.StreamName) {
		return nil, inUse("stream", in.StreamName)
	}

	now := m.now()

	var retention int32
	if in.DataRetentionInHours != nil {
		retention = *in.DataRetentionInHours
	}

	s := driver.StreamInfo{
		StreamName:           in.StreamName,
		StreamARN:            m.streamARN(in.StreamName, now),
		MediaType:            in.MediaType,
		KmsKeyID:             in.KmsKeyID,
		DeviceName:           in.DeviceName,
		DataRetentionInHours: retention,
		Version:              newVersion(),
		Status:               driver.StatusActive,
		CreationTime:         now,
		Tags:                 copyTags(in.Tags),
	}

	m.streams.Set(in.StreamName, s)

	out := copyStream(&s)

	return &out, nil
}

// DescribeStream returns a copy of the stream. Stored computed fields are
// returned unchanged so repeated reads never drift.
func (m *Mock) DescribeStream(_ context.Context, ref driver.StreamRef) (*driver.StreamInfo, error) {
	name, err := m.resolveStreamName(ref)
	if err != nil {
		return nil, err
	}

	s, ok := m.streams.Get(name)
	if !ok {
		return nil, notFound("stream", name)
	}

	out := copyStream(&s)

	return &out, nil
}

// UpdateStream applies DeviceName/MediaType changes and rotates the Version
// token. The computed StreamARN, Status and CreationTime are preserved.
func (m *Mock) UpdateStream(_ context.Context, in *driver.UpdateStreamInput) error {
	name, err := m.resolveStreamName(driver.StreamRef{StreamName: in.StreamName, StreamARN: in.StreamARN})
	if err != nil {
		return err
	}

	return m.mutateStream(name, in.CurrentVersion, func(s *driver.StreamInfo) {
		if in.DeviceName != "" {
			s.DeviceName = in.DeviceName
		}

		if in.MediaType != "" {
			s.MediaType = in.MediaType
		}
	})
}

// UpdateDataRetention increases or decreases the retention window and rotates
// the Version token.
func (m *Mock) UpdateDataRetention(_ context.Context, in *driver.UpdateDataRetentionInput) error {
	name, err := m.resolveStreamName(driver.StreamRef{StreamName: in.StreamName, StreamARN: in.StreamARN})
	if err != nil {
		return err
	}

	switch in.Operation {
	case driver.OperationIncreaseDataRetention, driver.OperationDecreaseDataRetention:
	default:
		return invalid("Operation must be %s or %s", driver.OperationIncreaseDataRetention, driver.OperationDecreaseDataRetention)
	}

	return m.mutateStream(name, in.CurrentVersion, func(s *driver.StreamInfo) {
		if in.Operation == driver.OperationIncreaseDataRetention {
			s.DataRetentionInHours += in.DataRetentionChangeInHours

			return
		}

		s.DataRetentionInHours -= in.DataRetentionChangeInHours
		if s.DataRetentionInHours < 0 {
			s.DataRetentionInHours = 0
		}
	})
}

// mutateStream applies fn to the stored stream, enforcing the optional
// current-version guard and rotating the Version token.
func (m *Mock) mutateStream(name, currentVersion string, fn func(*driver.StreamInfo)) error {
	var verErr error

	ok := m.streams.Update(name, func(s driver.StreamInfo) driver.StreamInfo {
		if currentVersion != "" && currentVersion != s.Version {
			verErr = invalid("current version %s does not match stream version", currentVersion)

			return s
		}

		fn(&s)
		s.Version = newVersion()

		return s
	})
	if !ok {
		return notFound("stream", name)
	}

	return verErr
}

// DeleteStream removes a stream, enforcing the optional current-version guard.
func (m *Mock) DeleteStream(_ context.Context, ref driver.StreamRef, currentVersion string) error {
	name, err := m.resolveStreamName(ref)
	if err != nil {
		return err
	}

	s, ok := m.streams.Get(name)
	if !ok {
		return notFound("stream", name)
	}

	if currentVersion != "" && currentVersion != s.Version {
		return invalid("current version %s does not match stream version", currentVersion)
	}

	m.streams.Delete(name)

	return nil
}

// ListStreams returns a deterministic page of streams ordered by name, applying
// an optional BEGINS_WITH name filter.
//
//nolint:dupl // parallel to ListSignalingChannels by design; operates on the stream store and type.
func (m *Mock) ListStreams(_ context.Context, page driver.Page) (streams []driver.StreamInfo, nextToken string, err error) {
	stored := m.streams.SortedValues()

	filtered := stored[:0:0]

	for i := range stored {
		if page.NameBeginsWith == "" || strings.HasPrefix(stored[i].StreamName, page.NameBeginsWith) {
			filtered = append(filtered, stored[i])
		}
	}

	start, end, next := paginate(len(filtered), page)

	out := make([]driver.StreamInfo, 0, end-start)
	for i := start; i < end; i++ {
		out = append(out, copyStream(&filtered[i]))
	}

	return out, next, nil
}

// resolveStreamName resolves a StreamRef to a stream name, parsing the name out
// of a StreamARN when the name was not supplied directly.
func (*Mock) resolveStreamName(ref driver.StreamRef) (string, error) {
	if ref.StreamName != "" {
		return ref.StreamName, nil
	}

	if ref.StreamARN == "" {
		return "", invalid("StreamName or StreamARN is required")
	}

	name, resType, ok := nameFromARN(ref.StreamARN)
	if !ok || resType != resourceStream {
		return "", invalidResourceFormat(ref.StreamARN)
	}

	return name, nil
}
