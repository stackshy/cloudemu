package logging_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	ocilogging "github.com/stackshy/cloudemu/v2/providers/oci/logging"
	"github.com/stackshy/cloudemu/v2/services/logging/driver"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

const (
	compartmentA = "ocid1.compartment.oc1..aaaaaaaacompa"
	compartmentB = "ocid1.compartment.oc1..aaaaaaaacompb"
)

func newMock(t *testing.T) *ocilogging.Mock {
	t.Helper()

	return ocilogging.New(config.NewOptions(
		config.WithClock(config.NewFakeClock(time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC))),
		config.WithRegion("us-ashburn-1"),
		config.WithCompartmentID(compartmentA),
	))
}

// newGroup creates a log group and returns it, failing the test on error.
func newGroup(t *testing.T, m *ocilogging.Mock, compartmentID, name string) *ocilogging.LogGroup {
	t.Helper()

	g, err := m.CreateGroup(context.Background(), ocilogging.LogGroupSpec{
		CompartmentID: compartmentID,
		DisplayName:   name,
	})
	require.NoError(t, err)

	return g
}

// newCustomLog creates an enabled custom log in a group.
func newCustomLog(t *testing.T, m *ocilogging.Mock, groupID, name string) *ocilogging.Log {
	t.Helper()

	l, err := m.CreateLog(context.Background(), groupID, ocilogging.LogSpec{
		DisplayName: name,
		LogType:     ocilogging.LogTypeCustom,
		IsEnabled:   true,
	})
	require.NoError(t, err)

	return l
}

func TestCreateGroup(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name       string
		spec       ocilogging.LogGroupSpec
		existing   string
		expectCode cerrors.Code
	}{
		{
			name: "success",
			spec: ocilogging.LogGroupSpec{CompartmentID: compartmentA, DisplayName: "app-logs"},
		},
		{
			name: "compartment defaults to the configured one",
			spec: ocilogging.LogGroupSpec{DisplayName: "app-logs"},
		},
		{
			name:       "display name is required",
			spec:       ocilogging.LogGroupSpec{CompartmentID: compartmentA},
			expectCode: cerrors.InvalidArgument,
		},
		{
			name:       "already exists",
			spec:       ocilogging.LogGroupSpec{CompartmentID: compartmentA, DisplayName: "dup"},
			existing:   "dup",
			expectCode: cerrors.AlreadyExists,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newMock(t)

			if tc.existing != "" {
				newGroup(t, m, compartmentA, tc.existing)
			}

			g, err := m.CreateGroup(ctx, tc.spec)

			if tc.expectCode != cerrors.OK {
				require.Error(t, err)
				assert.Equal(t, tc.expectCode, cerrors.GetCode(err))

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.spec.DisplayName, g.DisplayName)
			assert.Equal(t, compartmentA, g.CompartmentID)
			assert.Equal(t, ocilogging.StateActive, g.LifecycleState)
			assert.NotEmpty(t, g.TimeCreated)
		})
	}
}

func TestLogGroupOCIDShape(t *testing.T) {
	m := newMock(t)
	g := newGroup(t, m, compartmentA, "app-logs")
	l := newCustomLog(t, m, g.ID, "stdout")

	assert.True(t, strings.HasPrefix(g.ID, "ocid1.loggroup.oc1.iad."), "got %q", g.ID)
	assert.True(t, strings.HasPrefix(l.ID, "ocid1.log.oc1.iad."), "got %q", l.ID)
}

func TestGetGroup(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)
	g := newGroup(t, m, compartmentA, "app-logs")

	t.Run("success", func(t *testing.T) {
		got, err := m.GetGroup(ctx, g.ID)
		require.NoError(t, err)
		assert.Equal(t, g.ID, got.ID)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := m.GetGroup(ctx, "ocid1.loggroup.oc1.iad.missing")
		require.Error(t, err)
		assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))
	})
}

func TestListGroupsFiltersByCompartment(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)
	newGroup(t, m, compartmentA, "in-a")
	newGroup(t, m, compartmentB, "in-b")

	tests := []struct {
		name        string
		compartment string
		displayName string
		expect      []string
		expectErr   bool
	}{
		{name: "compartment a", compartment: compartmentA, expect: []string{"in-a"}},
		{name: "compartment b", compartment: compartmentB, expect: []string{"in-b"}},
		{name: "unknown compartment lists nothing", compartment: "ocid1.compartment.oc1..zzz", expect: []string{}},
		{name: "narrowed by display name", compartment: compartmentA, displayName: "in-a", expect: []string{"in-a"}},
		{name: "display name that does not match", compartment: compartmentA, displayName: "nope", expect: []string{}},
		{name: "compartment is required", expectErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := m.ListGroups(ctx, tc.compartment, tc.displayName)

			if tc.expectErr {
				require.Error(t, err)
				assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))

				return
			}

			require.NoError(t, err)

			names := make([]string, 0, len(got))
			for _, g := range got {
				names = append(names, g.DisplayName)
			}

			assert.Equal(t, tc.expect, names)
		})
	}
}

func TestUpdateGroup(t *testing.T) {
	ctx := context.Background()
	rename := "renamed"

	t.Run("success", func(t *testing.T) {
		m := newMock(t)
		g := newGroup(t, m, compartmentA, "app-logs")

		got, err := m.UpdateGroup(ctx, g.ID, ocilogging.LogGroupUpdate{
			DisplayName:  &rename,
			FreeformTags: map[string]string{"env": "prod"},
		})
		require.NoError(t, err)
		assert.Equal(t, rename, got.DisplayName)
		assert.Equal(t, "prod", got.FreeformTags["env"])
	})

	t.Run("rename onto a taken name conflicts", func(t *testing.T) {
		m := newMock(t)
		g := newGroup(t, m, compartmentA, "app-logs")
		newGroup(t, m, compartmentA, rename)

		_, err := m.UpdateGroup(ctx, g.ID, ocilogging.LogGroupUpdate{DisplayName: &rename})
		require.Error(t, err)
		assert.Equal(t, cerrors.AlreadyExists, cerrors.GetCode(err))
	})

	t.Run("not found", func(t *testing.T) {
		m := newMock(t)

		_, err := m.UpdateGroup(ctx, "ocid1.loggroup.oc1.iad.missing", ocilogging.LogGroupUpdate{})
		require.Error(t, err)
		assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))
	})
}

func TestDeleteGroupRemovesItsLogs(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)
	g := newGroup(t, m, compartmentA, "app-logs")
	l := newCustomLog(t, m, g.ID, "stdout")

	require.NoError(t, m.DeleteGroup(ctx, g.ID))

	_, err := m.GetGroup(ctx, g.ID)
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	_, err = m.GetLog(ctx, g.ID, l.ID)
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(m.DeleteGroup(ctx, g.ID)))
}

func TestMoveGroupCarriesItsLogs(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)
	g := newGroup(t, m, compartmentA, "app-logs")
	l := newCustomLog(t, m, g.ID, "stdout")

	require.NoError(t, m.MoveGroup(ctx, g.ID, compartmentB))

	moved, err := m.GetGroup(ctx, g.ID)
	require.NoError(t, err)
	assert.Equal(t, compartmentB, moved.CompartmentID)

	movedLog, err := m.GetLog(ctx, g.ID, l.ID)
	require.NoError(t, err)
	assert.Equal(t, compartmentB, movedLog.CompartmentID)

	inA, err := m.ListGroups(ctx, compartmentA, "")
	require.NoError(t, err)
	assert.Empty(t, inA)
}

func TestCreateLog(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name       string
		spec       ocilogging.LogSpec
		existing   string
		group      string
		expectCode cerrors.Code
	}{
		{
			name: "custom log",
			spec: ocilogging.LogSpec{DisplayName: "stdout", LogType: ocilogging.LogTypeCustom, IsEnabled: true},
		},
		{
			name: "log type defaults to custom",
			spec: ocilogging.LogSpec{DisplayName: "stdout", IsEnabled: true},
		},
		{
			name: "service log with a source",
			spec: ocilogging.LogSpec{
				DisplayName: "flowlogs",
				LogType:     ocilogging.LogTypeService,
				Configuration: &ocilogging.LogConfiguration{
					Source: ocilogging.LogSource{Service: "flowlogs", Resource: "ocid1.subnet.oc1.iad.a"},
				},
			},
		},
		{
			name:       "service log without a source",
			spec:       ocilogging.LogSpec{DisplayName: "flowlogs", LogType: ocilogging.LogTypeService},
			expectCode: cerrors.InvalidArgument,
		},
		{
			name:       "unknown log type",
			spec:       ocilogging.LogSpec{DisplayName: "stdout", LogType: "WEIRD"},
			expectCode: cerrors.InvalidArgument,
		},
		{
			name:       "display name is required",
			spec:       ocilogging.LogSpec{},
			expectCode: cerrors.InvalidArgument,
		},
		{
			name:       "already exists in the group",
			spec:       ocilogging.LogSpec{DisplayName: "stdout"},
			existing:   "stdout",
			expectCode: cerrors.AlreadyExists,
		},
		{
			name:       "unknown log group",
			spec:       ocilogging.LogSpec{DisplayName: "stdout"},
			group:      "ocid1.loggroup.oc1.iad.missing",
			expectCode: cerrors.NotFound,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newMock(t)
			g := newGroup(t, m, compartmentA, "app-logs")

			if tc.existing != "" {
				newCustomLog(t, m, g.ID, tc.existing)
			}

			groupID := g.ID
			if tc.group != "" {
				groupID = tc.group
			}

			l, err := m.CreateLog(ctx, groupID, tc.spec)

			if tc.expectCode != cerrors.OK {
				require.Error(t, err)
				assert.Equal(t, tc.expectCode, cerrors.GetCode(err))

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.spec.DisplayName, l.DisplayName)
			assert.Equal(t, g.ID, l.LogGroupID)
			assert.Equal(t, compartmentA, l.CompartmentID)
			assert.Equal(t, 30, l.RetentionDuration)
		})
	}
}

func TestGetLogThroughTheWrongGroupIsNotFound(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)
	g := newGroup(t, m, compartmentA, "app-logs")
	other := newGroup(t, m, compartmentA, "other-logs")
	l := newCustomLog(t, m, g.ID, "stdout")

	_, err := m.GetLog(ctx, other.ID, l.ID)
	require.Error(t, err)
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))
}

func TestListLogs(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)
	g := newGroup(t, m, compartmentA, "app-logs")
	newCustomLog(t, m, g.ID, "stdout")

	_, err := m.CreateLog(ctx, g.ID, ocilogging.LogSpec{
		DisplayName: "flowlogs",
		LogType:     ocilogging.LogTypeService,
		Configuration: &ocilogging.LogConfiguration{
			Source: ocilogging.LogSource{Service: "flowlogs", Resource: "ocid1.subnet.oc1.iad.a"},
		},
	})
	require.NoError(t, err)

	tests := []struct {
		name   string
		filter ocilogging.LogFilter
		expect int
	}{
		{name: "unfiltered", expect: 2},
		{name: "by display name", filter: ocilogging.LogFilter{DisplayName: "stdout"}, expect: 1},
		{name: "by log type", filter: ocilogging.LogFilter{LogType: ocilogging.LogTypeService}, expect: 1},
		{name: "by source service", filter: ocilogging.LogFilter{SourceService: "flowlogs"}, expect: 1},
		{name: "by source resource", filter: ocilogging.LogFilter{SourceResource: "ocid1.subnet.oc1.iad.a"}, expect: 1},
		{name: "by lifecycle state", filter: ocilogging.LogFilter{LifecycleState: ocilogging.StateActive}, expect: 2},
		{name: "no match", filter: ocilogging.LogFilter{DisplayName: "nope"}, expect: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := m.ListLogs(ctx, g.ID, tc.filter)
			require.NoError(t, err)
			assert.Len(t, got, tc.expect)
		})
	}

	t.Run("unknown log group", func(t *testing.T) {
		_, err := m.ListLogs(ctx, "ocid1.loggroup.oc1.iad.missing", ocilogging.LogFilter{})
		require.Error(t, err)
		assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))
	})
}

func TestUpdateAndDeleteLog(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)
	g := newGroup(t, m, compartmentA, "app-logs")
	l := newCustomLog(t, m, g.ID, "stdout")

	disabled := false
	retention := 90

	updated, err := m.UpdateLog(ctx, g.ID, l.ID, ocilogging.LogUpdate{
		IsEnabled:         &disabled,
		RetentionDuration: &retention,
	})
	require.NoError(t, err)
	assert.False(t, updated.IsEnabled)
	assert.Equal(t, 90, updated.RetentionDuration)

	require.NoError(t, m.DeleteLog(ctx, g.ID, l.ID))
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(m.DeleteLog(ctx, g.ID, l.ID)))
}

func TestPutLogs(t *testing.T) {
	ctx := context.Background()
	when := time.Date(2026, 8, 8, 11, 0, 0, 0, time.UTC)

	t.Run("success", func(t *testing.T) {
		m := newMock(t)
		g := newGroup(t, m, compartmentA, "app-logs")
		l := newCustomLog(t, m, g.ID, "stdout")

		require.NoError(t, m.PutLogs(ctx, l.ID, []ocilogging.LogEntryBatch{{
			Source: "host-a",
			Type:   "custom",
			Entries: []ocilogging.LogEntryItem{
				{Data: "first", Time: when},
				{Data: "second"},
			},
			DefaultLogEntryTime: when.Add(time.Minute),
		}}))

		entries, err := m.Entries(ctx, l.ID)
		require.NoError(t, err)
		require.Len(t, entries, 2)
		assert.Equal(t, "first", entries[0].Data)
		assert.Equal(t, when, entries[0].Time)
		assert.Equal(t, when.Add(time.Minute), entries[1].Time, "an entry with no time takes the batch default")
		assert.NotEmpty(t, entries[0].ID, "an entry with no id is given one")
		assert.Equal(t, "host-a", entries[0].Source)
	})

	t.Run("unknown log", func(t *testing.T) {
		m := newMock(t)

		err := m.PutLogs(ctx, "ocid1.log.oc1.iad.missing", nil)
		require.Error(t, err)
		assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))
	})

	t.Run("a service log is fed by its service, not by PutLogs", func(t *testing.T) {
		m := newMock(t)
		g := newGroup(t, m, compartmentA, "app-logs")

		l, err := m.CreateLog(ctx, g.ID, ocilogging.LogSpec{
			DisplayName: "flowlogs",
			LogType:     ocilogging.LogTypeService,
			IsEnabled:   true,
			Configuration: &ocilogging.LogConfiguration{
				Source: ocilogging.LogSource{Service: "flowlogs", Resource: "ocid1.subnet.oc1.iad.a"},
			},
		})
		require.NoError(t, err)

		err = m.PutLogs(ctx, l.ID, nil)
		require.Error(t, err)
		assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))
	})

	t.Run("a disabled log is refused rather than silently dropping entries", func(t *testing.T) {
		m := newMock(t)
		g := newGroup(t, m, compartmentA, "app-logs")

		l, err := m.CreateLog(ctx, g.ID, ocilogging.LogSpec{DisplayName: "stdout"})
		require.NoError(t, err)

		err = m.PutLogs(ctx, l.ID, []ocilogging.LogEntryBatch{{Entries: []ocilogging.LogEntryItem{{Data: "x"}}}})
		require.Error(t, err)
		assert.Equal(t, cerrors.FailedPrecondition, cerrors.GetCode(err))
	})
}

// Portable driver projection.

func TestPortableLogGroupCRUD(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	info, err := m.CreateLogGroup(ctx, driver.LogGroupConfig{
		Name:          "portable",
		RetentionDays: 14,
		Tags:          map[string]string{"env": "dev"},
		Scope:         scope.Scope{Compartment: compartmentB},
	})
	require.NoError(t, err)
	assert.Equal(t, 14, info.RetentionDays)
	assert.Equal(t, compartmentB, info.Scope.Compartment)
	assert.True(t, strings.HasPrefix(info.ResourceID, "ocid1.loggroup."))

	_, err = m.CreateLogGroup(ctx, driver.LogGroupConfig{
		Name:  "portable",
		Scope: scope.Scope{Compartment: compartmentB},
	})
	assert.Equal(t, cerrors.AlreadyExists, cerrors.GetCode(err))

	got, err := m.GetLogGroup(ctx, "portable")
	require.NoError(t, err)
	assert.Equal(t, info.ResourceID, got.ResourceID)

	_, err = m.GetLogGroup(ctx, "missing")
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	updated, err := m.UpdateLogGroup(ctx, driver.LogGroupConfig{Name: "portable", RetentionDays: 60})
	require.NoError(t, err)
	assert.Equal(t, 60, updated.RetentionDays)

	_, err = m.UpdateLogGroup(ctx, driver.LogGroupConfig{Name: "missing"})
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	require.NoError(t, m.DeleteLogGroup(ctx, "portable"))
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(m.DeleteLogGroup(ctx, "portable")))
}

func TestPortableListLogGroupsFiltersByCompartment(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)
	newGroup(t, m, compartmentA, "in-a")
	newGroup(t, m, compartmentB, "in-b")

	got, err := m.ListLogGroups(ctx, scope.Scope{Compartment: compartmentB})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "in-b", got[0].Name)

	all, err := m.ListLogGroups(ctx, scope.Scope{})
	require.NoError(t, err)
	assert.Len(t, all, 2)
}

func TestPortableStreamsAndEvents(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	_, err := m.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "app-logs"})
	require.NoError(t, err)

	_, err = m.CreateLogStream(ctx, "app-logs", "stdout")
	require.NoError(t, err)

	_, err = m.CreateLogStream(ctx, "app-logs", "stdout")
	assert.Equal(t, cerrors.AlreadyExists, cerrors.GetCode(err))

	_, err = m.CreateLogStream(ctx, "missing", "stdout")
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	base := time.Date(2026, 8, 8, 10, 0, 0, 0, time.UTC)
	require.NoError(t, m.PutLogEvents(ctx, "app-logs", "stdout", []driver.LogEvent{
		{Timestamp: base, Message: "hello world"},
		{Timestamp: base.Add(time.Hour), Message: "goodbye"},
	}))

	streams, err := m.ListLogStreams(ctx, "app-logs")
	require.NoError(t, err)
	require.Len(t, streams, 1)
	assert.NotEmpty(t, streams[0].LastEvent)

	group, err := m.GetLogGroup(ctx, "app-logs")
	require.NoError(t, err)
	assert.Equal(t, int64(len("hello world")+len("goodbye")), group.StoredBytes)

	events, err := m.GetLogEvents(ctx, &driver.LogQueryInput{LogGroup: "app-logs", Pattern: "hello"})
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "hello world", events[0].Message)

	windowed, err := m.GetLogEvents(ctx, &driver.LogQueryInput{
		LogGroup:  "app-logs",
		LogStream: "stdout",
		StartTime: base.Add(30 * time.Minute),
	})
	require.NoError(t, err)
	assert.Len(t, windowed, 1)

	filtered, err := m.FilterLogEvents(ctx, &driver.FilterLogEventsInput{
		LogGroup:      "app-logs",
		FilterPattern: "goodbye",
	})
	require.NoError(t, err)
	require.Len(t, filtered, 1)
	assert.Equal(t, "stdout", filtered[0].LogStream)

	_, err = m.FilterLogEvents(ctx, &driver.FilterLogEventsInput{LogGroup: "missing"})
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	require.NoError(t, m.DeleteLogStream(ctx, "app-logs", "stdout"))
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(m.DeleteLogStream(ctx, "app-logs", "stdout")))
}

func TestMetricFiltersAreNotAnOCIOperation(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	err := m.PutMetricFilter(ctx, &driver.MetricFilterConfig{Name: "errors"})
	require.Error(t, err)
	assert.Equal(t, cerrors.Unimplemented, cerrors.GetCode(err))
	assert.Contains(t, err.Error(), "Service Connector")

	assert.Equal(t, cerrors.Unimplemented, cerrors.GetCode(m.DeleteMetricFilter(ctx, "g", "errors")))

	_, err = m.DescribeMetricFilters(ctx, "g")
	assert.Equal(t, cerrors.Unimplemented, cerrors.GetCode(err))
}

// Per-compartment display-name uniqueness.

func TestGroupNamesAreUniquePerCompartment(t *testing.T) {
	ctx := context.Background()

	t.Run("the same name in two compartments is allowed", func(t *testing.T) {
		m := newMock(t)
		a := newGroup(t, m, compartmentA, "shared")
		b := newGroup(t, m, compartmentB, "shared")

		assert.NotEqual(t, a.ID, b.ID)
	})

	t.Run("rename onto a sibling in the same compartment is rejected", func(t *testing.T) {
		m := newMock(t)
		newGroup(t, m, compartmentA, "taken")
		g := newGroup(t, m, compartmentA, "free")

		name := "taken"
		_, err := m.UpdateGroup(ctx, g.ID, ocilogging.LogGroupUpdate{DisplayName: &name})
		require.Error(t, err)
		assert.Equal(t, cerrors.AlreadyExists, cerrors.GetCode(err))
	})

	t.Run("rename onto a name held in another compartment is allowed", func(t *testing.T) {
		m := newMock(t)
		newGroup(t, m, compartmentB, "taken")
		g := newGroup(t, m, compartmentA, "free")

		name := "taken"
		updated, err := m.UpdateGroup(ctx, g.ID, ocilogging.LogGroupUpdate{DisplayName: &name})
		require.NoError(t, err)
		assert.Equal(t, "taken", updated.DisplayName)
	})

	t.Run("moving onto a name the destination already holds is rejected", func(t *testing.T) {
		m := newMock(t)
		newGroup(t, m, compartmentB, "shared")
		g := newGroup(t, m, compartmentA, "shared")

		err := m.MoveGroup(ctx, g.ID, compartmentB)
		require.Error(t, err)
		assert.Equal(t, cerrors.AlreadyExists, cerrors.GetCode(err))
	})

	t.Run("moving a group to its own compartment is a no-op", func(t *testing.T) {
		m := newMock(t)
		g := newGroup(t, m, compartmentA, "stays")

		require.NoError(t, m.MoveGroup(ctx, g.ID, compartmentA))
	})
}

func TestPortableRejectsAnAmbiguousGroupName(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)
	newGroup(t, m, compartmentA, "shared")
	newGroup(t, m, compartmentB, "shared")

	_, err := m.GetLogGroup(ctx, "shared")
	require.Error(t, err)
	assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))
	assert.Contains(t, err.Error(), "more than one compartment")
}

func TestPortableUpdateLogGroupMoveCollides(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)
	newGroup(t, m, compartmentB, "shared")
	newGroup(t, m, compartmentA, "moving")

	_, err := m.UpdateLogGroup(ctx, driver.LogGroupConfig{
		Name:  "moving",
		Scope: scope.Scope{Compartment: compartmentB},
	})
	require.NoError(t, err, "no collision on a free name")

	newGroup(t, m, compartmentA, "shared2")
	newGroup(t, m, compartmentB, "shared2")

	_, err = m.UpdateLogGroup(ctx, driver.LogGroupConfig{Name: "shared2"})
	assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err), "an ambiguous name is rejected")
}

// Read limits.

func TestPortableReadLimitIsBounded(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name       string
		limit      int
		expectCode cerrors.Code
	}{
		{name: "unset falls back to the default", limit: 0},
		{name: "in range", limit: 10},
		{name: "negative", limit: -1, expectCode: cerrors.InvalidArgument},
		{name: "above the maximum", limit: 10_001, expectCode: cerrors.InvalidArgument},
		{name: "absurd", limit: 1 << 40, expectCode: cerrors.InvalidArgument},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newMock(t)
			g := newGroup(t, m, compartmentA, "app-logs")
			newCustomLog(t, m, g.ID, "stdout")

			_, getErr := m.GetLogEvents(ctx, &driver.LogQueryInput{LogGroup: "app-logs", Limit: tc.limit})
			_, filterErr := m.FilterLogEvents(ctx, &driver.FilterLogEventsInput{LogGroup: "app-logs", Limit: tc.limit})

			searchErr := searchWithLimit(t, m, tc.limit)

			if tc.expectCode == cerrors.OK {
				require.NoError(t, getErr)
				require.NoError(t, filterErr)
				require.NoError(t, searchErr)

				return
			}

			assert.Equal(t, tc.expectCode, cerrors.GetCode(getErr))
			assert.Equal(t, tc.expectCode, cerrors.GetCode(filterErr))
			assert.Equal(t, tc.expectCode, cerrors.GetCode(searchErr))
		})
	}
}

// searchWithLimit runs a search over the whole window with the given limit.
func searchWithLimit(t *testing.T, m *ocilogging.Mock, limit int) error {
	t.Helper()

	_, err := m.SearchLogs(context.Background(), ocilogging.SearchRequest{
		Query:     `search "` + compartmentA + `"`,
		TimeStart: searchWindowStart,
		TimeEnd:   searchWindowEnd,
		Limit:     limit,
	})

	return err
}

func TestPortableReadLimitTruncates(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)
	g := newGroup(t, m, compartmentA, "app-logs")
	l := newCustomLog(t, m, g.ID, "stdout")

	entries := make([]ocilogging.LogEntryItem, 5)
	for i := range entries {
		entries[i] = ocilogging.LogEntryItem{Data: "line-" + strconv.Itoa(i), Time: searchWindowStart}
	}

	require.NoError(t, m.PutLogs(ctx, l.ID, []ocilogging.LogEntryBatch{{Entries: entries}}))

	events, err := m.GetLogEvents(ctx, &driver.LogQueryInput{LogGroup: "app-logs", Limit: 2})
	require.NoError(t, err)
	assert.Len(t, events, 2)

	filtered, err := m.FilterLogEvents(ctx, &driver.FilterLogEventsInput{LogGroup: "app-logs", Limit: 3})
	require.NoError(t, err)
	assert.Len(t, filtered, 3)
}

// loggingsearch.

// The window every search test runs over.
//
//nolint:gochecknoglobals // fixed test window.
var (
	searchWindowStart = time.Date(2026, 8, 8, 10, 0, 0, 0, time.UTC)
	searchWindowEnd   = time.Date(2026, 8, 8, 13, 0, 0, 0, time.UTC)
)

// searchFixture is a mock holding two groups in two compartments, each with one
// log, and entries whose times are deliberately out of insertion order.
type searchFixture struct {
	m       *ocilogging.Mock
	groupA  *ocilogging.LogGroup
	groupB  *ocilogging.LogGroup
	stdout  *ocilogging.Log
	stderr  *ocilogging.Log
	otherIn *ocilogging.Log
}

func newSearchFixture(t *testing.T) *searchFixture {
	t.Helper()

	ctx := context.Background()
	m := newMock(t)

	f := &searchFixture{m: m}
	f.groupA = newGroup(t, m, compartmentA, "app-logs")
	f.groupB = newGroup(t, m, compartmentB, "other-logs")
	f.stdout = newCustomLog(t, m, f.groupA.ID, "stdout")
	f.stderr = newCustomLog(t, m, f.groupA.ID, "stderr")
	f.otherIn = newCustomLog(t, m, f.groupB.ID, "audit")

	at := func(min int) time.Time { return searchWindowStart.Add(time.Duration(min) * time.Minute) }

	require.NoError(t, m.PutLogs(ctx, f.stdout.ID, []ocilogging.LogEntryBatch{{
		Source:  "host-a",
		Type:    "com.oraclecloud.custom",
		Subject: "app",
		Entries: []ocilogging.LogEntryItem{
			{ID: "e-30", Data: `{"level":"info","code":200}`, Time: at(30)},
			{ID: "e-10", Data: `{"level":"error","code":500}`, Time: at(10)},
			{ID: "e-20", Data: "plain text line", Time: at(20)},
		},
	}}))

	require.NoError(t, m.PutLogs(ctx, f.stderr.ID, []ocilogging.LogEntryBatch{{
		Source:  "host-b",
		Type:    "com.oraclecloud.custom",
		Subject: "sidecar",
		Entries: []ocilogging.LogEntryItem{{ID: "e-40", Data: `{"level":"warn"}`, Time: at(40)}},
	}}))

	require.NoError(t, m.PutLogs(ctx, f.otherIn.ID, []ocilogging.LogEntryBatch{{
		Source:  "host-c",
		Entries: []ocilogging.LogEntryItem{{ID: "e-50", Data: "audit line", Time: at(50)}},
	}}))

	return f
}

// search runs a query over the fixture window and returns the matched entry ids
// in result order.
func (f *searchFixture) search(t *testing.T, query string) []string {
	t.Helper()

	res, err := f.m.SearchLogs(context.Background(), ocilogging.SearchRequest{
		Query:     query,
		TimeStart: searchWindowStart,
		TimeEnd:   searchWindowEnd,
	})
	require.NoError(t, err)

	ids := make([]string, 0, len(res.Entries))
	for i := range res.Entries {
		ids = append(ids, res.Entries[i].ID)
	}

	return ids
}

func TestSearchScopes(t *testing.T) {
	f := newSearchFixture(t)

	tests := []struct {
		name   string
		query  string
		expect []string
	}{
		{
			name:   "whole compartment",
			query:  `search "` + compartmentA + `"`,
			expect: []string{"e-10", "e-20", "e-30", "e-40"},
		},
		{
			name:   "narrowed to a log group",
			query:  `search "` + compartmentA + `/` + f.groupA.ID + `"`,
			expect: []string{"e-10", "e-20", "e-30", "e-40"},
		},
		{
			name:   "narrowed to one log",
			query:  `search "` + compartmentA + `/` + f.groupA.ID + `/` + f.stderr.ID + `"`,
			expect: []string{"e-40"},
		},
		{
			name:   "two targets",
			query:  `search "` + compartmentA + `/` + f.groupA.ID + `/` + f.stderr.ID + `", "` + compartmentB + `"`,
			expect: []string{"e-40", "e-50"},
		},
		{
			name:   "a log group in the wrong compartment matches nothing",
			query:  `search "` + compartmentB + `/` + f.groupA.ID + `"`,
			expect: []string{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expect, f.search(t, tc.query))
		})
	}
}

func TestSearchSortOrder(t *testing.T) {
	f := newSearchFixture(t)
	all := `search "` + compartmentA + `"`

	tests := []struct {
		name   string
		query  string
		expect []string
	}{
		{
			name:   "default is ascending by time",
			query:  all,
			expect: []string{"e-10", "e-20", "e-30", "e-40"},
		},
		{
			name:   "sort by datetime asc",
			query:  all + ` | sort by datetime asc`,
			expect: []string{"e-10", "e-20", "e-30", "e-40"},
		},
		{
			name:   "sort by datetime desc",
			query:  all + ` | sort by datetime desc`,
			expect: []string{"e-40", "e-30", "e-20", "e-10"},
		},
		{
			name:   "sort by time desc",
			query:  all + ` | sort by time desc`,
			expect: []string{"e-40", "e-30", "e-20", "e-10"},
		},
		{
			name:   "sort by logContent.datetime desc",
			query:  all + ` | sort by logContent.datetime desc`,
			expect: []string{"e-40", "e-30", "e-20", "e-10"},
		},
		{
			name:   "sort with no direction is ascending",
			query:  all + ` | sort by datetime`,
			expect: []string{"e-10", "e-20", "e-30", "e-40"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expect, f.search(t, tc.query))
		})
	}
}

func TestSearchSortIsStableOnEqualTimes(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)
	g := newGroup(t, m, compartmentA, "app-logs")
	l := newCustomLog(t, m, g.ID, "stdout")

	same := searchWindowStart.Add(time.Minute)
	require.NoError(t, m.PutLogs(ctx, l.ID, []ocilogging.LogEntryBatch{{Entries: []ocilogging.LogEntryItem{
		{ID: "e-c", Data: "c", Time: same},
		{ID: "e-a", Data: "a", Time: same},
		{ID: "e-b", Data: "b", Time: same},
	}}}))

	res, err := m.SearchLogs(ctx, ocilogging.SearchRequest{
		Query:     `search "` + compartmentA + `" | sort by datetime desc`,
		TimeStart: searchWindowStart,
		TimeEnd:   searchWindowEnd,
	})
	require.NoError(t, err)

	ids := make([]string, 0, len(res.Entries))
	for i := range res.Entries {
		ids = append(ids, res.Entries[i].ID)
	}

	assert.Equal(t, []string{"e-c", "e-b", "e-a"}, ids, "entries at the same time break the tie on id")
}

func TestSearchWhereFields(t *testing.T) {
	f := newSearchFixture(t)
	all := `search "` + compartmentA + `"`

	tests := []struct {
		name   string
		query  string
		expect []string
	}{
		{
			name:   "oracle.logid",
			query:  all + ` | where oracle.logid = '` + f.stderr.ID + `'`,
			expect: []string{"e-40"},
		},
		{
			name:   "oracle.loggroupid",
			query:  all + ` | where oracle.loggroupid = '` + f.groupA.ID + `'`,
			expect: []string{"e-10", "e-20", "e-30", "e-40"},
		},
		{
			name:   "oracle.compartmentid",
			query:  all + ` | where oracle.compartmentid = '` + compartmentA + `'`,
			expect: []string{"e-10", "e-20", "e-30", "e-40"},
		},
		{
			name:   "oracle.compartmentid mismatch",
			query:  all + ` | where oracle.compartmentid = '` + compartmentB + `'`,
			expect: []string{},
		},
		{
			name:   "oracle.ingestedtime is stamped by the clock",
			query:  all + ` | where oracle.ingestedtime = '2026-08-08T12:00:00Z'`,
			expect: []string{"e-10", "e-20", "e-30", "e-40"},
		},
		{
			name:   "logContent.oracle prefix is accepted",
			query:  all + ` | where logContent.oracle.logid = '` + f.stderr.ID + `'`,
			expect: []string{"e-40"},
		},
		{
			name:   "negated oracle.logid",
			query:  all + ` | where oracle.logid != '` + f.stderr.ID + `'`,
			expect: []string{"e-10", "e-20", "e-30"},
		},
		{
			name:   "id",
			query:  all + ` | where id = 'e-20'`,
			expect: []string{"e-20"},
		},
		{
			name:   "source",
			query:  all + ` | where source = 'host-b'`,
			expect: []string{"e-40"},
		},
		{
			name:   "subject",
			query:  all + ` | where subject = 'sidecar'`,
			expect: []string{"e-40"},
		},
		{
			name:   "type wildcard",
			query:  all + ` | where type = 'com.oraclecloud.*'`,
			expect: []string{"e-10", "e-20", "e-30", "e-40"},
		},
		{
			name:   "datetime",
			query:  all + ` | where datetime = '2026-08-08T10:20:00Z'`,
			expect: []string{"e-20"},
		},
		{
			name:   "time",
			query:  all + ` | where time = '2026-08-08T10:40:00Z'`,
			expect: []string{"e-40"},
		},
		{
			name:   "data wildcard",
			query:  all + ` | where data = '*plain*'`,
			expect: []string{"e-20"},
		},
		{
			name:   "data.<key> of a JSON payload",
			query:  all + ` | where data.level = 'error'`,
			expect: []string{"e-10"},
		},
		{
			name:   "data.<key> that is not a string",
			query:  all + ` | where data.code = '500'`,
			expect: []string{"e-10"},
		},
		{
			name:   "data.<key> missing from a payload matches nothing",
			query:  all + ` | where data.missing = 'x'`,
			expect: []string{},
		},
		{
			name:   "two comparisons joined by and",
			query:  all + ` | where data.level = 'info' and source = 'host-a'`,
			expect: []string{"e-30"},
		},
		{
			name:   "where and sort together",
			query:  all + ` | where type = 'com.oraclecloud.*' | sort by datetime desc`,
			expect: []string{"e-40", "e-30", "e-20", "e-10"},
		},
		{
			name:   "an unquoted value is compared literally",
			query:  all + ` | where source = host-b`,
			expect: []string{"e-40"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expect, f.search(t, tc.query))
		})
	}
}

func TestSearchTimeWindowAndFieldInfo(t *testing.T) {
	ctx := context.Background()
	f := newSearchFixture(t)

	t.Run("the window is half-open", func(t *testing.T) {
		res, err := f.m.SearchLogs(ctx, ocilogging.SearchRequest{
			Query:     `search "` + compartmentA + `"`,
			TimeStart: searchWindowStart.Add(20 * time.Minute),
			TimeEnd:   searchWindowStart.Add(40 * time.Minute),
		})
		require.NoError(t, err)
		require.Len(t, res.Entries, 2)
		assert.Equal(t, "e-20", res.Entries[0].ID, "start is inclusive")
		assert.Equal(t, "e-30", res.Entries[1].ID, "end is exclusive")
	})

	t.Run("field info is returned on request", func(t *testing.T) {
		res, err := f.m.SearchLogs(ctx, ocilogging.SearchRequest{
			Query:           `search "` + compartmentA + `"`,
			TimeStart:       searchWindowStart,
			TimeEnd:         searchWindowEnd,
			ReturnFieldInfo: true,
		})
		require.NoError(t, err)
		assert.NotEmpty(t, res.Fields)
		assert.Equal(t, "datetime", res.Fields[0].Name)
	})

	t.Run("field info is withheld otherwise", func(t *testing.T) {
		res, err := f.m.SearchLogs(ctx, ocilogging.SearchRequest{
			Query:     `search "` + compartmentA + `"`,
			TimeStart: searchWindowStart,
			TimeEnd:   searchWindowEnd,
		})
		require.NoError(t, err)
		assert.Empty(t, res.Fields)
	})

	t.Run("the limit truncates after sorting", func(t *testing.T) {
		res, err := f.m.SearchLogs(ctx, ocilogging.SearchRequest{
			Query:     `search "` + compartmentA + `" | sort by datetime desc`,
			TimeStart: searchWindowStart,
			TimeEnd:   searchWindowEnd,
			Limit:     2,
		})
		require.NoError(t, err)
		require.Len(t, res.Entries, 2)
		assert.Equal(t, "e-40", res.Entries[0].ID)
		assert.Equal(t, "e-30", res.Entries[1].ID)
	})

	t.Run("an entry carries the log it came from", func(t *testing.T) {
		res, err := f.m.SearchLogs(ctx, ocilogging.SearchRequest{
			Query:     `search "` + compartmentA + `/` + f.groupA.ID + `/` + f.stderr.ID + `"`,
			TimeStart: searchWindowStart,
			TimeEnd:   searchWindowEnd,
		})
		require.NoError(t, err)
		require.Len(t, res.Entries, 1)
		assert.Equal(t, compartmentA, res.Entries[0].CompartmentID)
		assert.Equal(t, f.groupA.ID, res.Entries[0].LogGroupID)
		assert.Equal(t, "stderr", res.Entries[0].LogName)
	})
}

func TestSearchRejectsWhatItDoesNotModel(t *testing.T) {
	f := newSearchFixture(t)
	all := `search "` + compartmentA + `"`

	tests := []struct {
		name     string
		query    string
		contains string
	}{
		{name: "empty query", query: "", contains: "searchQuery is required"},
		{name: "blank first stage", query: "   ", contains: "searchQuery is required"},
		{name: "does not begin with search", query: `where id = 'x'`, contains: "must begin with the search clause"},
		{name: "no target", query: `search`, contains: "names no target"},
		{name: "unquoted target", query: `search ` + compartmentA, contains: "must be quoted"},
		{
			name:     "too many segments",
			query:    `search "` + compartmentA + `/a/b/c"`,
			contains: "expected compartmentId",
		},
		{
			name:     "a name in place of a compartment OCID",
			query:    `search "my-compartment"`,
			contains: "is not a compartment OCID",
		},
		{
			name:     "a name in place of a log group OCID",
			query:    `search "` + compartmentA + `/app-logs"`,
			contains: "is not a log group OCID",
		},
		{
			name:     "a name in place of a log OCID",
			query:    `search "` + compartmentA + `/` + f.groupA.ID + `/stdout"`,
			contains: "is not a log OCID",
		},
		{name: "empty stage", query: all + ` | `, contains: "empty stage"},
		{name: "unsupported stage", query: all + ` | stats count()`, contains: "unsupported search operator"},
		{name: "sort without by", query: all + ` | sort datetime`, contains: "sort must be written as"},
		{name: "sort by an unmodelled field", query: all + ` | sort by source`, contains: "sorts by datetime only"},
		{name: "bad sort direction", query: all + ` | sort by datetime sideways`, contains: "is not asc or desc"},
		{
			name:     "sort with trailing tokens",
			query:    all + ` | sort by datetime desc then more`,
			contains: "single field and an optional direction",
		},
		{name: "parenthesized where", query: all + ` | where (id = 'a')`, contains: "parenthesized"},
		{name: "or in a where", query: all + ` | where id = 'a' or id = 'b'`, contains: `"or" operator is not modeled`},
		{name: "not in a where", query: all + ` | where not id = 'a'`, contains: `"not" operator is not modeled`},
		{name: "where with no comparison", query: all + ` | where `, contains: "empty comparison"},
		{name: "comparison with no operator", query: all + ` | where id`, contains: "has no operator"},
		{name: "unmodelled operator", query: all + ` | where id ~ 'a'`, contains: "is not modeled"},
		{name: "comparison naming no field", query: all + ` | where = 'a'`, contains: "names no field"},
		{name: "comparison with no value", query: all + ` | where id =`, contains: "has no value"},
		{name: "unmodelled field", query: all + ` | where nope = 'a'`, contains: "unsupported search field"},
		{name: "nested data path", query: all + ` | where data.a.b = 'x'`, contains: "not a nested path"},
		{name: "bare data prefix", query: all + ` | where data. = 'x'`, contains: "not a nested path"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.m.SearchLogs(context.Background(), ocilogging.SearchRequest{
				Query:     tc.query,
				TimeStart: searchWindowStart,
				TimeEnd:   searchWindowEnd,
			})
			require.Error(t, err)
			assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))
			assert.Contains(t, err.Error(), tc.contains)
		})
	}
}

func TestSearchRequiresATimeRange(t *testing.T) {
	ctx := context.Background()
	f := newSearchFixture(t)
	query := `search "` + compartmentA + `"`

	tests := []struct {
		name       string
		start, end time.Time
		contains   string
	}{
		{name: "no start", end: searchWindowEnd, contains: "are required"},
		{name: "no end", start: searchWindowStart, contains: "are required"},
		{
			name:     "end before start",
			start:    searchWindowEnd,
			end:      searchWindowStart,
			contains: "must be after",
		},
		{
			name:     "end equal to start",
			start:    searchWindowStart,
			end:      searchWindowStart,
			contains: "must be after",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.m.SearchLogs(ctx, ocilogging.SearchRequest{
				Query: query, TimeStart: tc.start, TimeEnd: tc.end,
			})
			require.Error(t, err)
			assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))
			assert.Contains(t, err.Error(), tc.contains)
		})
	}
}

func TestUpdateLogFields(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)
	g := newGroup(t, m, compartmentA, "app-logs")
	l := newCustomLog(t, m, g.ID, "stdout")
	newCustomLog(t, m, g.ID, "stderr")

	name := "renamed"
	tags := map[string]string{"env": "dev"}

	updated, err := m.UpdateLog(ctx, g.ID, l.ID, ocilogging.LogUpdate{
		DisplayName:  &name,
		FreeformTags: tags,
	})
	require.NoError(t, err)
	assert.Equal(t, "renamed", updated.DisplayName)
	assert.Equal(t, tags, updated.FreeformTags)

	taken := "stderr"
	_, err = m.UpdateLog(ctx, g.ID, l.ID, ocilogging.LogUpdate{DisplayName: &taken})
	require.Error(t, err)
	assert.Equal(t, cerrors.AlreadyExists, cerrors.GetCode(err))

	same := "renamed"
	_, err = m.UpdateLog(ctx, g.ID, l.ID, ocilogging.LogUpdate{DisplayName: &same})
	require.NoError(t, err, "renaming a log to the name it already has is a no-op")

	withCfg, err := m.UpdateLog(ctx, g.ID, l.ID, ocilogging.LogUpdate{
		Configuration: &ocilogging.LogConfiguration{
			Source: ocilogging.LogSource{Parameters: map[string]string{"k": "v"}},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, compartmentA, withCfg.Configuration.CompartmentID, "the log's compartment is filled in")
	assert.Equal(t, "OCISERVICE", withCfg.Configuration.Source.SourceType, "the only source type OCI defines")

	sl, err := m.CreateLog(ctx, g.ID, ocilogging.LogSpec{
		DisplayName: "flowlogs",
		LogType:     ocilogging.LogTypeService,
		Configuration: &ocilogging.LogConfiguration{
			Source: ocilogging.LogSource{Service: "flowlogs", Resource: "ocid1.subnet.oc1.iad.a"},
		},
	})
	require.NoError(t, err)

	_, err = m.UpdateLog(ctx, g.ID, sl.ID, ocilogging.LogUpdate{
		Configuration: &ocilogging.LogConfiguration{Source: ocilogging.LogSource{Service: "flowlogs"}},
	})
	require.Error(t, err, "a SERVICE log's configuration must still name a resource")
	assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))
}

func TestIngestionPublishesMetrics(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)
	g := newGroup(t, m, compartmentA, "app-logs")
	l := newCustomLog(t, m, g.ID, "stdout")

	mon := &recordingMonitoring{}
	m.SetMonitoring(mon)

	require.NoError(t, m.PutLogs(ctx, l.ID, []ocilogging.LogEntryBatch{{
		Entries: []ocilogging.LogEntryItem{{Data: "hello", Time: searchWindowStart}},
	}}))

	names := make([]string, 0, len(mon.data))
	for _, d := range mon.data {
		names = append(names, d.MetricName)
		assert.Equal(t, "oci_logging", d.Namespace)
		assert.Equal(t, l.ID, d.Dimensions["logId"])
	}

	assert.Equal(t, []string{"IngestedLogEntries", "IngestedLogBytes"}, names)
}

func TestIngestionSurvivesAMonitoringFailure(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)
	g := newGroup(t, m, compartmentA, "app-logs")
	l := newCustomLog(t, m, g.ID, "stdout")

	m.SetMonitoring(&recordingMonitoring{err: errFailedPublish})

	require.NoError(t, m.PutLogs(ctx, l.ID, []ocilogging.LogEntryBatch{{
		Entries: []ocilogging.LogEntryItem{{Data: "hello", Time: searchWindowStart}},
	}}), "metric publication is best-effort")

	entries, err := m.Entries(ctx, l.ID)
	require.NoError(t, err)
	assert.Len(t, entries, 1)
}

// errFailedPublish is what the stub monitoring driver refuses with.
var errFailedPublish = errors.New("monitoring is down")

// recordingMonitoring is a monitoring driver that records what Logging
// publishes. Every other operation is unused by this package.
type recordingMonitoring struct {
	mondriver.Monitoring

	data []mondriver.MetricDatum
	err  error
}

func (r *recordingMonitoring) PutMetricData(_ context.Context, data []mondriver.MetricDatum) error {
	if r.err != nil {
		return r.err
	}

	r.data = append(r.data, data...)

	return nil
}
