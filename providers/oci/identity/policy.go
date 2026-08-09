package identity

import (
	"context"
	"fmt"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/iam/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

// maxPolicyVersions caps the revisions kept for one policy.
const maxPolicyVersions = 5

// policy is an OCI policy: a named list of English-like statements attached to
// a compartment.
type policy struct {
	ID             string
	Name           string
	Description    string
	Statements     []string
	TimeCreated    string
	VersionDate    string
	Scope          scope.Scope
	FreeformTags   map[string]string
	parsed         []statement
	versions       []*policyRevision
	versionCounter int
}

// policyRevision is one recorded revision of a policy's statements, which is
// what the portable policy-version operations map onto.
type policyRevision struct {
	VersionID  string
	Statements []string
	IsDefault  bool
	CreatedAt  string
}

// CreateStatementPolicy creates a policy from its statements.
func (m *Mock) CreateStatementPolicy(_ context.Context, spec *driver.PolicySpec) (*driver.StatementPolicyInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.createPolicy(spec)
}

// createPolicy creates a policy from its statements. Callers hold m.mu.
func (m *Mock) createPolicy(spec *driver.PolicySpec) (*driver.StatementPolicyInfo, error) {
	if err := validateName(kindPolicy, spec.Name); err != nil {
		return nil, err
	}

	parsed, err := parseStatements(spec.Statements)
	if err != nil {
		return nil, err
	}

	compartmentID := m.compartmentOr(spec.CompartmentID)
	if _, found := m.policyNamed(compartmentID, spec.Name); found {
		return nil, cerrors.Newf(cerrors.AlreadyExists,
			"policy %q already exists in compartment %q", spec.Name, compartmentID)
	}

	now := m.now()
	p := &policy{
		ID:           idgen.GlobalOCID(kindPolicy, m.opts.Realm),
		Name:         spec.Name,
		Description:  spec.Description,
		Statements:   copyStrings(spec.Statements),
		TimeCreated:  now,
		VersionDate:  now,
		Scope:        scope.Scope{Compartment: compartmentID},
		FreeformTags: copyTags(spec.FreeformTags),
		parsed:       parsed,
	}
	p.addRevision(spec.Statements, now, true)
	m.policies.Set(p.ID, p)

	return toStatementPolicyInfo(p), nil
}

// GetStatementPolicy returns the policy with the given OCID.
func (m *Mock) GetStatementPolicy(_ context.Context, id string) (*driver.StatementPolicyInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.statementPolicy(id)
}

// statementPolicy returns the policy with the given OCID. Callers hold m.mu.
func (m *Mock) statementPolicy(id string) (*driver.StatementPolicyInfo, error) {
	p, ok := m.policies.Get(id)
	if !ok {
		return nil, policyNotFound(id)
	}

	return toStatementPolicyInfo(p), nil
}

// ListStatementPolicies returns the policies attached to one compartment.
func (m *Mock) ListStatementPolicies(_ context.Context, compartmentID string) ([]driver.StatementPolicyInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	filter := scope.Scope{Compartment: compartmentID}
	all := m.policies.SortedValues()
	out := make([]driver.StatementPolicyInfo, 0, len(all))

	for _, p := range all {
		if p.Scope.Matches(filter) {
			out = append(out, *toStatementPolicyInfo(p))
		}
	}

	return out, nil
}

// UpdateStatementPolicy applies the mutable fields of a policy, re-parsing the
// statements when they are replaced.
func (m *Mock) UpdateStatementPolicy(
	_ context.Context, id string, upd driver.PolicyUpdate,
) (*driver.StatementPolicyInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	p, ok := m.policies.Get(id)
	if !ok {
		return nil, policyNotFound(id)
	}

	if upd.Statements != nil {
		parsed, err := parseStatements(upd.Statements)
		if err != nil {
			return nil, err
		}

		now := m.now()
		p.parsed = parsed
		p.VersionDate = now
		p.Statements = copyStrings(upd.Statements)

		p.addRevision(upd.Statements, now, true)
	}

	if upd.Description != "" {
		p.Description = upd.Description
	}

	if upd.FreeformTags != nil {
		p.FreeformTags = copyTags(upd.FreeformTags)
	}

	m.policies.Set(id, p)

	return toStatementPolicyInfo(p), nil
}

// DeleteStatementPolicy deletes the policy with the given OCID.
func (m *Mock) DeleteStatementPolicy(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.deletePolicy(id)
}

// deletePolicy deletes the policy with the given OCID. Callers hold m.mu.
func (m *Mock) deletePolicy(id string) error {
	if !m.policies.Delete(id) {
		return policyNotFound(id)
	}

	return nil
}

// Evaluate reports whether any statement grants the request. OCI policies only
// ever allow, so the first match settles it. A statement this emulator cannot
// resolve is reported as Unimplemented rather than granted.
func (m *Mock) Evaluate(_ context.Context, req *driver.AccessRequest) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.evaluate(req)
}

// evaluate resolves an access request against every policy. Callers hold m.mu.
func (m *Mock) evaluate(req *driver.AccessRequest) (bool, error) {
	if verbRank(strings.ToLower(req.Verb)) == rankUnknown {
		return false, cerrors.Newf(cerrors.InvalidArgument,
			"unknown verb %q, want inspect, read, use or manage", req.Verb)
	}

	target := m.compartmentOr(req.CompartmentID)

	var undisclosed error

	for _, p := range m.policies.SortedValues() {
		for i := range p.parsed {
			granted, err := m.applies(p, &p.parsed[i], req, target)
			if granted {
				return true, nil
			}

			if err != nil && undisclosed == nil {
				undisclosed = err
			}
		}
	}

	return false, undisclosed
}

// applies reports whether one statement grants the request in target, or the
// error disclosing why it reaches the request but cannot be resolved.
func (m *Mock) applies(p *policy, st *statement, req *driver.AccessRequest, target string) (bool, error) {
	cover := st.grantsAccess(req)
	if cover == coverDenied {
		return false, nil
	}

	granted, ok := m.resolveLocation(p, st)
	if !ok || !m.covers(granted, target) {
		return false, nil
	}

	if err := st.unresolved(cover); err != nil {
		return false, err
	}

	return true, nil
}

// resolveLocation turns a statement's location into the compartment whose
// subtree it grants. A policy never reaches outside the compartment it is
// attached to, so "in tenancy" on a nested policy means that subtree.
func (m *Mock) resolveLocation(p *policy, st *statement) (string, bool) {
	if st.LocationKind == locationTenancy {
		return p.Scope.Compartment, true
	}

	if st.LocationByID {
		if !m.covers(p.Scope.Compartment, st.Location) {
			return "", false
		}

		return st.Location, true
	}

	return m.resolvePath(p.Scope.Compartment, st.Location)
}

// policyNamed returns the policy with the given name in a compartment.
func (m *Mock) policyNamed(compartmentID, name string) (*policy, bool) {
	for _, p := range m.policies.SortedValues() {
		if p.Scope.Compartment == compartmentID && strings.EqualFold(p.Name, name) {
			return p, true
		}
	}

	return nil, false
}

// addRevision records a revision of the policy's statements, dropping the
// oldest once the cap is reached.
func (p *policy) addRevision(statements []string, createdAt string, isDefault bool) *policyRevision {
	if isDefault {
		clearDefaults(p.versions)
	}

	p.versionCounter++
	rev := &policyRevision{
		VersionID:  fmt.Sprintf("v%d", p.versionCounter),
		Statements: copyStrings(statements),
		IsDefault:  isDefault,
		CreatedAt:  createdAt,
	}
	p.versions = append(p.versions, rev)

	if len(p.versions) > maxPolicyVersions {
		p.versions = p.versions[1:]
	}

	return rev
}

// clearDefaults unmarks every revision as the default.
func clearDefaults(versions []*policyRevision) {
	for _, v := range versions {
		v.IsDefault = false
	}
}

// parseStatements parses every statement, rejecting the whole set if one is
// malformed, the way real OCI rejects a policy it cannot parse.
func parseStatements(statements []string) ([]statement, error) {
	if len(statements) == 0 {
		return nil, cerrors.New(cerrors.InvalidArgument, "a policy needs at least one statement")
	}

	parsed := make([]statement, 0, len(statements))

	for _, text := range statements {
		st, err := parseStatement(text)
		if err != nil {
			return nil, err
		}

		parsed = append(parsed, st)
	}

	return parsed, nil
}

// copyStrings copies a statement list so a caller cannot mutate stored state.
func copyStrings(in []string) []string {
	out := make([]string, len(in))
	copy(out, in)

	return out
}

func policyNotFound(id string) error {
	return cerrors.Newf(cerrors.NotFound, "policy %q not found", id)
}

func toStatementPolicyInfo(p *policy) *driver.StatementPolicyInfo {
	return &driver.StatementPolicyInfo{
		ID:             p.ID,
		CompartmentID:  p.Scope.Compartment,
		Name:           p.Name,
		Description:    p.Description,
		Statements:     copyStrings(p.Statements),
		TimeCreated:    p.TimeCreated,
		VersionDate:    p.VersionDate,
		LifecycleState: lifecycleActive,
		FreeformTags:   copyTags(p.FreeformTags),
	}
}
