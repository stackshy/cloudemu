package s3

import (
	"context"
	"encoding/json"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// PutBucketNotification replaces a bucket's SQS notification configuration.
func (m *Mock) PutBucketNotification(_ context.Context, bucket string, configs []QueueNotification) error {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	bkt.notifications = configs

	return nil
}

// GetBucketNotification returns a bucket's SQS notification configuration.
func (m *Mock) GetBucketNotification(_ context.Context, bucket string) ([]QueueNotification, error) {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	return bkt.notifications, nil
}

// notifyObjectCreated delivers an s3:ObjectCreated:Put event to every SQS
// target configured on the bucket whose event filter matches. Best-effort:
// delivery errors are swallowed so a missing/failed queue never fails the
// upload (mirroring S3's asynchronous, decoupled notification behavior).
func (m *Mock) notifyObjectCreated(bkt *bucketMeta, bucket, key string, size int64) {
	if m.sqs == nil || len(bkt.notifications) == 0 {
		return
	}

	const eventName = "ObjectCreated:Put"

	body := m.objectEventJSON(bucket, key, size, eventName)

	for i := range bkt.notifications {
		n := &bkt.notifications[i]
		if !eventMatches(n.Events, eventName) {
			continue
		}

		_ = m.sqs.DeliverExternal(context.Background(), n.QueueARN, body)
	}
}

// eventMatches reports whether an event name satisfies one of the configured
// event selectors. "s3:ObjectCreated:*" matches any ObjectCreated:* event;
// "s3:ObjectCreated:Put" matches exactly.
func eventMatches(selectors []string, eventName string) bool {
	full := "s3:" + eventName

	for _, sel := range selectors {
		switch {
		case sel == full:
			return true
		case strings.HasSuffix(sel, ":*"):
			prefix := strings.TrimSuffix(sel, "*") // "s3:ObjectCreated:"
			if strings.HasPrefix(full, prefix) {
				return true
			}
		}
	}

	return false
}

func (m *Mock) objectEventJSON(bucket, key string, size int64, eventName string) string {
	record := map[string]any{
		"eventSource": "aws:s3",
		"eventName":   eventName,
		"awsRegion":   m.opts.Region,
		"eventTime":   m.opts.Clock.Now().UTC().Format(s3TimeFormat),
		"s3": map[string]any{
			"bucket": map[string]any{"name": bucket},
			"object": map[string]any{"key": key, "size": size},
		},
	}

	b, err := json.Marshal(map[string]any{"Records": []any{record}})
	if err != nil {
		return "{}"
	}

	return string(b)
}
