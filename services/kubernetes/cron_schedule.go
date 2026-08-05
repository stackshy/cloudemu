package kubernetes

import (
	"strconv"
	"strings"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// Self-contained deterministic parser for the standard 5-field cron syntax
// (minute hour day-of-month month day-of-week). It supports `*`, `*/n` steps,
// comma lists, `a-b` ranges, and `a-b/n` / `a/n` stepped ranges. It intentionally
// does NOT support the nonstandard extensions (`@hourly`-style macros, `L`, `W`,
// `#`, `?`, or a seconds/year field) — callers get an error for those so an
// unschedulable expression fails loudly rather than silently never firing.

const (
	cronFieldCount = 5

	minuteLo, minuteHi = 0, 59
	hourLo, hourHi     = 0, 23
	domLo, domHi       = 1, 31
	monthLo, monthHi   = 1, 12
	dowLo, dowHi       = 0, 6 // 0 = Sunday, matching time.Weekday()

	// nextSearchDays bounds the forward scan in nextAfter so an expression that
	// can never match (e.g. Feb 30) returns an error instead of looping forever.
	// A window over 4 years covers leap-year day-of-week alignment.
	nextSearchDays = 366*4 + 1
)

// cronSchedule is a parsed cron expression: one allowed-value set per field.
type cronSchedule struct {
	minute map[int]bool
	hour   map[int]bool
	dom    map[int]bool
	month  map[int]bool
	dow    map[int]bool

	// Cron day matching: when both day-of-month and day-of-week are restricted
	// (neither is `*`), a day matches if EITHER field matches; when only one is
	// restricted, that field alone gates the day.
	domRestricted bool
	dowRestricted bool
}

// parseSchedule parses a standard 5-field cron expression.
func parseSchedule(spec string) (*cronSchedule, error) {
	fields := strings.Fields(spec)
	if len(fields) != cronFieldCount {
		return nil, cerrors.Newf(cerrors.InvalidArgument,
			"cron: expected %d fields, got %d in %q", cronFieldCount, len(fields), spec)
	}

	sched := &cronSchedule{
		domRestricted: fields[2] != "*",
		dowRestricted: fields[4] != "*",
	}

	specs := []struct {
		field    string
		lo, hi   int
		dst      *map[int]bool
		fieldPos string
	}{
		{fields[0], minuteLo, minuteHi, &sched.minute, "minute"},
		{fields[1], hourLo, hourHi, &sched.hour, "hour"},
		{fields[2], domLo, domHi, &sched.dom, "day-of-month"},
		{fields[3], monthLo, monthHi, &sched.month, "month"},
		{fields[4], dowLo, dowHi, &sched.dow, "day-of-week"},
	}

	for _, sp := range specs {
		set, err := parseCronField(sp.field, sp.lo, sp.hi)
		if err != nil {
			return nil, cerrors.Newf(cerrors.InvalidArgument, "cron %s: %v", sp.fieldPos, err)
		}

		*sp.dst = set
	}

	return sched, nil
}

// parseCronField parses one comma-separated cron field into its value set.
func parseCronField(field string, lo, hi int) (map[int]bool, error) {
	out := make(map[int]bool)

	for _, part := range strings.Split(field, ",") {
		if err := parseCronPart(part, lo, hi, out); err != nil {
			return nil, err
		}
	}

	return out, nil
}

// parseCronPart parses a single list element (`*`, `n`, `a-b`, or any of those
// with a `/step`) and adds its values to out.
func parseCronPart(part string, lo, hi int, out map[int]bool) error {
	rangeStr := part
	step := 1
	stepped := false

	if slash := strings.IndexByte(part, '/'); slash >= 0 {
		s, err := strconv.Atoi(part[slash+1:])
		if err != nil || s <= 0 {
			return cerrors.Newf(cerrors.InvalidArgument, "invalid step %q", part)
		}

		rangeStr, step, stepped = part[:slash], s, true
	}

	start, end, err := parseCronRange(rangeStr, lo, hi, stepped)
	if err != nil {
		return err
	}

	for v := start; v <= end; v += step {
		out[v] = true
	}

	return nil
}

// parseCronRange resolves the range a step applies over. A bare number with a
// step (`a/n`) runs from a to the field maximum, matching standard cron.
func parseCronRange(rangeStr string, lo, hi int, stepped bool) (start, end int, err error) {
	if rangeStr == "*" {
		return lo, hi, nil
	}

	if dash := strings.IndexByte(rangeStr, '-'); dash >= 0 {
		start, err = parseBounded(rangeStr[:dash], lo, hi)
		if err != nil {
			return 0, 0, err
		}

		end, err = parseBounded(rangeStr[dash+1:], lo, hi)
		if err != nil {
			return 0, 0, err
		}

		if start > end {
			return 0, 0, cerrors.Newf(cerrors.InvalidArgument, "range %q is inverted", rangeStr)
		}

		return start, end, nil
	}

	v, err := parseBounded(rangeStr, lo, hi)
	if err != nil {
		return 0, 0, err
	}

	if stepped {
		return v, hi, nil
	}

	return v, v, nil
}

// parseBounded parses a single integer and checks it lies within [lo, hi].
func parseBounded(s string, lo, hi int) (int, error) {
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, cerrors.Newf(cerrors.InvalidArgument, "invalid value %q", s)
	}

	if v < lo || v > hi {
		return 0, cerrors.Newf(cerrors.InvalidArgument, "value %d out of range [%d,%d]", v, lo, hi)
	}

	return v, nil
}

// matches reports whether t (at minute granularity) satisfies the schedule.
func (c *cronSchedule) matches(t time.Time) bool {
	if !c.minute[t.Minute()] || !c.hour[t.Hour()] || !c.month[int(t.Month())] {
		return false
	}

	return c.dayMatches(t)
}

// dayMatches applies cron's day-of-month / day-of-week OR semantics.
func (c *cronSchedule) dayMatches(t time.Time) bool {
	domOK := c.dom[t.Day()]
	dowOK := c.dow[int(t.Weekday())]

	switch {
	case c.domRestricted && c.dowRestricted:
		return domOK || dowOK
	case c.domRestricted:
		return domOK
	case c.dowRestricted:
		return dowOK
	default:
		return true
	}
}

// nextAfter returns the earliest scheduled time strictly after t (minute
// resolution, in t's location). It errors if no match falls inside the bounded
// search window, so an impossible schedule can't loop forever.
func (c *cronSchedule) nextAfter(t time.Time) (time.Time, error) {
	cursor := t.Truncate(time.Minute).Add(time.Minute)
	limit := cursor.AddDate(0, 0, nextSearchDays)

	for ; cursor.Before(limit); cursor = cursor.Add(time.Minute) {
		if c.matches(cursor) {
			return cursor, nil
		}
	}

	return time.Time{}, cerrors.Newf(cerrors.InvalidArgument, "cron: no scheduled time within %d days", nextSearchDays)
}
