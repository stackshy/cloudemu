package identity

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/iam/driver"
)

func TestCompartmentCRUD(t *testing.T) {
	m := newMock(t)
	ctx := t.Context()

	created, err := m.CreateCompartment(ctx, driver.CompartmentSpec{
		ParentID:    tenancy,
		Name:        devName,
		Description: "engineering",
	})
	require.NoError(t, err)
	assert.Equal(t, tenancy, created.ParentID)
	assert.Equal(t, lifecycleActive, created.LifecycleState)

	got, err := m.GetCompartment(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, devName, got.Name)

	updated, err := m.UpdateCompartment(ctx, created.ID, driver.IdentityUpdate{
		Name:        "development",
		Description: "renamed",
	})
	require.NoError(t, err)
	assert.Equal(t, "development", updated.Name)
	assert.Equal(t, "renamed", updated.Description)

	require.NoError(t, m.DeleteCompartment(ctx, created.ID))

	_, err = m.GetCompartment(ctx, created.ID)
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))
}

func TestTenancyIsTheRootCompartment(t *testing.T) {
	m := newMock(t)

	root, err := m.GetCompartment(t.Context(), tenancy)
	require.NoError(t, err)
	assert.Equal(t, rootName, root.Name)
	assert.Empty(t, root.ParentID, "the root compartment has no parent")
}

func TestCompartmentErrors(t *testing.T) {
	m := newMock(t)
	ctx := t.Context()
	dev := newCompartment(t, m, tenancy, devName)

	_, err := m.CreateOCIUser(ctx, driver.PrincipalSpec{CompartmentID: dev, Name: "alice"})
	require.NoError(t, err)

	tests := []struct {
		name string
		call func() error
		code cerrors.Code
	}{
		{
			name: "missing name",
			call: func() error {
				_, err := m.CreateCompartment(ctx, driver.CompartmentSpec{ParentID: tenancy})
				return err
			},
			code: cerrors.InvalidArgument,
		},
		{
			name: "duplicate sibling name",
			call: func() error {
				_, err := m.CreateCompartment(ctx, driver.CompartmentSpec{ParentID: tenancy, Name: devName})
				return err
			},
			code: cerrors.AlreadyExists,
		},
		{
			name: "unknown parent",
			call: func() error {
				_, err := m.CreateCompartment(ctx, driver.CompartmentSpec{
					ParentID: "ocid1.compartment.oc1..missing", Name: "x",
				})
				return err
			},
			code: cerrors.NotFound,
		},
		{
			name: "delete unknown",
			call: func() error { return m.DeleteCompartment(ctx, "ocid1.compartment.oc1..missing") },
			code: cerrors.NotFound,
		},
		{
			name: "delete occupied",
			call: func() error { return m.DeleteCompartment(ctx, dev) },
			code: cerrors.FailedPrecondition,
		},
		{
			name: "list unknown parent",
			call: func() error {
				_, err := m.ListCompartments(ctx, "ocid1.compartment.oc1..missing", false)
				return err
			},
			code: cerrors.NotFound,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.code, cerrors.GetCode(tc.call()))
		})
	}
}

func TestSameNameAllowedInDifferentParents(t *testing.T) {
	m := newMock(t)
	dev := newCompartment(t, m, tenancy, devName)

	// "shared" under the tenancy and under dev are different compartments.
	a := newCompartment(t, m, tenancy, "shared")
	b := newCompartment(t, m, dev, "shared")

	assert.NotEqual(t, a, b)
}

func TestListCompartmentsNestsOnlyInSubtree(t *testing.T) {
	m := newMock(t)
	ctx := t.Context()

	dev := newCompartment(t, m, tenancy, devName)
	staging := newCompartment(t, m, tenancy, "staging")
	team := newCompartment(t, m, dev, "team")
	svc := newCompartment(t, m, team, "svc")

	tests := []struct {
		name    string
		parent  string
		subtree bool
		want    []string
	}{
		{name: "direct children of tenancy", parent: tenancy, want: []string{dev, staging}},
		{name: "tenancy subtree", parent: tenancy, subtree: true, want: []string{dev, staging, team, svc}},
		{name: "direct children of dev", parent: dev, want: []string{team}},
		{name: "dev subtree", parent: dev, subtree: true, want: []string{team, svc}},
		{name: "leaf has no children", parent: svc, want: nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := m.ListCompartments(ctx, tc.parent, tc.subtree)
			require.NoError(t, err)

			ids := make([]string, 0, len(got))
			for _, c := range got {
				ids = append(ids, c.ID)
			}

			assert.ElementsMatch(t, tc.want, ids)
		})
	}
}

func TestCoversWalksAncestry(t *testing.T) {
	m := newMock(t)
	dev := newCompartment(t, m, tenancy, devName)
	team := newCompartment(t, m, dev, "team")
	staging := newCompartment(t, m, tenancy, "staging")

	tests := []struct {
		name     string
		ancestor string
		target   string
		want     bool
	}{
		{name: "self", ancestor: dev, target: dev, want: true},
		{name: "child", ancestor: dev, target: team, want: true},
		{name: "tenancy covers everything", ancestor: tenancy, target: team, want: true},
		{name: "sibling", ancestor: dev, target: staging},
		{name: "upwards", ancestor: team, target: dev},
		{name: "unknown target", ancestor: tenancy, target: "ocid1.compartment.oc1..missing"},
		{name: "empty", ancestor: "", target: dev},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, m.covers(tc.ancestor, tc.target))
		})
	}
}

func TestResolvePathWalksNames(t *testing.T) {
	m := newMock(t)
	dev := newCompartment(t, m, tenancy, devName)
	team := newCompartment(t, m, dev, "team")

	got, ok := m.resolvePath(tenancy, "dev:team")
	require.True(t, ok)
	assert.Equal(t, team, got)

	_, ok = m.resolvePath(tenancy, "dev:missing")
	assert.False(t, ok)
}
