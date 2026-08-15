package identity

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/iam/driver"
)

const manageAllInDev = "Allow group Admins to manage all-resources in compartment dev"

func TestPolicyCRUD(t *testing.T) {
	m := newMock(t)
	ctx := t.Context()

	created, err := m.CreateStatementPolicy(ctx, &PolicySpec{
		CompartmentID: tenancy,
		Name:          "admins",
		Description:   "admin access",
		Statements:    []string{manageAllInDev},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{manageAllInDev}, created.Statements)
	assert.Equal(t, created.TimeCreated, created.VersionDate)

	got, err := m.GetStatementPolicy(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, "admins", got.Name)

	updated, err := m.UpdateStatementPolicy(ctx, created.ID, PolicyUpdate{
		Description: "narrowed",
		Statements:  []string{"Allow group Admins to read buckets in tenancy"},
	})
	require.NoError(t, err)
	assert.Equal(t, "narrowed", updated.Description)
	assert.Len(t, updated.Statements, 1)

	listed, err := m.ListStatementPolicies(ctx, tenancy)
	require.NoError(t, err)
	assert.Len(t, listed, 1)

	require.NoError(t, m.DeleteStatementPolicy(ctx, created.ID))

	_, err = m.GetStatementPolicy(ctx, created.ID)
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))
}

func TestPolicyErrors(t *testing.T) {
	m := newMock(t)
	ctx := t.Context()

	_, err := m.CreateStatementPolicy(ctx, &PolicySpec{
		CompartmentID: tenancy, Name: "admins", Statements: []string{manageAllInDev},
	})
	require.NoError(t, err)

	tests := []struct {
		name string
		call func() error
		code cerrors.Code
	}{
		{
			name: "missing name",
			call: func() error {
				_, err := m.CreateStatementPolicy(ctx, &PolicySpec{CompartmentID: tenancy})
				return err
			},
			code: cerrors.InvalidArgument,
		},
		{
			name: "no statements",
			call: func() error {
				_, err := m.CreateStatementPolicy(ctx, &PolicySpec{CompartmentID: tenancy, Name: "empty"})
				return err
			},
			code: cerrors.InvalidArgument,
		},
		{
			name: "unparseable statement",
			call: func() error {
				_, err := m.CreateStatementPolicy(ctx, &PolicySpec{
					CompartmentID: tenancy, Name: "bad", Statements: []string{"do whatever you like"},
				})
				return err
			},
			code: cerrors.InvalidArgument,
		},
		{
			name: "duplicate name in the same compartment",
			call: func() error {
				_, err := m.CreateStatementPolicy(ctx, &PolicySpec{
					CompartmentID: tenancy, Name: "admins", Statements: []string{manageAllInDev},
				})
				return err
			},
			code: cerrors.AlreadyExists,
		},
		{
			name: "get unknown",
			call: func() error {
				_, err := m.GetStatementPolicy(ctx, "ocid1.policy.oc1..missing")
				return err
			},
			code: cerrors.NotFound,
		},
		{
			name: "delete unknown",
			call: func() error { return m.DeleteStatementPolicy(ctx, "ocid1.policy.oc1..missing") },
			code: cerrors.NotFound,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.code, cerrors.GetCode(tc.call()))
		})
	}
}

func TestPoliciesFilterByCompartment(t *testing.T) {
	m := newMock(t)
	ctx := t.Context()
	dev := newCompartment(t, m, tenancy, devName)

	_, err := m.CreateStatementPolicy(ctx, &PolicySpec{
		CompartmentID: dev, Name: "dev-policy", Statements: []string{"Allow group Admins to manage buckets in tenancy"},
	})
	require.NoError(t, err)

	inDev, err := m.ListStatementPolicies(ctx, dev)
	require.NoError(t, err)
	assert.Len(t, inDev, 1)

	inRoot, err := m.ListStatementPolicies(ctx, tenancy)
	require.NoError(t, err)
	assert.Empty(t, inRoot, "a policy in another compartment must not list")
}

// evalFixture builds a tenancy with dev/dev-team and staging compartments and a
// root policy granting Admins management of dev.
func evalFixture(t *testing.T) (m *Mock, dev, team, staging string) {
	t.Helper()

	m = newMock(t)
	dev = newCompartment(t, m, tenancy, devName)
	team = newCompartment(t, m, dev, "team")
	staging = newCompartment(t, m, tenancy, "staging")

	_, err := m.CreateStatementPolicy(t.Context(), &PolicySpec{
		CompartmentID: tenancy, Name: "admins", Statements: []string{manageAllInDev},
	})
	require.NoError(t, err)

	return m, dev, team, staging
}

func TestEvaluateResolvesCompartmentSubtree(t *testing.T) {
	m, dev, team, staging := evalFixture(t)

	tests := []struct {
		name        string
		compartment string
		want        bool
	}{
		{name: "named compartment", compartment: dev, want: true},
		{name: "descendant of the named compartment", compartment: team, want: true},
		{name: "sibling compartment", compartment: staging},
		{name: "the tenancy itself", compartment: tenancy},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ok, err := m.Evaluate(t.Context(), &AccessRequest{
				Groups:        []string{adminName},
				Verb:          verbManage,
				ResourceType:  "buckets",
				CompartmentID: tc.compartment,
			})
			require.NoError(t, err)
			assert.Equal(t, tc.want, ok)
		})
	}
}

func TestEvaluateHonoursTheStatementLocationForms(t *testing.T) {
	m := newMock(t)
	ctx := t.Context()
	dev := newCompartment(t, m, tenancy, devName)
	team := newCompartment(t, m, dev, "team")

	tests := []struct {
		name      string
		statement string
		target    string
		want      bool
	}{
		{
			name:      "in tenancy grants the whole tree",
			statement: "Allow group Admins to manage buckets in tenancy",
			target:    team, want: true,
		},
		{
			name:      "compartment by id",
			statement: "Allow group Admins to manage buckets in compartment id " + dev,
			target:    team, want: true,
		},
		{
			name:      "nested compartment path",
			statement: "Allow group Admins to manage buckets in compartment dev:team",
			target:    team, want: true,
		},
		{
			name:      "nested path does not reach the parent",
			statement: "Allow group Admins to manage buckets in compartment dev:team",
			target:    dev,
		},
		{
			name:      "unknown compartment name grants nothing",
			statement: "Allow group Admins to manage buckets in compartment nowhere",
			target:    team,
		},
	}

	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p, err := m.CreateStatementPolicy(ctx, &PolicySpec{
				CompartmentID: tenancy,
				Name:          "case-" + string(rune('a'+i)),
				Statements:    []string{tc.statement},
			})
			require.NoError(t, err)

			defer func() { require.NoError(t, m.DeleteStatementPolicy(ctx, p.ID)) }()

			ok, err := m.Evaluate(ctx, &AccessRequest{
				Groups:        []string{adminName},
				Verb:          verbManage,
				ResourceType:  "buckets",
				CompartmentID: tc.target,
			})
			require.NoError(t, err)
			assert.Equal(t, tc.want, ok)
		})
	}
}

// A nested path written with spaces around the colon must be rejected. Keeping
// only its first token would grant the parent's whole subtree instead.
func TestSpacedNestedPathIsRejectedRatherThanBroadened(t *testing.T) {
	m := newMock(t)
	ctx := t.Context()
	dev := newCompartment(t, m, tenancy, devName)
	team := newCompartment(t, m, dev, "team")
	other := newCompartment(t, m, dev, "other")

	_, err := m.CreateStatementPolicy(ctx, &PolicySpec{
		CompartmentID: tenancy,
		Name:          "spaced",
		Statements:    []string{"Allow group Admins to manage buckets in compartment dev : team"},
	})
	require.Error(t, err)
	assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))

	_, err = m.CreateStatementPolicy(ctx, &PolicySpec{
		CompartmentID: tenancy,
		Name:          "canonical",
		Statements:    []string{"Allow group Admins to manage buckets in compartment dev:team"},
	})
	require.NoError(t, err)

	tests := []struct {
		name   string
		target string
		want   bool
	}{
		{name: "the named nested compartment", target: team, want: true},
		{name: "its parent", target: dev},
		{name: "a sibling under the same parent", target: other},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ok, err := m.Evaluate(ctx, &AccessRequest{
				Groups:        []string{adminName},
				Verb:          verbManage,
				ResourceType:  "buckets",
				CompartmentID: tc.target,
			})
			require.NoError(t, err)
			assert.Equal(t, tc.want, ok)
		})
	}
}

func TestPolicyNeverReachesOutsideItsOwnCompartment(t *testing.T) {
	m := newMock(t)
	ctx := t.Context()
	dev := newCompartment(t, m, tenancy, devName)
	staging := newCompartment(t, m, tenancy, "staging")

	// A policy attached to dev naming staging by OCID grants nothing: real OCI
	// only lets a policy govern its own compartment's subtree.
	_, err := m.CreateStatementPolicy(ctx, &PolicySpec{
		CompartmentID: dev,
		Name:          "reach-out",
		Statements:    []string{"Allow group Admins to manage buckets in compartment id " + staging},
	})
	require.NoError(t, err)

	ok, err := m.Evaluate(ctx, &AccessRequest{
		Groups: []string{adminName}, Verb: verbManage, ResourceType: "buckets", CompartmentID: staging,
	})
	require.NoError(t, err)
	assert.False(t, ok)
}

// TestEvaluateDisclosesAWhereCondition pins the disclose-or-reject rule: a
// statement whose grant is qualified by "where" must not grant the whole verb.
func TestEvaluateDisclosesAWhereCondition(t *testing.T) {
	m := newMock(t)
	ctx := t.Context()
	dev := newCompartment(t, m, tenancy, devName)

	_, err := m.CreateStatementPolicy(ctx, &PolicySpec{
		CompartmentID: tenancy,
		Name:          "conditional",
		Statements: []string{
			"Allow group Admins to manage buckets in compartment dev where request.permission = 'BUCKET_INSPECT'",
		},
	})
	require.NoError(t, err)

	ok, err := m.Evaluate(ctx, &AccessRequest{
		Groups: []string{adminName}, Verb: verbManage, ResourceType: "buckets", CompartmentID: dev,
	})
	require.Error(t, err)
	assert.False(t, ok, "a where-qualified statement must not grant the whole verb")
	assert.Equal(t, cerrors.Unimplemented, cerrors.GetCode(err))
	assert.Contains(t, err.Error(), keywordWhere)
}

func TestEvaluateIgnoresAWhereConditionItDoesNotReach(t *testing.T) {
	m := newMock(t)
	ctx := t.Context()
	dev := newCompartment(t, m, tenancy, devName)

	_, err := m.CreateStatementPolicy(ctx, &PolicySpec{
		CompartmentID: tenancy,
		Name:          "mixed",
		Statements: []string{
			"Allow group Auditors to manage buckets in compartment dev where request.region = 'iad'",
			"Allow group Admins to manage buckets in compartment dev",
		},
	})
	require.NoError(t, err)

	ok, err := m.Evaluate(ctx, &AccessRequest{
		Groups: []string{adminName}, Verb: verbManage, ResourceType: "buckets", CompartmentID: dev,
	})
	require.NoError(t, err, "a condition on a statement that does not reach the request is not disclosed")
	assert.True(t, ok)
}

// TestEvaluateDisclosesAnUnmodeledFamily pins the other half of the rule: a
// family this emulator does not model is reported, not quietly denied.
func TestEvaluateDisclosesAnUnmodeledFamily(t *testing.T) {
	m := newMock(t)
	ctx := t.Context()
	dev := newCompartment(t, m, tenancy, devName)

	_, err := m.CreateStatementPolicy(ctx, &PolicySpec{
		CompartmentID: tenancy,
		Name:          "science",
		Statements:    []string{"Allow group Admins to manage data-science-family in compartment dev"},
	})
	require.NoError(t, err)

	ok, err := m.Evaluate(ctx, &AccessRequest{
		Groups: []string{adminName}, Verb: verbManage, ResourceType: "data-science-models", CompartmentID: dev,
	})
	require.Error(t, err)
	assert.False(t, ok, "an unmodeled family must not grant")
	assert.Equal(t, cerrors.Unimplemented, cerrors.GetCode(err))
	assert.Contains(t, err.Error(), "data-science-family")
}

func TestEvaluateGrantsTheBroadenedFamilies(t *testing.T) {
	m := newMock(t)
	ctx := t.Context()
	dev := newCompartment(t, m, tenancy, devName)

	tests := []struct {
		family   string
		resource string
	}{
		{family: "functions-family", resource: "fn-function"},
		{family: "stream-family", resource: "streams"},
		{family: "email-family", resource: "email-domains"},
		{family: "compute-management-family", resource: "instance-pools"},
		{family: "volume-family", resource: "boot-volumes"},
		{family: "virtual-network-family", resource: "vlans"},
	}

	for i, tc := range tests {
		t.Run(tc.family, func(t *testing.T) {
			p, err := m.CreateStatementPolicy(ctx, &PolicySpec{
				CompartmentID: tenancy,
				Name:          "fam-" + string(rune('a'+i)),
				Statements:    []string{"Allow group Admins to manage " + tc.family + " in compartment dev"},
			})
			require.NoError(t, err)

			defer func() { require.NoError(t, m.DeleteStatementPolicy(ctx, p.ID)) }()

			ok, err := m.Evaluate(ctx, &AccessRequest{
				Groups: []string{adminName}, Verb: verbManage, ResourceType: tc.resource, CompartmentID: dev,
			})
			require.NoError(t, err)
			assert.True(t, ok)
		})
	}
}

func TestEvaluateRejectsUnknownVerb(t *testing.T) {
	m, dev, _, _ := evalFixture(t)

	_, err := m.Evaluate(t.Context(), &AccessRequest{
		Groups: []string{adminName}, Verb: "destroy", ResourceType: "buckets", CompartmentID: dev,
	})
	assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))
}

func TestCheckPermissionUsesGroupMembership(t *testing.T) {
	m := newMock(t)
	ctx := t.Context()

	_, err := m.CreateUser(ctx, driver.UserConfig{Name: "alice"})
	require.NoError(t, err)

	_, err = m.CreateGroup(ctx, driver.GroupConfig{Name: adminName})
	require.NoError(t, err)

	_, err = m.CreatePolicy(ctx, driver.PolicyConfig{
		Name:           "admins",
		PolicyDocument: "Allow group Admins to manage buckets in tenancy",
	})
	require.NoError(t, err)

	denied, err := m.CheckPermission(ctx, "alice", verbManage, "buckets")
	require.NoError(t, err)
	assert.False(t, denied, "a user outside the group is not granted")

	require.NoError(t, m.AddUserToGroup(ctx, "alice", adminName))

	allowed, err := m.CheckPermission(ctx, "alice", verbManage, "buckets")
	require.NoError(t, err)
	assert.True(t, allowed)

	wrongResource, err := m.CheckPermission(ctx, "alice", verbManage, "instances")
	require.NoError(t, err)
	assert.False(t, wrongResource)

	unknown, err := m.CheckPermission(ctx, "nobody", verbManage, "buckets")
	require.NoError(t, err)
	assert.False(t, unknown)
}

func TestPortablePolicyDocumentCarriesStatements(t *testing.T) {
	m := newMock(t)
	ctx := t.Context()

	doc := "Allow group Admins to manage buckets in tenancy\nAllow any-user to inspect buckets in tenancy"

	created, err := m.CreatePolicy(ctx, driver.PolicyConfig{Name: "mixed", PolicyDocument: doc})
	require.NoError(t, err)
	assert.Equal(t, doc, created.PolicyDocument)
	assert.Equal(t, created.ID, created.ARN)

	got, err := m.GetPolicy(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, doc, got.PolicyDocument)

	all, err := m.ListPolicies(ctx)
	require.NoError(t, err)
	assert.Len(t, all, 1)

	require.NoError(t, m.DeletePolicy(ctx, created.ID))
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(m.DeletePolicy(ctx, created.ID)))
}

func TestPolicyVersionsRecordStatementRevisions(t *testing.T) {
	m := newMock(t)
	ctx := t.Context()

	created, err := m.CreatePolicy(ctx, driver.PolicyConfig{
		Name:           "admins",
		PolicyDocument: "Allow group Admins to read buckets in tenancy",
	})
	require.NoError(t, err)

	v2, err := m.CreatePolicyVersion(ctx, driver.PolicyVersionConfig{
		PolicyARN:      created.ID,
		PolicyDocument: "Allow group Admins to manage buckets in tenancy",
		SetAsDefault:   true,
	})
	require.NoError(t, err)
	assert.Equal(t, "v2", v2.VersionID)
	assert.True(t, v2.IsDefaultVersion)

	versions, err := m.ListPolicyVersions(ctx, created.ID)
	require.NoError(t, err)
	assert.Len(t, versions, 2)

	got, err := m.GetPolicyVersion(ctx, created.ID, "v1")
	require.NoError(t, err)
	assert.False(t, got.IsDefaultVersion)

	// The default revision is what evaluation sees.
	_, err = m.CreateUser(ctx, driver.UserConfig{Name: "alice"})
	require.NoError(t, err)

	_, err = m.CreateGroup(ctx, driver.GroupConfig{Name: adminName})
	require.NoError(t, err)

	require.NoError(t, m.AddUserToGroup(ctx, "alice", adminName))

	allowed, err := m.CheckPermission(ctx, "alice", verbManage, "buckets")
	require.NoError(t, err)
	assert.True(t, allowed, "v2 raised read to manage")

	assert.Equal(t, cerrors.FailedPrecondition,
		cerrors.GetCode(m.DeletePolicyVersion(ctx, created.ID, "v2")))
	require.NoError(t, m.DeletePolicyVersion(ctx, created.ID, "v1"))

	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(m.SetDefaultPolicyVersion(ctx, created.ID, "v1")))
	require.NoError(t, m.SetDefaultPolicyVersion(ctx, created.ID, "v2"))

	_, err = m.ListPolicyVersions(ctx, "ocid1.policy.oc1..missing")
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))
}
