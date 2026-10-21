package eventbridge

import (
	"context"
	"encoding/json"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/eventbus/driver"
)

const (
	// maxPutEventsEntries is the most entries one PutEvents request may carry.
	maxPutEventsEntries = 10

	// putEventsSizeLimit is the total entry size a PutEvents request must stay
	// under (1 MB since January 2026).
	putEventsSizeLimit = 1 << 20
	// legacyPutEventsSizeLimit is the 256 KB limit still in force in the regions
	// that did not get the 1 MB increase.
	legacyPutEventsSizeLimit = 256 << 10
	// timeEntrySize is what a set Time field adds to an entry's size.
	timeEntrySize = 14

	// busDimension is the AWS/Events metric dimension naming the event bus.
	busDimension = "EventBusName"

	errCodeInvalidArgument = "InvalidArgument"
	errCodeMalformedDetail = "MalformedDetail"
)

const (
	putEventsEntryCountMessage = "1 validation error detected: Value at 'entries' failed to satisfy " +
		"constraint: Member must have length less than or equal to 10 and greater than or equal to 1."
	putEventsSizeMessage = "Total size of the entries in the request is over the limit."

	msgSourceRequired     = "Parameter Source is not valid. Reason: Source is a required argument."
	msgDetailTypeRequired = "Parameter DetailType is not valid. Reason: DetailType is a required argument."
	msgDetailRequired     = "Parameter Detail is not valid. Reason: Detail is a required argument."
	msgMalformedDetail    = "Detail is malformed."
)

// legacySizeLimitRegion reports whether region is one where the 1 MB PutEvents
// payload increase is not available: Malaysia, New Zealand, Thailand, Taipei and
// Mexico (Central).
func legacySizeLimitRegion(region string) bool {
	switch region {
	case "ap-southeast-5", "ap-southeast-6", "ap-southeast-7", "ap-east-2", "mx-central-1":
		return true
	default:
		return false
	}
}

func sizeLimitFor(region string) int {
	if legacySizeLimitRegion(region) {
		return legacyPutEventsSizeLimit
	}

	return putEventsSizeLimit
}

// entrySize computes a PutEvents entry's size the way EventBridge documents it:
// 14 bytes for a set Time plus the UTF-8 bytes of Source, DetailType, Detail and
// each Resource.
func entrySize(e *driver.Event) int {
	size := len(e.Source) + len(e.DetailType) + len(e.Detail)
	if !e.Time.IsZero() {
		size += timeEntrySize
	}

	for _, r := range e.Resources {
		size += len(r)
	}

	return size
}

// validatePutEventsRequest applies the request-wide limits that fail the whole
// call: 1 to 10 entries and a total size under the region's limit.
func validatePutEventsRequest(events []driver.Event, region string) error {
	if n := len(events); n < 1 || n > maxPutEventsEntries {
		return errors.New(errors.InvalidArgument, putEventsEntryCountMessage)
	}

	total := 0
	for i := range events {
		total += entrySize(&events[i])
	}

	if total >= sizeLimitFor(region) {
		return errors.New(errors.InvalidArgument, putEventsSizeMessage)
	}

	return nil
}

// validateEntry checks one entry in the order EventBridge does, returning the
// per-entry error code and message, or an empty code when the entry is valid.
func validateEntry(e *driver.Event) (code, msg string) {
	switch {
	case e.Source == "":
		return errCodeInvalidArgument, msgSourceRequired
	case e.DetailType == "":
		return errCodeInvalidArgument, msgDetailTypeRequired
	case e.Detail == "":
		return errCodeInvalidArgument, msgDetailRequired
	case !isJSONObject(e.Detail):
		return errCodeMalformedDetail, msgMalformedDetail
	default:
		return "", ""
	}
}

// isJSONObject reports whether s is a well-formed JSON object. EventBridge fails
// any other Detail (string, array, null, malformed) with MalformedDetail.
func isJSONObject(s string) bool {
	var obj map[string]json.RawMessage

	return json.Unmarshal([]byte(s), &obj) == nil && obj != nil
}

// PutEventEntries publishes a PutEvents batch and reports each entry's outcome,
// aligned by index with events. Request-wide violations (entry count, total
// size) fail the whole call; an invalid entry fails only its own slot. An entry
// addressed to a bus that does not exist still gets an EventId and counts as a
// success, but is dropped: that is what real EventBridge does.
func (m *Mock) PutEventEntries(ctx context.Context, events []driver.Event) ([]driver.PutEventsEntryResult, error) {
	if err := validatePutEventsRequest(events, m.opts.Region); err != nil {
		return nil, err
	}

	results := make([]driver.PutEventsEntryResult, len(events))

	for i := range events {
		if code, msg := validateEntry(&events[i]); code != "" {
			results[i] = driver.PutEventsEntryResult{ErrorCode: code, ErrorMessage: msg}

			continue
		}

		results[i] = driver.PutEventsEntryResult{EventID: m.publishEvent(ctx, &events[i], i)}
	}

	return results, nil
}

// PutEvents publishes events to the event bus. It is the portable form of
// PutEventEntries, summarizing the per-entry outcomes into counts.
func (m *Mock) PutEvents(ctx context.Context, events []driver.Event) (*driver.PublishResult, error) {
	entries, err := m.PutEventEntries(ctx, events)
	if err != nil {
		return nil, err
	}

	result := &driver.PublishResult{EventIDs: make([]string, 0, len(entries))}

	for _, e := range entries {
		if e.ErrorCode != "" {
			result.FailCount++

			continue
		}

		result.SuccessCount++
		result.EventIDs = append(result.EventIDs, e.EventID)
	}

	return result, nil
}

// publishEvent assigns the event its id and time, then stores, matches and
// delivers it on its bus. It returns the event id even when the bus does not
// exist, in which case the event is dropped without being stored or delivered.
func (m *Mock) publishEvent(ctx context.Context, event *driver.Event, index int) string {
	now := m.opts.Clock.Now()

	event.ID = generateEventID(event, now, index)
	if event.Time.IsZero() {
		event.Time = now
	}

	busName := event.EventBus
	if busName == "" {
		busName = defaultBusName
	}

	bd, ok := m.buses.Get(busName)
	if !ok {
		return event.ID
	}

	m.storeEvent(bd, event)
	m.captureToArchives(busName, event)
	matched := m.MatchedRules(event)
	m.deliverToTargets(ctx, matched, event)

	dims := map[string]string{busDimension: busName}
	m.emitMetric(ctx, "PutEventsRequestCount", 1, dims)
	m.emitMetric(ctx, "MatchedEvents", float64(len(matched)), dims)

	return event.ID
}
