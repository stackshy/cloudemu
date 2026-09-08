// Package driver defines the minimal interface an in-memory GCP Binary
// Authorization backend must implement. Binary Authorization is GCP-only, so
// there is a single provider implementation (providers/gcp/binaryauthorization)
// rather than the usual three; the interface still lives here so the wire
// handler (server/gcp/binaryauthorization) depends on an abstraction rather than
// the concrete mock.
//
// Scope is the binaryauthorization.googleapis.com v1 control plane: the
// per-project Policy singleton (getPolicy/updatePolicy) and the Attestor CRUD
// surface (create/get/list/update/delete) plus the attestor
// getIamPolicy/setIamPolicy/testIamPermissions IAM methods. All operations are
// SYNCHRONOUS — Binary Authorization has no long-running operations. Deep config
// blocks (admissionWhitelistPatterns, defaultAdmissionRule, clusterAdmissionRules
// and an attestor's publicKeys) are stored as opaque JSON so they round-trip
// verbatim without the control plane modeling every nested grammar; integer
// enums inside them are normalized to their canonical names at the wire layer.
package driver

import (
	"context"
	"encoding/json"
	"time"
)

// GlobalPolicyEvaluationMode enum names
// (google.cloud.binaryauthorization.v1.Policy.GlobalPolicyEvaluationMode). The
// ordinals are GLOBAL_POLICY_EVALUATION_MODE_UNSPECIFIED=0, ENABLE=1, DISABLE=2.
const (
	GlobalPolicyEvaluationModeUnspecified = "GLOBAL_POLICY_EVALUATION_MODE_UNSPECIFIED"
	GlobalPolicyEvaluationModeEnable      = "ENABLE"
	GlobalPolicyEvaluationModeDisable     = "DISABLE"
)

// BinaryAuthorization is the control-plane surface for Binary Authorization. The
// Policy is a per-project singleton addressed as projects/{p}/policy; attestors
// are addressed by their full resource name projects/{p}/attestors/{id}.
type BinaryAuthorization interface {
	// GetPolicy returns the project's policy. A project that has never set a
	// policy gets a sensible default (ALWAYS_ALLOW) seeded and stored on first
	// read, so the response is byte-stable across subsequent reads.
	GetPolicy(ctx context.Context, project string) (*Policy, error)

	// UpdatePolicy fully replaces the project's policy and returns it. The policy
	// singleton is never created or deleted; an update materializes it. cfg must
	// be non-nil.
	UpdatePolicy(ctx context.Context, project string, cfg *PolicyConfig) (*Policy, error)

	// CreateAttestor stores a new attestor under projects/{project}/attestors/
	// {attestorID}. It fails with AlreadyExists if the id is taken.
	CreateAttestor(ctx context.Context, project, attestorID string, cfg AttestorConfig) (*Attestor, error)

	// GetAttestor returns an attestor by its full resource name.
	GetAttestor(ctx context.Context, name string) (*Attestor, error)

	// ListAttestors returns every attestor under the given project, in
	// deterministic name order.
	ListAttestors(ctx context.Context, project string) ([]Attestor, error)

	// UpdateAttestor fully replaces the mutable fields of an existing attestor
	// (cfg.Name is the full resource name) and returns it. It fails with NotFound
	// if the attestor does not exist.
	UpdateAttestor(ctx context.Context, cfg AttestorConfig) (*Attestor, error)

	// DeleteAttestor removes an attestor by its full resource name.
	DeleteAttestor(ctx context.Context, name string) error

	// GetIamPolicy returns the attestor's stored IAM policy (an empty, versioned
	// policy when none was set). The attestor must exist.
	GetIamPolicy(ctx context.Context, name string) (*IAMPolicy, error)

	// SetIamPolicy stores the attestor's IAM policy and returns it with a
	// refreshed etag. The attestor must exist.
	SetIamPolicy(ctx context.Context, name string, policy IAMPolicy) (*IAMPolicy, error)

	// TestIamPermissions echoes back the requested permissions (CloudEmu does not
	// enforce IAM). The attestor must exist.
	TestIamPermissions(ctx context.Context, name string, permissions []string) ([]string, error)
}

// Policy is a stored Binary Authorization policy (the per-project singleton).
// The three deep blocks are opaque JSON so they round-trip verbatim; Name and
// UpdateTime are output-only, minted by the backend and stored so reads are
// byte-stable (no clock read on GET).
type Policy struct {
	Name                       string
	Description                string
	GlobalPolicyEvaluationMode string

	AdmissionWhitelistPatterns json.RawMessage
	DefaultAdmissionRule       json.RawMessage
	ClusterAdmissionRules      json.RawMessage

	Etag       string
	UpdateTime time.Time
}

// PolicyConfig is the input to UpdatePolicy (a full replace). It mirrors the
// mutable subset of Policy — Name and UpdateTime are managed by the backend.
type PolicyConfig struct {
	Description                string
	GlobalPolicyEvaluationMode string

	AdmissionWhitelistPatterns json.RawMessage
	DefaultAdmissionRule       json.RawMessage
	ClusterAdmissionRules      json.RawMessage

	Etag string
}

// Attestor is a stored Binary Authorization attestor. Name, UpdateTime and the
// note's DelegationServiceAccountEmail are output-only, minted once and stored so
// reads are byte-stable.
type Attestor struct {
	Name        string
	Description string

	UserOwnedGrafeasNote *UserOwnedGrafeasNote

	Etag       string
	UpdateTime time.Time

	// IAMPolicy is the attestor's stored IAM policy, nil until first set. It is
	// not part of the Attestor wire shape; it is served by the getIamPolicy
	// method.
	IAMPolicy *IAMPolicy
}

// UserOwnedGrafeasNote binds an attestor to a Grafeas note and its verification
// keys. PublicKeys is opaque JSON (the PgpPublicKey/PkixPublicKey grammar) so it
// round-trips verbatim. DelegationServiceAccountEmail is output-only and computed
// deterministically from the project.
type UserOwnedGrafeasNote struct {
	NoteReference                 string
	PublicKeys                    json.RawMessage
	DelegationServiceAccountEmail string
}

// AttestorConfig is the input to CreateAttestor/UpdateAttestor. Name is the full
// resource name; the note's DelegationServiceAccountEmail is ignored on input
// (the backend computes it).
type AttestorConfig struct {
	Name        string
	Description string

	UserOwnedGrafeasNote *UserOwnedGrafeasNote

	Etag string
}

// IAMPolicy is a GCP IAM policy (the getIamPolicy/setIamPolicy resource).
type IAMPolicy struct {
	Version  int
	Bindings []IAMBinding
	Etag     string
}

// IAMBinding associates a role with a list of members.
type IAMBinding struct {
	Role    string
	Members []string
}
