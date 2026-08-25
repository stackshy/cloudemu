package s3

import (
	"context"
	"encoding/json"
	"fmt"
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

// objectEvent carries the object-level facts an S3 event record reports, so the
// event builders take one value instead of a long positional parameter list.
type objectEvent struct {
	bucket    string
	key       string
	size      int64
	eTag      string
	versionID string
	eventName string // e.g. "ObjectCreated:Put" (no "s3:" prefix)
	sequencer string
}

// notifyObjectCreated delivers an s3:ObjectCreated:Put event (PutObject, copy,
// or completed multipart upload) to matching notification targets.
func (m *Mock) notifyObjectCreated(bkt *bucketMeta, bucket, key string, size int64, eTag, versionID string) {
	m.notify(bkt, &objectEvent{
		bucket: bucket, key: key, size: size, eTag: eTag,
		versionID: versionID, eventName: "ObjectCreated:Put",
	})
}

// notifyObjectRemoved delivers an s3:ObjectRemoved:Delete event to matching
// notification targets.
func (m *Mock) notifyObjectRemoved(bkt *bucketMeta, bucket, key, versionID string) {
	m.notify(bkt, &objectEvent{
		bucket: bucket, key: key, versionID: versionID, eventName: "ObjectRemoved:Delete",
	})
}

// notify delivers an S3 event to every notification target configured on the
// bucket whose event selector matches and whose S3Key filter (prefix/suffix)
// accepts the object key. Best-effort: delivery errors are swallowed so a
// missing/failed target never fails the object operation (mirroring S3's
// asynchronous, decoupled notification behavior).
func (m *Mock) notify(bkt *bucketMeta, ev *objectEvent) {
	if len(bkt.notifications) == 0 {
		return
	}

	// A single object operation carries one sequencer across all of its
	// notification records, matching real S3.
	ev.sequencer = m.nextSequencer()

	for i := range bkt.notifications {
		n := &bkt.notifications[i]
		if !eventMatches(n.Events, ev.eventName) || !filterMatches(n.Filters, ev.key) {
			continue
		}

		m.deliver(n, m.objectEventJSON(ev, n.ID))
	}
}

// nextSequencer returns a monotonically increasing, 0-padded hexadecimal token,
// the shape S3 stamps on ObjectCreated/ObjectRemoved event records so a consumer
// can order events for a given object key.
func (m *Mock) nextSequencer() string {
	return fmt.Sprintf("%016X", m.eventSeq.Add(1))
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

// objectEventJSON renders the S3 event-notification record for a single
// notification target (its configurationId is the target's id). The shape
// mirrors the documented S3 event message: top-level eventVersion/userIdentity/
// requestParameters/responseElements plus the full s3 block (s3SchemaVersion,
// configurationId, bucket.{name,arn,ownerIdentity} and object.{key,size,eTag,
// versionId,sequencer}), so a handler reading record.s3.object.eTag/sequencer or
// record.s3.bucket.arn sees real values.
func (m *Mock) objectEventJSON(ev *objectEvent, configurationID string) string {
	object := map[string]any{
		"key":       ev.key,
		"sequencer": ev.sequencer,
	}

	// Object creation reports size/eTag; a removal (delete marker) carries
	// neither, matching real S3's ObjectRemoved records.
	if strings.HasPrefix(ev.eventName, "ObjectCreated") {
		object["size"] = ev.size
		object["eTag"] = ev.eTag
	}

	if ev.versionID != "" {
		object["versionId"] = ev.versionID
	}

	record := map[string]any{
		"eventVersion":      "2.1",
		"eventSource":       "aws:s3",
		"awsRegion":         m.opts.Region,
		"eventTime":         m.opts.Clock.Now().UTC().Format(s3TimeFormat),
		"eventName":         ev.eventName,
		"userIdentity":      map[string]any{"principalId": m.opts.AccountID},
		"requestParameters": map[string]any{"sourceIPAddress": "127.0.0.1"},
		"responseElements": map[string]any{
			"x-amz-request-id": ev.sequencer,
			"x-amz-id-2":       ev.sequencer,
		},
		"s3": map[string]any{
			"s3SchemaVersion": "1.0",
			"configurationId": configurationID,
			"bucket": map[string]any{
				"name":          ev.bucket,
				"ownerIdentity": map[string]any{"principalId": m.opts.AccountID},
				"arn":           "arn:aws:s3:::" + ev.bucket,
			},
			"object": object,
		},
	}

	b, err := json.Marshal(map[string]any{"Records": []any{record}})
	if err != nil {
		return "{}"
	}

	return string(b)
}
