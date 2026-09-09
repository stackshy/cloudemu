// Package binaryauthorization provides an in-memory backend for GCP Binary
// Authorization (binaryauthorization.googleapis.com v1). It satisfies
// services/binaryauthorization/driver.BinaryAuthorization so the v1 REST wire
// handler (server/gcp/binaryauthorization) serves real
// google.golang.org/api/binaryauthorization/v1 clients — and Terraform's google
// provider (google_binary_authorization_policy / google_binary_authorization_
// attestor) — against it.
//
// This is the control plane only: the per-project Policy singleton and the
// Attestor CRUD surface plus the attestor IAM methods. All operations are
// synchronous. Deep config blocks are stored as opaque JSON and echoed verbatim.
package binaryauthorization

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/binaryauthorization/driver"
)

// Compile-time check that Mock implements driver.BinaryAuthorization.
var _ driver.BinaryAuthorization = (*Mock)(nil)

// defaultAdmissionRule is the ALWAYS_ALLOW rule real Binary Authorization seeds
// for a project that has never set a policy.
//
//nolint:gochecknoglobals // immutable default-policy fixture
var defaultAdmissionRule = json.RawMessage(
	`{"evaluationMode":"ALWAYS_ALLOW","enforcementMode":"ENFORCED_BLOCK_AND_AUDIT_LOG"}`)

// Mock is an in-memory Binary Authorization backend. The policy singleton is
// keyed by project id; attestors are keyed by their full resource name
// (projects/{p}/attestors/{id}).
type Mock struct {
	mu        sync.Mutex
	policies  *memstore.Store[*driver.Policy]
	attestors *memstore.Store[*driver.Attestor]
	opts      *config.Options
}

// New creates a Binary Authorization mock with the given options.
func New(opts *config.Options) *Mock {
	return &Mock{
		policies:  memstore.New[*driver.Policy](),
		attestors: memstore.New[*driver.Attestor](),
		opts:      opts,
	}
}

// policyName returns the singleton resource name for a project.
func policyName(project string) string {
	return "projects/" + project + "/policy"
}

// GetPolicy returns the project's policy, seeding and storing a default
// ALWAYS_ALLOW policy on first read so the response is byte-stable thereafter.
func (m *Mock) GetPolicy(_ context.Context, project string) (*driver.Policy, error) {
	if project == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "project is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if p, ok := m.policies.Get(project); ok {
		return clonePolicy(p), nil
	}

	seeded := &driver.Policy{
		Name:                       policyName(project),
		GlobalPolicyEvaluationMode: driver.GlobalPolicyEvaluationModeEnable,
		DefaultAdmissionRule:       cloneBytes(defaultAdmissionRule),
		Etag:                       newEtag(),
		UpdateTime:                 m.opts.Clock.Now(),
	}
	m.policies.Set(project, seeded)

	return clonePolicy(seeded), nil
}

// UpdatePolicy fully replaces the project's policy and returns it.
func (m *Mock) UpdatePolicy(_ context.Context, project string, cfg *driver.PolicyConfig) (*driver.Policy, error) {
	if project == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "project is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	p := &driver.Policy{
		Name:                       policyName(project),
		Description:                cfg.Description,
		GlobalPolicyEvaluationMode: cfg.GlobalPolicyEvaluationMode,
		AdmissionWhitelistPatterns: cloneBytes(cfg.AdmissionWhitelistPatterns),
		DefaultAdmissionRule:       cloneBytes(cfg.DefaultAdmissionRule),
		ClusterAdmissionRules:      cloneBytes(cfg.ClusterAdmissionRules),
		Etag:                       newEtag(),
		UpdateTime:                 m.opts.Clock.Now(),
	}
	m.policies.Set(project, p)

	return clonePolicy(p), nil
}

// CreateAttestor stores a new attestor, failing with AlreadyExists on a taken id.
func (m *Mock) CreateAttestor(
	_ context.Context, project, attestorID string, cfg driver.AttestorConfig,
) (*driver.Attestor, error) {
	if project == "" || attestorID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "project and attestorId are required")
	}

	name := "projects/" + project + "/attestors/" + attestorID

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.attestors.Has(name) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "attestor %q already exists", name)
	}

	a := &driver.Attestor{
		Name:                 name,
		Description:          cfg.Description,
		UserOwnedGrafeasNote: noteWithDelegation(cfg.UserOwnedGrafeasNote, project),
		Etag:                 newEtag(),
		UpdateTime:           m.opts.Clock.Now(),
	}
	m.attestors.Set(name, a)

	return cloneAttestor(a), nil
}

// GetAttestor returns an attestor by its full resource name.
func (m *Mock) GetAttestor(_ context.Context, name string) (*driver.Attestor, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	a, ok := m.attestors.Get(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "attestor %q not found", name)
	}

	return cloneAttestor(a), nil
}

// ListAttestors returns every attestor under a project, in deterministic order.
func (m *Mock) ListAttestors(_ context.Context, project string) ([]driver.Attestor, error) {
	prefix := "projects/" + project + "/attestors/"

	m.mu.Lock()
	defer m.mu.Unlock()

	all := m.attestors.SortedValues()
	out := make([]driver.Attestor, 0, len(all))

	for _, a := range all {
		if strings.HasPrefix(a.Name, prefix) {
			out = append(out, *cloneAttestor(a))
		}
	}

	return out, nil
}

// UpdateAttestor fully replaces an existing attestor's mutable fields.
func (m *Mock) UpdateAttestor(_ context.Context, cfg driver.AttestorConfig) (*driver.Attestor, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	existing, ok := m.attestors.Get(cfg.Name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "attestor %q not found", cfg.Name)
	}

	a := &driver.Attestor{
		Name:                 existing.Name,
		Description:          cfg.Description,
		UserOwnedGrafeasNote: noteWithDelegation(cfg.UserOwnedGrafeasNote, projectOf(existing.Name)),
		Etag:                 newEtag(),
		UpdateTime:           m.opts.Clock.Now(),
		IAMPolicy:            existing.IAMPolicy,
	}
	m.attestors.Set(cfg.Name, a)

	return cloneAttestor(a), nil
}

// DeleteAttestor removes an attestor by its full resource name.
func (m *Mock) DeleteAttestor(_ context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.attestors.Has(name) {
		return cerrors.Newf(cerrors.NotFound, "attestor %q not found", name)
	}

	m.attestors.Delete(name)

	return nil
}

// noteWithDelegation returns a copy of the input note with a deterministic,
// project-derived delegationServiceAccountEmail filled in (output-only). A nil
// input note yields nil.
func noteWithDelegation(in *driver.UserOwnedGrafeasNote, project string) *driver.UserOwnedGrafeasNote {
	if in == nil {
		return nil
	}

	return &driver.UserOwnedGrafeasNote{
		NoteReference:                 in.NoteReference,
		PublicKeys:                    cloneBytes(in.PublicKeys),
		DelegationServiceAccountEmail: delegationEmail(project),
	}
}

// delegationEmail returns the deterministic Binary Authorization service-agent
// email for a project — stable across reads, matching the computed field a
// Terraform practitioner sees.
func delegationEmail(project string) string {
	return "service-" + project + "@gcp-sa-binaryauthorization.iam.gserviceaccount.com"
}

// projectOf extracts the project id from a projects/{p}/... resource name.
func projectOf(name string) string {
	parts := strings.Split(name, "/")
	if len(parts) >= 2 && parts[0] == "projects" {
		return parts[1]
	}

	return ""
}

// newEtag returns a fresh opaque optimistic-concurrency tag.
func newEtag() string {
	return idgen.GenerateID("etag-")
}
