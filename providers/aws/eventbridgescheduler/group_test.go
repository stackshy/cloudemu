package eventbridgescheduler_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/services/eventbridgescheduler/driver"
)

func TestCreateScheduleGroupComputedFields(t *testing.T) {
	m := newMock()

	g, err := m.CreateScheduleGroup(context.Background(), "g1", map[string]string{"env": "test"})
	requireNoError(t, err)

	wantArn := "arn:aws:scheduler:us-east-1:" + config.NewOptions().AccountID + ":schedule-group/g1"
	if g.Arn != wantArn {
		t.Fatalf("arn = %q, want %q", g.Arn, wantArn)
	}

	if g.State != driver.GroupActive {
		t.Fatalf("state = %q, want ACTIVE", g.State)
	}

	if g.CreationDate.IsZero() || g.Tags["env"] != "test" {
		t.Fatalf("group not initialized correctly: %+v", g)
	}
}

func TestDefaultGroupImplicit(t *testing.T) {
	m := newMock()

	g, err := m.GetScheduleGroup(context.Background(), "default")
	requireNoError(t, err)

	if g.State != driver.GroupActive || !strings.HasSuffix(g.Arn, ":schedule-group/default") {
		t.Fatalf("implicit default group wrong: %+v", g)
	}
}

func TestDefaultGroupTimestampsStableAcrossReads(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	first, err := m.GetScheduleGroup(ctx, "default")
	requireNoError(t, err)

	second, err := m.GetScheduleGroup(ctx, "default")
	requireNoError(t, err)

	if !first.CreationDate.Equal(second.CreationDate) {
		t.Errorf("default group CreationDate drifted across reads: %v -> %v",
			first.CreationDate, second.CreationDate)
	}

	if !first.LastModificationDate.Equal(second.LastModificationDate) {
		t.Errorf("default group LastModificationDate drifted across reads: %v -> %v",
			first.LastModificationDate, second.LastModificationDate)
	}
}

func TestGetScheduleGroupEmptyResolvesDefault(t *testing.T) {
	m := newMock()

	g, err := m.GetScheduleGroup(context.Background(), "")
	requireNoError(t, err)

	if g.Name != "default" {
		t.Fatalf("empty name resolved to %q, want default", g.Name)
	}
}

func TestCreateDefaultGroupConflicts(t *testing.T) {
	m := newMock()

	_, err := m.CreateScheduleGroup(context.Background(), "default", nil)
	assertException(t, err, driver.ExConflict)
}

func TestCreateScheduleGroupDuplicateConflicts(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	_, err := m.CreateScheduleGroup(ctx, "g1", nil)
	requireNoError(t, err)

	_, err = m.CreateScheduleGroup(ctx, "g1", nil)
	assertException(t, err, driver.ExConflict)
}

func TestDeleteScheduleGroupCascades(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	_, err := m.CreateScheduleGroup(ctx, "g1", nil)
	requireNoError(t, err)

	_, err = m.CreateSchedule(ctx, scheduleInput("s1", "g1"))
	requireNoError(t, err)

	requireNoError(t, m.DeleteScheduleGroup(ctx, "g1"))

	_, err = m.GetScheduleGroup(ctx, "g1")
	assertException(t, err, driver.ExResourceNotFound)

	// The schedule inside the deleted group is gone too.
	_, err = m.GetSchedule(ctx, "g1", "s1")
	assertException(t, err, driver.ExResourceNotFound)
}

func TestDeleteDefaultGroupRejected(t *testing.T) {
	m := newMock()

	assertException(t, m.DeleteScheduleGroup(context.Background(), "default"), driver.ExValidation)
}

func TestListScheduleGroupsIncludesDefault(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	_, err := m.CreateScheduleGroup(ctx, "g1", nil)
	requireNoError(t, err)

	groups, _, err := m.ListScheduleGroups(ctx, driver.GroupFilter{})
	requireNoError(t, err)

	var names []string
	for i := range groups {
		names = append(names, groups[i].Name)
	}

	if len(names) != 2 || names[0] != "default" || names[1] != "g1" {
		t.Fatalf("groups = %v, want [default g1]", names)
	}
}

func TestGroupTagsRoundTrip(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	g, err := m.CreateScheduleGroup(ctx, "g1", nil)
	requireNoError(t, err)

	requireNoError(t, m.TagResource(ctx, g.Arn, map[string]string{"a": "1", "b": "2"}))

	tags, err := m.ListTagsForResource(ctx, g.Arn)
	requireNoError(t, err)

	if tags["a"] != "1" || tags["b"] != "2" {
		t.Fatalf("tags = %v", tags)
	}

	requireNoError(t, m.UntagResource(ctx, g.Arn, []string{"a"}))

	tags, err = m.ListTagsForResource(ctx, g.Arn)
	requireNoError(t, err)

	if _, ok := tags["a"]; ok || tags["b"] != "2" {
		t.Fatalf("after untag tags = %v, want only b=2", tags)
	}
}

func TestTagsRejectScheduleArn(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	s, err := m.CreateSchedule(ctx, scheduleInput("s1", ""))
	requireNoError(t, err)

	// Tags are supported only on schedule groups; a schedule ARN is rejected.
	assertException(t, m.TagResource(ctx, s.Arn, map[string]string{"x": "y"}), driver.ExValidation)
}
