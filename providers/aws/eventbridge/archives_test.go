package eventbridge

import (
	"context"
	"errors"
	"testing"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/eventbus/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func busARN(name string) string {
	return "arn:aws:events:us-east-1:123456789012:event-bus/" + name
}

func createTestArchive(t *testing.T, m *Mock, name, bus, pattern string) *driver.Archive {
	t.Helper()

	a, err := m.CreateArchive(context.Background(), &driver.ArchiveConfig{
		Name: name, EventSourceARN: busARN(bus), EventPattern: pattern,
	})
	require.NoError(t, err)

	return a
}

func putTestEvent(t *testing.T, m *Mock, bus, source string) {
	t.Helper()

	_, err := m.PutEvents(context.Background(), []driver.Event{
		{Source: source, DetailType: "t", Detail: `{"k":"v"}`, EventBus: bus},
	})
	require.NoError(t, err)
}

func TestCreateArchive(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	a, err := m.CreateArchive(ctx, &driver.ArchiveConfig{
		Name: "a1", EventSourceARN: busARN("default"), Description: "d",
		EventPattern: `{ "source": ["s"] }`, RetentionDays: 3,
	})
	require.NoError(t, err)
	assert.Equal(t, "arn:aws:events:us-east-1:123456789012:archive/a1", a.ARN)
	assert.Equal(t, "ENABLED", a.State)
	assert.Equal(t, `{"source":["s"]}`, a.EventPattern)
	assert.Equal(t, 3, a.RetentionDays)
	assert.False(t, a.CreatedAt.IsZero())

	t.Run("duplicate", func(t *testing.T) {
		_, err := m.CreateArchive(ctx, &driver.ArchiveConfig{Name: "a1", EventSourceARN: busARN("default")})
		require.True(t, cerrors.IsAlreadyExists(err))
		assert.Equal(t, "Archive a1 already exists.", cerrors.Message(err))
	})

	t.Run("unknown bus", func(t *testing.T) {
		_, err := m.CreateArchive(ctx, &driver.ArchiveConfig{Name: "a2", EventSourceARN: busARN("ghost")})
		require.True(t, cerrors.IsNotFound(err))
		assert.Equal(t, "Event bus ghost does not exist.", cerrors.Message(err))
	})

	t.Run("invalid name", func(t *testing.T) {
		_, err := m.CreateArchive(ctx, &driver.ArchiveConfig{Name: "bad name", EventSourceARN: busARN("default")})
		require.True(t, cerrors.IsInvalidArgument(err))
		assert.Equal(t, "1 validation error detected: Value 'bad name' at 'archiveName' failed to satisfy constraint: "+
			`Member must satisfy regular expression pattern: [\.\-_A-Za-z0-9]+`, cerrors.Message(err))
	})

	t.Run("several violations are joined", func(t *testing.T) {
		_, err := m.CreateArchive(ctx, &driver.ArchiveConfig{Name: "bad name", EventSourceARN: "nope", RetentionDays: -1})
		require.True(t, cerrors.IsInvalidArgument(err))
		assert.Contains(t, cerrors.Message(err), "3 validation errors detected: ")
	})

	t.Run("invalid pattern", func(t *testing.T) {
		_, err := m.CreateArchive(ctx, &driver.ArchiveConfig{Name: "a3", EventSourceARN: busARN("default"), EventPattern: `{"source":"s"}`})
		require.Error(t, err)
		assert.True(t, errors.Is(err, driver.ErrInvalidEventPattern))
	})
}

func TestArchiveCapturesMatchingEvents(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()
	createTestBus(t, m, "b1")

	createTestArchive(t, m, "filtered", "b1", `{"source":["keep"]}`)
	createTestArchive(t, m, "all", "b1", "")
	createTestArchive(t, m, "other-bus", "default", "")

	putTestEvent(t, m, "b1", "keep")
	putTestEvent(t, m, "b1", "drop")

	filtered, err := m.DescribeArchive(ctx, "filtered")
	require.NoError(t, err)
	assert.Equal(t, int64(1), filtered.EventCount)
	assert.Positive(t, filtered.SizeBytes)

	all, err := m.DescribeArchive(ctx, "all")
	require.NoError(t, err)
	assert.Equal(t, int64(2), all.EventCount)

	other, err := m.DescribeArchive(ctx, "other-bus")
	require.NoError(t, err)
	assert.Equal(t, int64(0), other.EventCount)
}

func TestArchiveRetention(t *testing.T) {
	m, fc := newTestMock()
	ctx := context.Background()

	_, err := m.CreateArchive(ctx, &driver.ArchiveConfig{Name: "a", EventSourceARN: busARN("default"), RetentionDays: 1})
	require.NoError(t, err)

	putTestEvent(t, m, "", "s")
	fc.Advance(23 * time.Hour)

	a, err := m.DescribeArchive(ctx, "a")
	require.NoError(t, err)
	assert.Equal(t, int64(1), a.EventCount)

	fc.Advance(2 * time.Hour)

	a, err = m.DescribeArchive(ctx, "a")
	require.NoError(t, err)
	assert.Equal(t, int64(0), a.EventCount)
	assert.Equal(t, int64(0), a.SizeBytes)
}

func TestUpdateArchive(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()
	createTestArchive(t, m, "a", "default", "")

	t.Run("no field given is rejected before the archive is looked up", func(t *testing.T) {
		_, err := m.UpdateArchive(ctx, &driver.ArchiveUpdate{Name: "ghost"})
		require.True(t, cerrors.IsInvalidArgument(err))
		assert.Equal(t, "At least one of EventPattern, RetentionDays or Description must be provided.", cerrors.Message(err))
	})

	t.Run("unknown archive", func(t *testing.T) {
		desc := "x"
		_, err := m.UpdateArchive(ctx, &driver.ArchiveUpdate{Name: "ghost", Description: &desc})
		require.True(t, cerrors.IsNotFound(err))
		assert.Equal(t, "Archive ghost does not exist.", cerrors.Message(err))
	})

	t.Run("fields are updated and zero retention is honored", func(t *testing.T) {
		desc, pattern, days := "new", `{"source":["n"]}`, 7
		_, err := m.UpdateArchive(ctx, &driver.ArchiveUpdate{Name: "a", Description: &desc, EventPattern: &pattern, RetentionDays: &days})
		require.NoError(t, err)

		zero := 0
		out, err := m.UpdateArchive(ctx, &driver.ArchiveUpdate{Name: "a", RetentionDays: &zero})
		require.NoError(t, err)
		assert.Equal(t, "new", out.Description)
		assert.Equal(t, pattern, out.EventPattern)
		assert.Equal(t, 0, out.RetentionDays)
	})

	t.Run("invalid pattern", func(t *testing.T) {
		bad := `{"source":1}`
		_, err := m.UpdateArchive(ctx, &driver.ArchiveUpdate{Name: "a", EventPattern: &bad})
		assert.True(t, errors.Is(err, driver.ErrInvalidEventPattern))
	})
}

func TestDeleteArchive(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()
	createTestArchive(t, m, "a", "default", "")

	require.NoError(t, m.DeleteArchive(ctx, "a"))

	_, err := m.DescribeArchive(ctx, "a")
	assert.True(t, cerrors.IsNotFound(err))

	err = m.DeleteArchive(ctx, "a")
	require.True(t, cerrors.IsNotFound(err))
	assert.Equal(t, "Archive a does not exist.", cerrors.Message(err))
}

func TestListArchives(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()
	createTestBus(t, m, "b1")
	createTestArchive(t, m, "beta", "default", "")
	createTestArchive(t, m, "alpha", "b1", "")

	all, err := m.ListArchives(ctx, driver.ArchiveFilter{})
	require.NoError(t, err)
	require.Len(t, all, 2)
	assert.Equal(t, "alpha", all[0].Name)

	byPrefix, err := m.ListArchives(ctx, driver.ArchiveFilter{NamePrefix: "be"})
	require.NoError(t, err)
	require.Len(t, byPrefix, 1)
	assert.Equal(t, "beta", byPrefix[0].Name)

	byBus, err := m.ListArchives(ctx, driver.ArchiveFilter{EventSourceARN: busARN("b1")})
	require.NoError(t, err)
	require.Len(t, byBus, 1)
	assert.Equal(t, "alpha", byBus[0].Name)

	byState, err := m.ListArchives(ctx, driver.ArchiveFilter{State: "DISABLED"})
	require.NoError(t, err)
	assert.Empty(t, byState)

	_, err = m.ListArchives(ctx, driver.ArchiveFilter{NamePrefix: "a", State: "ENABLED"})
	require.True(t, cerrors.IsInvalidArgument(err))
	assert.Equal(t, "At most one filter is allowed for ListArchives. Use either : State, EventSourceArn, or NamePrefix.", cerrors.Message(err))

	_, err = m.ListArchives(ctx, driver.ArchiveFilter{EventSourceARN: busARN("ghost")})
	require.True(t, cerrors.IsNotFound(err))
	assert.Equal(t, "Event bus ghost does not exist.", cerrors.Message(err))

	_, err = m.ListArchives(ctx, driver.ArchiveFilter{State: "BOGUS"})
	require.True(t, cerrors.IsInvalidArgument(err))
	assert.Contains(t, cerrors.Message(err), "Member must satisfy enum value set: [ENABLED, DISABLED, CREATING, UPDATING, CREATE_FAILED, UPDATE_FAILED]")
}
