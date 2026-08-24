package s3

import (
	"context"
	"encoding/json"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// PutBucketNotification replaces a bucket's event-notification configuration
// (SQS queue, SNS topic, and Lambda function targets).
func (m *Mock) PutBucketNotification(_ context.Context, bucket string, configs []BucketNotification) error {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	bkt.notifications = configs

	return nil
}

// GetBucketNotification returns a bucket's event-notification configuration.
func (m *Mock) GetBucketNotification(_ context.Context, bucket string) ([]BucketNotification, error) {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	return bkt.notifications, nil
}

// notifyObjectCreated delivers an s3:ObjectCreated:Put event (PutObject, copy,
// or completed multipart upload) to matching notification targets.
func (m *Mock) notifyObjectCreated(bkt *bucketMeta, bucket, key string, size int64) {
	m.notify(bkt, bucket, key, size, "ObjectCreated:Put")
}

// notifyObjectRemoved delivers an s3:ObjectRemoved:Delete event to matching
// notification targets.
func (m *Mock) notifyObjectRemoved(bkt *bucketMeta, bucket, key string) {
	m.notify(bkt, bucket, key, 0, "ObjectRemoved:Delete")
}

// notify delivers an S3 event to every notification target configured on the
// bucket whose event selector matches and whose S3Key filter (prefix/suffix)
// accepts the object key. Best-effort: delivery errors are swallowed so a
// missing/failed target never fails the object operation (mirroring S3's
// asynchronous, decoupled notification behavior).
func (m *Mock) notify(bkt *bucketMeta, bucket, key string, size int64, eventName string) {
	if len(bkt.notifications) == 0 {
		return
	}

	body := m.objectEventJSON(bucket, key, size, eventName)

	for i := range bkt.notifications {
		n := &bkt.notifications[i]
		if !eventMatches(n.Events, eventName) || !filterMatches(n.Filters, key) {
			continue
		}

		m.deliver(n, body)
	}
}

// deliver dispatches an event body to a single notification target by kind.
func (m *Mock) deliver(n *BucketNotification, body string) {
	ctx := context.Background()

	switch n.Target {
	case NotifyQueue:
		if m.sqs != nil {
			_ = m.sqs.DeliverExternal(ctx, n.ARN, body)
		}
	case NotifyTopic:
		if m.sns != nil {
			_ = m.sns.PublishExternal(ctx, n.ARN, body)
		}
	case NotifyLambda:
		if m.lambda != nil {
			_ = m.lambda.InvokeExternal(ctx, n.ARN, []byte(body))
		}
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

// filterMatches reports whether an object key satisfies every S3Key filter rule.
// S3 ANDs a prefix and a suffix rule, so a key must match all rules; no rules
// means the target receives every matching event.
func filterMatches(rules []NotificationFilterRule, key string) bool {
	for i := range rules {
		switch strings.ToLower(rules[i].Name) {
		case "prefix":
			if !strings.HasPrefix(key, rules[i].Value) {
				return false
			}
		case "suffix":
			if !strings.HasSuffix(key, rules[i].Value) {
				return false
			}
		}
	}

	return true
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
