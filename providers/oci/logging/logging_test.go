package logging_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	ocilogging "github.com/stackshy/cloudemu/v2/providers/oci/logging"
	"github.com/stackshy/cloudemu/v2/services/logging/driver"
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

	_, err = m.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "portable"})
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
