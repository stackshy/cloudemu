// Package scope identifies the cloud-side container a resource lives in —
// the Azure subscription/resource group, the GCP project, or the OCI
// compartment. Drivers record a resource's scope at create time and filter
// lists by it, so scoped list endpoints (ListByResourceGroup, per-project
// lists, compartmentId queries) return only what the caller's scope actually
// contains.
package scope

// Scope locates a resource. The zero value means "unscoped": AWS resources
// and portable-API callers that don't care about scoping use it, and it
// matches every filter.
type Scope struct {
	Subscription  string
	ResourceGroup string
	Project       string
	// Compartment is the OCI compartment OCID. Matching is exact: real OCI
	// only descends the compartment tree when compartmentIdInSubtree is set.
	Compartment string
}

// IsZero reports whether no scope information is set.
func (s Scope) IsZero() bool {
	return s == Scope{}
}

// Matches reports whether a resource created in scope s is visible under
// filter f. Empty filter fields match anything, so a zero filter lists
// everything and a subscription-only filter spans its resource groups.
// Resources created without scope (portable API) are visible everywhere —
// hiding them from scoped lists would make them unreachable over the wire.
func (s Scope) Matches(f Scope) bool {
	if s.IsZero() {
		return true
	}
	if f.Subscription != "" && s.Subscription != f.Subscription {
		return false
	}
	if f.ResourceGroup != "" && s.ResourceGroup != f.ResourceGroup {
		return false
	}
	if f.Project != "" && s.Project != f.Project {
		return false
	}
	if f.Compartment != "" && s.Compartment != f.Compartment {
		return false
	}
	return true
}
