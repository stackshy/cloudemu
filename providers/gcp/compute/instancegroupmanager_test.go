package compute

import (
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInstanceGroupManagerCRUD(t *testing.T) {
	m := newTestMock()

	require.NoError(t, m.CreateInstanceGroupManagerGCP(InstanceGroupManager{
		Name: "web-mig", Zone: "us-central1-a", TargetSize: 3, BaseInstanceName: "web",
	}))

	got, ok := m.GetInstanceGroupManagerGCP("us-central1-a", "web-mig")
	require.True(t, ok)
	assert.Equal(t, 3, got.TargetSize)
	assert.Equal(t, "web", got.BaseInstanceName)
	assert.NotEmpty(t, got.CreatedAt)

	list := m.ListInstanceGroupManagersGCP("us-central1-a")
	require.Len(t, list, 1)
	assert.Equal(t, "web-mig", list[0].Name)

	// A different zone is a distinct scope.
	assert.Empty(t, m.ListInstanceGroupManagersGCP("us-central1-b"))

	require.NoError(t, m.DeleteInstanceGroupManagerGCP("us-central1-a", "web-mig"))

	_, ok = m.GetInstanceGroupManagerGCP("us-central1-a", "web-mig")
	assert.False(t, ok)
}

func TestInstanceGroupManagerDuplicateAndValidation(t *testing.T) {
	m := newTestMock()

	require.NoError(t, m.CreateInstanceGroupManagerGCP(InstanceGroupManager{Name: "a", Zone: "z1", TargetSize: 1}))

	err := m.CreateInstanceGroupManagerGCP(InstanceGroupManager{Name: "a", Zone: "z1"})
	require.Error(t, err)
	assert.True(t, cerrors.IsAlreadyExists(err))

	// Same name in another zone is allowed.
	require.NoError(t, m.CreateInstanceGroupManagerGCP(InstanceGroupManager{Name: "a", Zone: "z2", TargetSize: 1}))

	assert.True(t, cerrors.IsInvalidArgument(m.CreateInstanceGroupManagerGCP(InstanceGroupManager{Zone: "z1"})))
	assert.True(t, cerrors.IsInvalidArgument(m.CreateInstanceGroupManagerGCP(InstanceGroupManager{Name: "x"})))
}

func TestInstanceGroupManagerResize(t *testing.T) {
	m := newTestMock()

	require.NoError(t, m.CreateInstanceGroupManagerGCP(InstanceGroupManager{Name: "g", Zone: "z1", TargetSize: 1}))

	require.NoError(t, m.ResizeInstanceGroupManagerGCP("z1", "g", 5))

	got, ok := m.GetInstanceGroupManagerGCP("z1", "g")
	require.True(t, ok)
	assert.Equal(t, 5, got.TargetSize)

	assert.True(t, cerrors.IsNotFound(m.ResizeInstanceGroupManagerGCP("z1", "missing", 2)))
	assert.True(t, cerrors.IsInvalidArgument(m.ResizeInstanceGroupManagerGCP("z1", "g", -1)))
}

func TestInstanceGroupManagerUpsertPreservesCreatedAt(t *testing.T) {
	m := newTestMock()

	m.UpsertInstanceGroupManagerGCP(InstanceGroupManager{Name: "u", Zone: "z1", TargetSize: 2})
	first, ok := m.GetInstanceGroupManagerGCP("z1", "u")
	require.True(t, ok)

	// A second upsert (e.g. a node-pool resize reconcile) updates targetSize but
	// keeps the original creation timestamp.
	m.UpsertInstanceGroupManagerGCP(InstanceGroupManager{Name: "u", Zone: "z1", TargetSize: 7})
	second, ok := m.GetInstanceGroupManagerGCP("z1", "u")
	require.True(t, ok)

	assert.Equal(t, 7, second.TargetSize)
	assert.Equal(t, first.CreatedAt, second.CreatedAt)
}

func TestAllInstanceGroupManagers(t *testing.T) {
	m := newTestMock()

	require.NoError(t, m.CreateInstanceGroupManagerGCP(InstanceGroupManager{Name: "a", Zone: "z1", TargetSize: 1}))
	require.NoError(t, m.CreateInstanceGroupManagerGCP(InstanceGroupManager{Name: "b", Zone: "z2", TargetSize: 1}))

	assert.Len(t, m.AllInstanceGroupManagersGCP(), 2)
}
