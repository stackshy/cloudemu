package cloudtrail

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/cloudtrail/driver"
)

const (
	// maxRecordedEvents bounds the in-memory management-event log. Real CloudTrail
	// keeps 90 days of LookupEvents history; the emulator keeps the most recent
	// maxRecordedEvents and drops the oldest, so a long-lived process is bounded.
	maxRecordedEvents = 2000
	// defaultLookupResults / maxLookupResults mirror the LookupEvents API, whose
	// MaxResults defaults to and is capped at 50.
	defaultLookupResults = 50
	maxLookupResults     = 50
)

// LookupAttribute keys accepted by LookupEvents, matching the AWS API.
const (
	attrEventID      = "EventId"
	attrEventName    = "EventName"
	attrEventSource  = "EventSource"
	attrUsername     = "Username"
	attrReadOnly     = "ReadOnly"
	attrAccessKeyID  = "AccessKeyId"
	attrResourceType = "ResourceType"
	attrResourceName = "ResourceName"
)

// RecordEvent appends a management event to the bounded event log LookupEvents
// reads. It is called by the wire server as API activity flows through it, so
// the emulator's audit trail reflects real API calls rather than staying empty.
// It is part of the AWS-only EventRecorder capability (see the driver package).
func (m *Mock) RecordEvent(e *driver.Event) {
	now := m.now()

	// The recorder owns the timestamp, id, and the CloudTrailEvent JSON blob so
	// the clock (FakeClock in tests) and region stay in the provider.
	e.EventTime = now
	if e.EventID == "" {
		e.EventID = idgen.GenerateID("")
	}

	if e.CloudTrailEvent == "" {
		e.CloudTrailEvent = m.cloudTrailEventJSON(e, now)
	}

	m.eventsMu.Lock()
	defer m.eventsMu.Unlock()

	m.events = append(m.events, *e)
	if len(m.events) > maxRecordedEvents {
		// Drop the oldest, keeping the newest maxRecordedEvents.
		m.events = append([]driver.Event(nil), m.events[len(m.events)-maxRecordedEvents:]...)
	}
}

// cloudTrailEventJSON renders the JSON document real CloudTrail returns in the
// CloudTrailEvent field of a LookupEvents result — the full event record.
func (m *Mock) cloudTrailEventJSON(e *driver.Event, now time.Time) string {
	doc := map[string]any{
		"eventVersion":       "1.08",
		"eventID":            e.EventID,
		"eventTime":          now.Format(time.RFC3339),
		"eventName":          e.EventName,
		"eventSource":        e.EventSource,
		"awsRegion":          m.opts.Region,
		"readOnly":           e.ReadOnly == "true",
		"managementEvent":    true,
		"eventCategory":      "Management",
		"recipientAccountId": m.opts.AccountID,
		"userIdentity": map[string]any{
			"type":        "IAMUser",
			"accountId":   m.opts.AccountID,
			"accessKeyId": e.AccessKeyID,
			"userName":    e.Username,
		},
	}

	b, err := json.Marshal(doc)
	if err != nil {
		return ""
	}

	return string(b)
}

// LookupEvents returns management events matching the request's attribute and
// time filters, newest first, paginated. Real CloudTrail LookupEvents queries
// the last 90 days of management events; here it queries the recorded log.
//
//nolint:gocritic // in matches the driver LookupEvents signature (taken by value)
func (m *Mock) LookupEvents(_ context.Context, in driver.LookupInput) ([]driver.Event, string, error) {
	m.eventsMu.RLock()
	all := append([]driver.Event(nil), m.events...)
	m.eventsMu.RUnlock()

	// Newest first, matching the API's default ordering.
	sort.SliceStable(all, func(i, j int) bool { return all[i].EventTime.After(all[j].EventTime) })

	matched := make([]driver.Event, 0, len(all))

	for i := range all {
		if eventMatches(&all[i], &in) {
			matched = append(matched, all[i])
		}
	}

	start := decodeLookupToken(in.NextToken)
	if start > len(matched) {
		start = len(matched)
	}

	limit := int(in.MaxResults)
	if limit <= 0 || limit > maxLookupResults {
		limit = defaultLookupResults
	}

	end := start + limit
	if end > len(matched) {
		end = len(matched)
	}

	next := ""
	if end < len(matched) {
		next = strconv.Itoa(end)
	}

	return matched[start:end], next, nil
}

// eventMatches reports whether an event satisfies the lookup's time window and
// every supplied attribute filter (AWS matches on the attributes given).
func eventMatches(e *driver.Event, in *driver.LookupInput) bool {
	if !in.StartTime.IsZero() && e.EventTime.Before(in.StartTime) {
		return false
	}

	if !in.EndTime.IsZero() && e.EventTime.After(in.EndTime) {
		return false
	}

	for _, a := range in.LookupAttributes {
		if !attributeMatches(e, a.AttributeKey, a.AttributeValue) {
			return false
		}
	}

	return true
}

func attributeMatches(e *driver.Event, key, value string) bool {
	switch key {
	case attrEventID:
		return e.EventID == value
	case attrEventName:
		return e.EventName == value
	case attrEventSource:
		return e.EventSource == value
	case attrUsername:
		return e.Username == value
	case attrAccessKeyID:
		return e.AccessKeyID == value
	case attrReadOnly:
		return strings.EqualFold(e.ReadOnly, value)
	case attrResourceType, attrResourceName:
		// Resources are not modeled on recorded events; nothing matches, matching
		// real CloudTrail returning no events for an unmatched resource filter.
		return false
	default:
		return false
	}
}

func decodeLookupToken(token string) int {
	if token == "" {
		return 0
	}

	n, err := strconv.Atoi(token)
	if err != nil || n < 0 {
		return 0
	}

	return n
}
