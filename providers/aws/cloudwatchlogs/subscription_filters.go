package cloudwatchlogs

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/recursionguard"
	"github.com/stackshy/cloudemu/v2/services/logging/driver"
)

// maxSubscriptionFilters is the number of subscription filters CloudWatch Logs
// allows on a single log group. A create that would exceed it (a new filter
// name, not an update of an existing one) is rejected with LimitExceededException.
const maxSubscriptionFilters = 2

// PutSubscriptionFilter creates or updates a subscription filter on a log group.
// Updating reuses an existing filter name; a new name is rejected once the group
// already holds maxSubscriptionFilters filters.
func (m *Mock) PutSubscriptionFilter(_ context.Context, cfg *driver.SubscriptionFilterConfig) error {
	g, ok := m.groups.Get(cfg.LogGroup)
	if !ok {
		return errors.Newf(errors.NotFound, "log group %q not found", cfg.LogGroup)
	}

	if cfg.Name == "" {
		return errors.New(errors.InvalidArgument, "subscription filter name is required")
	}

	if cfg.DestinationARN == "" {
		return errors.New(errors.InvalidArgument, "destinationArn is required")
	}

	// A new filter (not an update of an existing name) must fit within the
	// per-group quota; real CloudWatch Logs caps a group at two filters.
	if !g.subFilters.Has(cfg.Name) && g.subFilters.Len() >= maxSubscriptionFilters {
		return errors.Newf(errors.ResourceExhausted,
			"log group %q already has the maximum of %d subscription filters", cfg.LogGroup, maxSubscriptionFilters)
	}

	info := &driver.SubscriptionFilterInfo{
		Name:           cfg.Name,
		LogGroup:       cfg.LogGroup,
		FilterPattern:  cfg.FilterPattern,
		DestinationARN: cfg.DestinationARN,
		RoleARN:        cfg.RoleARN,
		Distribution:   cfg.Distribution,
		CreatedAt:      m.opts.Clock.Now().UTC(),
	}

	g.subFilters.Set(cfg.Name, info)

	return nil
}

// DeleteSubscriptionFilter removes a subscription filter from a log group.
func (m *Mock) DeleteSubscriptionFilter(_ context.Context, logGroup, filterName string) error {
	g, ok := m.groups.Get(logGroup)
	if !ok {
		return errors.Newf(errors.NotFound, "log group %q not found", logGroup)
	}

	if !g.subFilters.Delete(filterName) {
		return errors.Newf(errors.NotFound,
			"subscription filter %q not found in group %q", filterName, logGroup)
	}

	return nil
}

// DescribeSubscriptionFilters lists a log group's subscription filters, ordered
// by filter name (memstore SortedValues) to match CloudWatch Logs' ASCII sort.
func (m *Mock) DescribeSubscriptionFilters(_ context.Context, logGroup string) ([]driver.SubscriptionFilterInfo, error) {
	g, ok := m.groups.Get(logGroup)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "log group %q not found", logGroup)
	}

	all := g.subFilters.SortedValues()
	results := make([]driver.SubscriptionFilterInfo, 0, len(all))

	for _, sf := range all {
		results = append(results, *sf)
	}

	return results, nil
}

// deliverToSubscriptions streams the events just written to streamName to every
// subscription filter on the group whose pattern matches. Delivery is
// best-effort and decoupled from the caller's context (mirroring CloudWatch
// Logs' asynchronous real-time delivery); only the re-entrant delivery depth is
// carried forward so a subscriber Lambda that writes back into CloudWatch Logs
// stays bounded (the guard lives in lambda.InvokeExternal).
func (m *Mock) deliverToSubscriptions(callerCtx context.Context, g *logGroup, streamName string, events []driver.LogEvent) {
	if m.lambda == nil || g.subFilters.Len() == 0 || len(events) == 0 {
		return
	}

	ctx := recursionguard.WithDepth(context.Background(), recursionguard.Depth(callerCtx))

	for _, sf := range g.subFilters.SortedValues() {
		matched := matchSubscriptionEvents(sf.FilterPattern, events)
		if len(matched) == 0 {
			continue
		}

		m.deliverSubscription(ctx, sf, g.info.Name, streamName, matched)
	}
}

// deliverSubscription dispatches matched events to a single subscription
// filter's destination. Lambda destinations are invoked with the CloudWatch
// Logs subscription payload; Kinesis and Firehose destinations are not yet
// wired (deferred) and are silently skipped.
func (m *Mock) deliverSubscription(
	ctx context.Context,
	sf *driver.SubscriptionFilterInfo,
	groupName, streamName string,
	events []driver.LogEvent,
) {
	if !strings.Contains(sf.DestinationARN, ":lambda:") {
		// Kinesis / Firehose delivery is deferred; nothing to do for now.
		return
	}

	payload, err := m.buildSubscriptionPayload(sf, groupName, streamName, events)
	if err != nil {
		return
	}

	_ = m.lambda.InvokeExternal(ctx, sf.DestinationARN, payload)
}

// matchSubscriptionEvents returns the events whose message satisfies the filter
// pattern. An empty pattern matches every event; otherwise substring matching is
// used (the same simplification the metric-filter path applies — the richer
// CloudWatch Logs filter grammar is tracked separately).
func matchSubscriptionEvents(pattern string, events []driver.LogEvent) []driver.LogEvent {
	if pattern == "" {
		return events
	}

	matched := make([]driver.LogEvent, 0, len(events))

	for i := range events {
		if strings.Contains(events[i].Message, pattern) {
			matched = append(matched, events[i])
		}
	}

	return matched
}

// cwlSubscriptionEvent is the decoded CloudWatch Logs subscription payload
// (the JSON that is gzipped and base64-encoded into awslogs.data).
type cwlSubscriptionEvent struct {
	MessageType         string                    `json:"messageType"`
	Owner               string                    `json:"owner"`
	LogGroup            string                    `json:"logGroup"`
	LogStream           string                    `json:"logStream"`
	SubscriptionFilters []string                  `json:"subscriptionFilters"`
	LogEvents           []cwlSubscriptionLogEvent `json:"logEvents"`
}

type cwlSubscriptionLogEvent struct {
	ID        string `json:"id"`
	Timestamp int64  `json:"timestamp"`
	Message   string `json:"message"`
}

// awslogsEnvelope wraps the gzipped+base64 subscription data the way Lambda
// receives it: {"awslogs":{"data":"<base64(gzip(json))>"}}.
type awslogsEnvelope struct {
	AWSLogs struct {
		Data string `json:"data"`
	} `json:"awslogs"`
}

// buildSubscriptionPayload renders the Lambda subscription-filter event: the
// CloudWatch Logs subscription JSON, gzipped, base64-encoded, and wrapped in the
// awslogs envelope, matching the real invocation shape a subscriber Lambda decodes.
func (m *Mock) buildSubscriptionPayload(
	sf *driver.SubscriptionFilterInfo,
	groupName, streamName string,
	events []driver.LogEvent,
) ([]byte, error) {
	inner := cwlSubscriptionEvent{
		MessageType:         "DATA_MESSAGE",
		Owner:               m.opts.AccountID,
		LogGroup:            groupName,
		LogStream:           streamName,
		SubscriptionFilters: []string{sf.Name},
		LogEvents:           make([]cwlSubscriptionLogEvent, 0, len(events)),
	}

	for i := range events {
		ts := events[i].Timestamp.UnixMilli()
		inner.LogEvents = append(inner.LogEvents, cwlSubscriptionLogEvent{
			ID:        subscriptionEventID(ts, i),
			Timestamp: ts,
			Message:   events[i].Message,
		})
	}

	raw, err := json.Marshal(inner)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer

	gz := gzip.NewWriter(&buf)
	if _, err = gz.Write(raw); err != nil {
		return nil, err
	}

	if closeErr := gz.Close(); closeErr != nil {
		return nil, closeErr
	}

	var env awslogsEnvelope
	env.AWSLogs.Data = base64.StdEncoding.EncodeToString(buf.Bytes())

	return json.Marshal(env)
}

// subscriptionEventID builds a stable per-event identifier for the subscription
// payload. Real CloudWatch Logs uses an opaque numeric token; a timestamp-derived
// value keeps events distinguishable within a delivery without pretending to be
// the real 56-digit id.
func subscriptionEventID(timestampMillis int64, index int) string {
	return strconv.FormatInt(timestampMillis, 10) + strconv.Itoa(index)
}
