package binaryauthorization

import (
	"encoding/json"
	"time"

	badriver "github.com/stackshy/cloudemu/v2/services/binaryauthorization/driver"
)

// policyJSON is the wire shape of a Binary Authorization Policy. The three deep
// config blocks are carried as opaque JSON (json.RawMessage) so they round-trip
// verbatim without the control plane modeling every nested grammar. name and
// updateTime are output-only.
type policyJSON struct {
	Name                       string `json:"name,omitempty"`
	Description                string `json:"description,omitempty"`
	GlobalPolicyEvaluationMode string `json:"globalPolicyEvaluationMode,omitempty"`

	AdmissionWhitelistPatterns json.RawMessage `json:"admissionWhitelistPatterns,omitempty"`
	DefaultAdmissionRule       json.RawMessage `json:"defaultAdmissionRule,omitempty"`
	ClusterAdmissionRules      json.RawMessage `json:"clusterAdmissionRules,omitempty"`

	Etag       string `json:"etag,omitempty"`
	UpdateTime string `json:"updateTime,omitempty"`
}

// attestorJSON is the wire shape of a Binary Authorization Attestor. name and
// updateTime are output-only.
type attestorJSON struct {
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`

	UserOwnedGrafeasNote *grafeasNoteJSON `json:"userOwnedGrafeasNote,omitempty"`

	Etag       string `json:"etag,omitempty"`
	UpdateTime string `json:"updateTime,omitempty"`
}

// grafeasNoteJSON carries the note reference and verification keys.
// delegationServiceAccountEmail is output-only. publicKeys is opaque JSON so the
// PgpPublicKey/PkixPublicKey grammar round-trips verbatim.
type grafeasNoteJSON struct {
	NoteReference                 string          `json:"noteReference,omitempty"`
	PublicKeys                    json.RawMessage `json:"publicKeys,omitempty"`
	DelegationServiceAccountEmail string          `json:"delegationServiceAccountEmail,omitempty"`
}

type listAttestorsResponse struct {
	Attestors     []attestorJSON `json:"attestors,omitempty"`
	NextPageToken string         `json:"nextPageToken,omitempty"`
}

// iamPolicyJSON is the GCP IAM Policy resource returned by getIamPolicy /
// setIamPolicy.
type iamPolicyJSON struct {
	Version  int              `json:"version,omitempty"`
	Bindings []iamBindingJSON `json:"bindings,omitempty"`
	Etag     string           `json:"etag,omitempty"`
}

type iamBindingJSON struct {
	Role    string   `json:"role"`
	Members []string `json:"members,omitempty"`
}

type setIamPolicyRequest struct {
	Policy iamPolicyJSON `json:"policy"`
}

type testIamPermissionsRequest struct {
	Permissions []string `json:"permissions"`
}

type testIamPermissionsResponse struct {
	Permissions []string `json:"permissions,omitempty"`
}

// toPolicyConfig converts a decoded wire policy into a driver PolicyConfig.
func (p *policyJSON) toPolicyConfig() badriver.PolicyConfig {
	return badriver.PolicyConfig{
		Description:                p.Description,
		GlobalPolicyEvaluationMode: p.GlobalPolicyEvaluationMode,
		AdmissionWhitelistPatterns: p.AdmissionWhitelistPatterns,
		DefaultAdmissionRule:       p.DefaultAdmissionRule,
		ClusterAdmissionRules:      p.ClusterAdmissionRules,
		Etag:                       p.Etag,
	}
}

// toPolicyJSON renders a driver Policy for the wire.
func toPolicyJSON(p *badriver.Policy) policyJSON {
	return policyJSON{
		Name:                       p.Name,
		Description:                p.Description,
		GlobalPolicyEvaluationMode: p.GlobalPolicyEvaluationMode,
		AdmissionWhitelistPatterns: p.AdmissionWhitelistPatterns,
		DefaultAdmissionRule:       p.DefaultAdmissionRule,
		ClusterAdmissionRules:      p.ClusterAdmissionRules,
		Etag:                       p.Etag,
		UpdateTime:                 formatTime(p.UpdateTime),
	}
}

// toAttestorConfig converts a decoded wire attestor into a driver AttestorConfig.
// name is supplied by the handler (the full resource name).
func (a *attestorJSON) toAttestorConfig(name string) badriver.AttestorConfig {
	return badriver.AttestorConfig{
		Name:                 name,
		Description:          a.Description,
		UserOwnedGrafeasNote: a.UserOwnedGrafeasNote.toDriver(),
		Etag:                 a.Etag,
	}
}

func (n *grafeasNoteJSON) toDriver() *badriver.UserOwnedGrafeasNote {
	if n == nil {
		return nil
	}

	return &badriver.UserOwnedGrafeasNote{
		NoteReference:                 n.NoteReference,
		PublicKeys:                    n.PublicKeys,
		DelegationServiceAccountEmail: n.DelegationServiceAccountEmail,
	}
}

// toAttestorJSON renders a driver Attestor for the wire.
func toAttestorJSON(a *badriver.Attestor) attestorJSON {
	return attestorJSON{
		Name:                 a.Name,
		Description:          a.Description,
		UserOwnedGrafeasNote: noteToJSON(a.UserOwnedGrafeasNote),
		Etag:                 a.Etag,
		UpdateTime:           formatTime(a.UpdateTime),
	}
}

func noteToJSON(n *badriver.UserOwnedGrafeasNote) *grafeasNoteJSON {
	if n == nil {
		return nil
	}

	return &grafeasNoteJSON{
		NoteReference:                 n.NoteReference,
		PublicKeys:                    n.PublicKeys,
		DelegationServiceAccountEmail: n.DelegationServiceAccountEmail,
	}
}

// toPolicyIAMJSON renders a driver IAM policy for the wire.
func toPolicyIAMJSON(pol *badriver.IAMPolicy) iamPolicyJSON {
	out := iamPolicyJSON{Version: pol.Version, Etag: pol.Etag}
	for _, b := range pol.Bindings {
		out.Bindings = append(out.Bindings, iamBindingJSON{Role: b.Role, Members: b.Members})
	}

	return out
}

// fromPolicyIAMJSON decodes a wire IAM policy into the driver model.
func fromPolicyIAMJSON(pol iamPolicyJSON) badriver.IAMPolicy {
	out := badriver.IAMPolicy{Version: pol.Version, Etag: pol.Etag}
	for _, b := range pol.Bindings {
		out.Bindings = append(out.Bindings, badriver.IAMBinding{Role: b.Role, Members: b.Members})
	}

	return out
}

// formatTime renders a timestamp as RFC3339 (proto3 JSON), or "" when zero so
// the omitempty output-only field is dropped.
func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}

	return t.UTC().Format(time.RFC3339Nano)
}
