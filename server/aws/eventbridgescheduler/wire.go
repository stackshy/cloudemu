package eventbridgescheduler

import (
	"encoding/json"
	"time"

	"github.com/stackshy/cloudemu/v2/services/eventbridgescheduler/driver"
)

// epochOrNil renders a time as Unix-epoch-seconds the Scheduler SDK decodes into
// a *time.Time, or nil for the zero time.
func epochOrNil(t time.Time) *float64 {
	if t.IsZero() {
		return nil
	}

	secs := float64(t.Unix())

	return &secs
}

// scheduleToWire renders a schedule as its GetSchedule restJson1 object. Required
// fields are always present; the target, flexible-time-window and start/end
// dates are emitted verbatim from their stored raw JSON so nested blocks
// round-trip exactly. Optional scalars are omitted when unset, matching the
// service.
func scheduleToWire(s *driver.Schedule) map[string]any {
	out := map[string]any{
		"Arn":                  s.Arn,
		"Name":                 s.Name,
		"GroupName":            s.GroupName,
		"ScheduleExpression":   s.ScheduleExpression,
		"State":                s.State,
		"CreationDate":         epochOrNil(s.CreationDate),
		"LastModificationDate": epochOrNil(s.LastModificationDate),
		"FlexibleTimeWindow":   rawOrNil(s.FlexibleTimeWindow),
		"Target":               rawOrNil(s.Target),
	}

	putNonEmpty(out, "Description", s.Description)
	putNonEmpty(out, "ScheduleExpressionTimezone", s.ScheduleExpressionTimezone)
	putNonEmpty(out, "KmsKeyArn", s.KmsKeyArn)
	putNonEmpty(out, "ActionAfterCompletion", s.ActionAfterCompletion)
	putNonEmptyRaw(out, "StartDate", s.StartDate)
	putNonEmptyRaw(out, "EndDate", s.EndDate)

	return out
}

// scheduleSummaryToWire renders a schedule as its ListSchedules ScheduleSummary
// object. The summary's Target carries only the target Arn.
func scheduleSummaryToWire(s *driver.Schedule) map[string]any {
	out := map[string]any{
		"Arn":                  s.Arn,
		"Name":                 s.Name,
		"GroupName":            s.GroupName,
		"State":                s.State,
		"CreationDate":         epochOrNil(s.CreationDate),
		"LastModificationDate": epochOrNil(s.LastModificationDate),
	}

	if arn := targetARN(s.Target); arn != "" {
		out["Target"] = map[string]any{"Arn": arn}
	}

	return out
}

// groupToWire renders a schedule group as its GetScheduleGroup restJson1 object.
func groupToWire(g *driver.ScheduleGroup) map[string]any {
	return map[string]any{
		"Arn":                  g.Arn,
		"Name":                 g.Name,
		"State":                g.State,
		"CreationDate":         epochOrNil(g.CreationDate),
		"LastModificationDate": epochOrNil(g.LastModificationDate),
	}
}

// targetARN extracts the Arn field from a stored target's raw JSON for the list
// summary; a malformed or absent target yields an empty string.
func targetARN(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}

	var t struct {
		Arn string `json:"Arn"`
	}

	if err := json.Unmarshal(raw, &t); err != nil {
		return ""
	}

	return t.Arn
}

// rawOrNil returns the raw JSON message, or nil so a required block that is
// somehow empty serializes as null rather than an invalid empty token.
func rawOrNil(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}

	return raw
}

// putNonEmpty sets key to val only when val is non-empty.
func putNonEmpty(m map[string]any, key, val string) {
	if val != "" {
		m[key] = val
	}
}

// putNonEmptyRaw sets key to the raw JSON only when it is present, so an optional
// timestamp the caller never set is omitted from the wire object.
func putNonEmptyRaw(m map[string]any, key string, raw json.RawMessage) {
	if len(raw) > 0 {
		m[key] = raw
	}
}

// maxQueryInt bounds a parsed query integer so the result fits an int32 without
// overflow; Scheduler's MaxResults tops out well below this.
const maxQueryInt = 1 << 20

// atoiDefault parses a non-negative query int32, returning 0 when empty or
// invalid. It is bounded by maxQueryInt so the conversion cannot overflow.
func atoiDefault(s string) int32 {
	n := 0

	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}

		n = n*10 + int(c-'0')
		if n > maxQueryInt {
			return maxQueryInt
		}
	}

	return int32(n)
}
