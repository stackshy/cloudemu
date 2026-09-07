// Package scheduler provides an in-memory mock implementation of the Amazon
// EventBridge Scheduler control plane: standalone schedules and the schedule
// groups that contain them, plus a group's resource tags. A schedule or group
// is created directly usable with a stable arn, creation timestamp and (for a
// group) ACTIVE state; a schedule's target, flexible-time-window and start/end
// dates are carried verbatim so a round-tripped schedule reflects everything the
// caller sent. Firing a schedule at its target is out of scope: this is a
// control-plane-only surface.
package eventbridgescheduler

import (
	"strconv"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/eventbridgescheduler/driver"
)

// Compile-time check that Mock implements driver.Scheduler.
var _ driver.Scheduler = (*Mock)(nil)

// defaultMaxResults caps a page when the caller requests none.
const defaultMaxResults = 100

// arnMarker scopes an EventBridge Scheduler resource ARN.
const arnMarker = ":scheduler:"

// Mock is an in-memory implementation of the Amazon EventBridge Scheduler
// control plane. Schedules are keyed by "<group>/<name>" so a schedule name is
// unique only within its group; groups are keyed by name.
type Mock struct {
	schedules *memstore.Store[driver.Schedule]
	groups    *memstore.Store[driver.ScheduleGroup]
	opts      *config.Options
	// defaultGroupTime pins the timestamps of the always-present, unmaterialized
	// default group so repeated reads of it are byte-stable (minted once here,
	// not re-derived from the clock on every synthesis).
	defaultGroupTime time.Time
}

// New creates a new Scheduler mock with the given configuration options.
func New(opts *config.Options) *Mock {
	m := &Mock{
		schedules: memstore.New[driver.Schedule](),
		groups:    memstore.New[driver.ScheduleGroup](),
		opts:      opts,
	}
	m.defaultGroupTime = m.now()

	return m
}

func (m *Mock) now() time.Time {
	return m.opts.Clock.Now().UTC()
}

// scheduleKey is the store key for a schedule: its group and name together, so a
// name is unique within a group but may be reused across groups.
func scheduleKey(group, name string) string {
	return group + "/" + name
}

// scheduleARN mints the stable ARN for a schedule
// (arn:aws:scheduler:{region}:{acct}:schedule/{group}/{name}).
func (m *Mock) scheduleARN(group, name string) string {
	return idgen.AWSARN("scheduler", m.opts.Region, m.opts.AccountID, "schedule/"+group+"/"+name)
}

// groupARN mints the stable ARN for a schedule group
// (arn:aws:scheduler:{region}:{acct}:schedule-group/{name}).
func (m *Mock) groupARN(name string) string {
	return idgen.AWSARN("scheduler", m.opts.Region, m.opts.AccountID, "schedule-group/"+name)
}

// resolveGroup returns the group name a schedule operation targets, defaulting a
// blank name to the always-present default group.
func resolveGroup(name string) string {
	if name == "" {
		return driver.DefaultGroupName
	}

	return name
}

// groupExists reports whether a group is usable as a schedule's container. The
// default group always exists implicitly, even when never created.
func (m *Mock) groupExists(name string) bool {
	return name == driver.DefaultGroupName || m.groups.Has(name)
}

func copyTags(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}

	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}

	return out
}

func copyRaw(in []byte) []byte {
	if in == nil {
		return nil
	}

	return append([]byte(nil), in...)
}

// copySchedule returns an alias-free copy of a schedule so callers cannot mutate
// stored state through the result.
func copySchedule(s *driver.Schedule) driver.Schedule {
	out := *s
	out.StartDate = copyRaw(s.StartDate)
	out.EndDate = copyRaw(s.EndDate)
	out.FlexibleTimeWindow = copyRaw(s.FlexibleTimeWindow)
	out.Target = copyRaw(s.Target)

	return out
}

// copyGroup returns an alias-free copy of a group.
func copyGroup(g *driver.ScheduleGroup) driver.ScheduleGroup {
	out := *g
	out.Tags = copyTags(g.Tags)

	return out
}

// paginate returns the offset window and next token for a slice of length n,
// honoring an opaque numeric offset token.
func paginate(n int, page driver.Page) (start, end int, next string) {
	start = decodeToken(page.NextToken)
	if start > n {
		start = n
	}

	limit := int(page.MaxResults)
	if limit <= 0 {
		limit = defaultMaxResults
	}

	end = start + limit
	if end >= n {
		return start, n, ""
	}

	return start, end, encodeToken(end)
}

// encodeToken encodes a numeric list offset as an opaque pagination token.
func encodeToken(offset int) string {
	return strconv.Itoa(offset)
}

// decodeToken decodes an opaque pagination token to a numeric offset. An empty
// or malformed token decodes to 0 (start from the beginning).
func decodeToken(token string) int {
	if token == "" {
		return 0
	}

	n, err := strconv.Atoi(token)
	if err != nil || n < 0 {
		return 0
	}

	return n
}

// hasPrefix reports whether s begins with prefix, treating an empty prefix as a
// match-all filter.
func hasPrefix(s, prefix string) bool {
	return prefix == "" || strings.HasPrefix(s, prefix)
}
