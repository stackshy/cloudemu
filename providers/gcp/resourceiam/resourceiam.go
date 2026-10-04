// Package resourceiam is the shared store for google.iam.v1 resource policies
// (getIamPolicy / setIamPolicy) of GCP resources that have no IAM model of
// their own. Policies are keyed by the full resource name, for example
// "projects/p/instances/i", so they are scoped per project by construction.
//
// The store follows the real IAM contract
// (https://cloud.google.com/iam/docs/policies#etag): an unset policy reads as
// version 1 with a stable initial etag, every write mints a new etag, a write
// carrying a stale etag is rejected with ErrAborted, an empty etag is a blind
// overwrite, and a conditional binding needs policy version 3.
package resourceiam

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
)

// conditionalVersion is the lowest policy version that may carry conditions.
const conditionalVersion = 3

// ErrAborted reports a setIamPolicy whose etag no longer matches the stored
// policy. Real GCP answers it with 409 ABORTED.
var ErrAborted = errors.New("there were concurrent policy changes; " +
	"please retry the whole read-modify-write with the new etag")

// Expr is a google.type.Expr binding condition.
type Expr struct {
	Expression  string `json:"expression,omitempty"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	Location    string `json:"location,omitempty"`
}

// Binding grants role to members, optionally under a condition.
type Binding struct {
	Role      string   `json:"role,omitempty"`
	Members   []string `json:"members,omitempty"`
	Condition *Expr    `json:"condition,omitempty"`
}

// AuditLogConfig is one log type of an audit config.
type AuditLogConfig struct {
	LogType         string   `json:"logType,omitempty"`
	ExemptedMembers []string `json:"exemptedMembers,omitempty"`
}

// AuditConfig is the audit logging configuration for one service.
type AuditConfig struct {
	Service         string           `json:"service,omitempty"`
	AuditLogConfigs []AuditLogConfig `json:"auditLogConfigs,omitempty"`
}

// Policy is a google.iam.v1 Policy.
type Policy struct {
	Version      int           `json:"version,omitempty"`
	Bindings     []Binding     `json:"bindings,omitempty"`
	AuditConfigs []AuditConfig `json:"auditConfigs,omitempty"`
	Etag         string        `json:"etag,omitempty"`
}

// Mock holds resource policies keyed by full resource name.
type Mock struct {
	// mu makes Set's etag compare and write one atomic step.
	mu       sync.Mutex
	policies *memstore.Store[Policy]
}

// New returns an empty policy store.
func New() *Mock {
	return &Mock{policies: memstore.New[Policy]()}
}

// Get returns the policy of name, or version 1 with the initial etag when
// none was ever set.
func (m *Mock) Get(name string) Policy {
	p, ok := m.policies.Get(name)
	if !ok {
		return Policy{Version: 1, Etag: InitialEtag()}
	}

	return clonePolicy(p)
}

// Set writes p as the policy of name and returns the stored policy with its
// new etag. updateMask is the comma-separated FieldMask of the request; empty
// means "bindings,etag", so stored auditConfigs survive unless named.
func (m *Mock) Set(name string, p Policy, updateMask string) (Policy, error) {
	version := max(p.Version, 1)

	bindings, err := checkBindings(version, p.Bindings)
	if err != nil {
		return Policy{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	cur, ok := m.policies.Get(name)
	curEtag := InitialEtag()

	if ok {
		curEtag = cur.Etag
	}

	if p.Etag != "" && p.Etag != curEtag {
		return Policy{}, ErrAborted
	}

	next := Policy{Version: version, Bindings: cur.Bindings, AuditConfigs: cur.AuditConfigs}
	fields := maskFields(updateMask)

	if fields["bindings"] {
		next.Bindings = bindings
	}

	if fields["auditconfigs"] {
		next.AuditConfigs = p.AuditConfigs
	}

	next.Etag = NextEtag(curEtag)
	stored := clonePolicy(next)
	m.policies.Set(name, stored)

	return clonePolicy(stored), nil
}

// Delete drops the policy of name and of every resource below it, so a
// recreated resource starts with an empty policy.
func (m *Mock) Delete(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.policies.Delete(name)

	for _, k := range m.policies.Keys() {
		if strings.HasPrefix(k, name+"/") {
			m.policies.Delete(k)
		}
	}
}

// Snapshot serializes every stored policy.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	return m.policies.Snapshot()
}

// Restore loads policies captured by Snapshot.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	if err := m.policies.LoadSnapshot(data); err != nil {
		return fmt.Errorf("resourceiam: parse snapshot: %w", err)
	}

	return nil
}

// checkBindings rejects a conditional binding below version 3 and drops
// bindings with no members, which real IAM does not store.
func checkBindings(version int, in []Binding) ([]Binding, error) {
	out := make([]Binding, 0, len(in))

	for _, b := range in {
		if b.Condition != nil && version < conditionalVersion {
			return nil, cerrors.Newf(cerrors.InvalidArgument,
				"policy version %d cannot contain the conditional binding for role %s; use version 3", version, b.Role)
		}

		if len(b.Members) > 0 {
			out = append(out, b)
		}
	}

	return out, nil
}

// maskFields returns the lowercased, underscore-free field names of mask, or
// the IAM default "bindings,etag" when mask is empty.
func maskFields(mask string) map[string]bool {
	if strings.TrimSpace(mask) == "" {
		mask = "bindings,etag"
	}

	out := map[string]bool{}

	for _, f := range strings.Split(mask, ",") {
		f = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(f), "_", ""))
		out[strings.TrimPrefix(f, "policy.")] = true
	}

	return out
}

// clonePolicy deep-copies p so callers never share slices with the store.
func clonePolicy(p Policy) Policy {
	out := Policy{Version: p.Version, Etag: p.Etag}

	for _, b := range p.Bindings {
		nb := Binding{Role: b.Role, Members: append([]string(nil), b.Members...)}

		if b.Condition != nil {
			c := *b.Condition
			nb.Condition = &c
		}

		out.Bindings = append(out.Bindings, nb)
	}

	for _, a := range p.AuditConfigs {
		na := AuditConfig{Service: a.Service}
		for _, l := range a.AuditLogConfigs {
			na.AuditLogConfigs = append(na.AuditLogConfigs,
				AuditLogConfig{LogType: l.LogType, ExemptedMembers: append([]string(nil), l.ExemptedMembers...)})
		}

		out.AuditConfigs = append(out.AuditConfigs, na)
	}

	return out
}
