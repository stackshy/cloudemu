package cloudlogging

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/logging/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testProject = "demo"

// TestCreateSinkWriterIdentity guards that a sink with no caller-supplied writer
// identity falls back to the shared non-unique account, and a caller-supplied
// one (unique/custom, resolved by the wire layer) is stored verbatim.
func TestCreateSinkWriterIdentity(t *testing.T) {
	ctx := context.Background()

	t.Run("default shared account", func(t *testing.T) {
		m := newTestMock()

		s, err := m.CreateSink(ctx, testProject, &driver.LogSink{
			Name:        "s1",
			Destination: "storage.googleapis.com/b",
		})
		require.NoError(t, err)
		assert.Equal(t, defaultSinkWriterIdentity, s.WriterIdentity)
	})

	t.Run("caller-supplied identity preserved", func(t *testing.T) {
		m := newTestMock()

		s, err := m.CreateSink(ctx, testProject, &driver.LogSink{
			Name:           "s2",
			Destination:    "storage.googleapis.com/b",
			WriterIdentity: "serviceAccount:service-demo@gcp-sa-logging.iam.gserviceaccount.com",
		})
		require.NoError(t, err)
		assert.Equal(t, "serviceAccount:service-demo@gcp-sa-logging.iam.gserviceaccount.com", s.WriterIdentity)
	})
}

// TestUpdateSinkPartialMask guards that a masked update touches only the fields
// whose Set* flag is set, leaving every other field (including the writer
// identity) unchanged — the semantics of a Cloud Logging updateMask patch.
func TestUpdateSinkPartialMask(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateSink(ctx, testProject, &driver.LogSink{
		Name:        "s",
		Destination: "storage.googleapis.com/keep",
		Filter:      "severity>=ERROR",
		Description: "keep me",
	})
	require.NoError(t, err)

	// Update only the filter.
	updated, err := m.UpdateSink(ctx, testProject, "s", &driver.SinkUpdate{
		Filter:    "severity>=WARNING",
		SetFilter: true,
	})
	require.NoError(t, err)

	assert.Equal(t, "severity>=WARNING", updated.Filter, "filter updated")
	assert.Equal(t, "storage.googleapis.com/keep", updated.Destination, "destination preserved")
	assert.Equal(t, "keep me", updated.Description, "description preserved")
	assert.Equal(t, defaultSinkWriterIdentity, updated.WriterIdentity, "writer identity preserved")
}

// TestUpdateSinkWriterIdentityReplace guards that a non-empty WriterIdentity on
// the update replaces the stored one (a uniqueWriterIdentity/customWriterIdentity
// transition), while an empty one leaves it unchanged.
func TestUpdateSinkWriterIdentityReplace(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateSink(ctx, testProject, &driver.LogSink{Name: "s", Destination: "storage.googleapis.com/b"})
	require.NoError(t, err)

	const unique = "serviceAccount:service-demo@gcp-sa-logging.iam.gserviceaccount.com"

	replaced, err := m.UpdateSink(ctx, testProject, "s", &driver.SinkUpdate{WriterIdentity: unique})
	require.NoError(t, err)
	assert.Equal(t, unique, replaced.WriterIdentity)

	// Empty identity leaves it unchanged.
	kept, err := m.UpdateSink(ctx, testProject, "s", &driver.SinkUpdate{Filter: "x", SetFilter: true})
	require.NoError(t, err)
	assert.Equal(t, unique, kept.WriterIdentity)
}

// TestUpdateSinkNotFound guards the not-found path.
func TestUpdateSinkNotFound(t *testing.T) {
	m := newTestMock()

	_, err := m.UpdateSink(context.Background(), testProject, "nope", &driver.SinkUpdate{})
	require.Error(t, err)
}
