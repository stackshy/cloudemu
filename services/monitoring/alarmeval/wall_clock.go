package alarmeval

import (
	"errors"
	"regexp"
	"sort"
	"strconv"
	"time"
)

// A wall clock window, from
// https://docs.aws.amazon.com/AmazonCloudWatch/latest/monitoring/alarm-evaluation-window.html,
// aligns each period to the clock in the alarm's time zone: the top of the
// hour for an hour, midnight for a day and Monday 00:00 for a week. A period
// is evaluated only once it has ended, and exactly EvaluationPeriods periods
// are looked at. There is no evaluation range and no premature-alarm rule.

// Periods a wall clock window allows.
const (
	oneMinuteSeconds   = 60
	fiveMinutesSeconds = 300
	oneHourSeconds     = 3600
	oneWeekSeconds     = 604800
)

// WallClockPeriod reports whether a wall clock window can use period seconds.
func WallClockPeriod(period int) bool {
	switch period {
	case oneMinuteSeconds, fiveMinutesSeconds, oneHourSeconds, oneDaySeconds, oneWeekSeconds:
		return true
	default:
		return false
	}
}

// Timezone limits from API_WallClockWindow.
const (
	maxTimezoneLength = 50
	offsetStepMinutes = 5
	maxOffsetHours    = 14
	minutesPerHour    = 60
	secondsPerMinute  = 60
	daysPerWeek       = 7
)

var (
	errTimezone   = errors.New("not a valid time zone")
	offsetPattern = regexp.MustCompile(`^(?:UTC|GMT)?([+-])(\d{2}):(\d{2})$`)
)

// ParseTimezone resolves a WallClockWindow Timezone: an IANA name such as
// America/New_York, a fixed offset such as +05:30 or Z, or an offset with a
// UTC or GMT prefix. Empty is UTC. The offset must be a multiple of 5 minutes.
func ParseTimezone(s string) (*time.Location, error) {
	switch s {
	case "", "Z", "UTC", "GMT":
		return time.UTC, nil
	case "Local":
		return nil, errTimezone
	}

	if len(s) > maxTimezoneLength {
		return nil, errTimezone
	}

	if m := offsetPattern.FindStringSubmatch(s); m != nil {
		return fixedOffset(s, m[1], m[2], m[3])
	}

	loc, err := time.LoadLocation(s)
	if err != nil {
		return nil, errTimezone
	}

	return loc, nil
}

func fixedOffset(name, sign, hh, mm string) (*time.Location, error) {
	hours, _ := strconv.Atoi(hh)
	minutes, _ := strconv.Atoi(mm)

	if hours > maxOffsetHours || minutes >= minutesPerHour {
		return nil, errTimezone
	}

	total := hours*minutesPerHour + minutes
	if total%offsetStepMinutes != 0 {
		return nil, errTimezone
	}

	if sign == "-" {
		total = -total
	}

	return time.FixedZone(name, total*secondsPerMinute), nil
}

// wallFloor is the start of the wall clock period that holds t.
func wallFloor(t time.Time, period time.Duration, loc *time.Location) time.Time {
	lt := t.In(loc)
	midnight := time.Date(lt.Year(), lt.Month(), lt.Day(), 0, 0, 0, 0, loc)

	if period == oneWeekSeconds*time.Second {
		sinceMonday := (int(lt.Weekday()) + daysPerWeek - 1) % daysPerWeek

		return midnight.AddDate(0, 0, -sinceMonday)
	}

	if period == oneDaySeconds*time.Second {
		return midnight
	}

	elapsed := lt.Sub(midnight)

	return midnight.Add(elapsed - elapsed%period)
}

// wallEdges returns span+1 period boundaries, newest first. Period i covers
// [edges[i+1], edges[i]). edges[0] is the start of the period holding now,
// which has not ended and so is not evaluated.
func wallEdges(now time.Time, period time.Duration, span int, loc *time.Location) []time.Time {
	edges := make([]time.Time, span+1)
	edges[0] = wallFloor(now, period, loc)

	for i := 1; i <= span; i++ {
		edges[i] = wallFloor(edges[i-1].Add(-time.Nanosecond), period, loc)
	}

	return edges
}

// wallIndex is the period of ts in edges, or false when ts is outside them.
func wallIndex(edges []time.Time, ts time.Time) (int, bool) {
	if !ts.Before(edges[0]) || ts.Before(edges[len(edges)-1]) {
		return 0, false
	}

	// The first edge at or before ts closes the period that holds it.
	i := sort.Search(len(edges), func(k int) bool { return !edges[k].After(ts) })

	return i - 1, true
}
