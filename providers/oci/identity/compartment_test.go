package identity

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/iam/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
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

// TestIdentityNamesFollowOCIConstraints pins the naming rule real OCI applies
// to compartments, users and groups.
func TestIdentityNamesFollowOCIConstraints(t *testing.T) {
	ctx := t.Context()
	tooLong := strings.Repeat("a", maxNameLength+1)

	tests := []struct {
		name string
		call func(m *Mock, name string) error
	}{
		{
			name: "compartment",
			call: func(m *Mock, name string) error {
				_, err := m.CreateCompartment(ctx, driver.CompartmentSpec{ParentID: tenancy, Name: name})
				return err
			},
		},
		{
			name: "compartment rename",
			call: func(m *Mock, name string) error {
				id := newCompartment(t, m, tenancy, "keep")
				_, err := m.UpdateCompartment(ctx, id, driver.IdentityUpdate{Name: name})

				return err
			},
		},
		{
			name: "user",
			call: func(m *Mock, name string) error {
				_, err := m.CreateOCIUser(ctx, driver.PrincipalSpec{CompartmentID: tenancy, Name: name})
				return err
			},
		},
		{
			name: "group",
			call: func(m *Mock, name string) error {
				_, err := m.CreateOCIGroup(ctx, driver.PrincipalSpec{CompartmentID: tenancy, Name: name})
				return err
			},
		},
		{
			name: "dynamic group",
			call: func(m *Mock, name string) error {
				_, err := m.CreateRole(ctx, driver.RoleConfig{Name: name})
				return err
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for _, bad := range []string{"has a space", tooLong, "slash/name"} {
				assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(tc.call(newMock(t), bad)), bad)
			}

			require.NoError(t, tc.call(newMock(t), "dev.team_1-a"))
		})
	}
}

func TestDeleteCompartmentSeesADynamicGroup(t *testing.T) {
	m := newMock(t)
	dev := newCompartment(t, m, tenancy, devName)

	// CreateRole always lands in the configured compartment, so place one in
	// dev directly; the emptiness check must still see it.
	m.dynamicGroups.Set("dg", &dynamicGroup{
		ID: "dg", Name: "fleet", Scope: scope.Scope{Compartment: dev},
	})

	err := m.DeleteCompartment(t.Context(), dev)
	assert.Equal(t, cerrors.FailedPrecondition, cerrors.GetCode(err))
	assert.Contains(t, err.Error(), "dynamic group fleet")
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
