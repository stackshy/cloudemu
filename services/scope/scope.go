// Package scope identifies the cloud-side container a resource lives in:
// the Azure subscription/resource group, the GCP project, or the OCI
// compartment. Drivers record a resource's scope at create time and filter
// lists by it, so scoped list endpoints (ListByResourceGroup, per-project
// lists, compartmentId queries) return only what the caller's scope actually
// contains.
package scope

import "strings"

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
// Resources created without scope (portable API) are visible everywhere;
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

// InResourceGroup reports whether a resource created in scope s belongs to the
// Azure resource group resourceGroup in subscription. Unlike Matches it never
// treats the zero scope as a wildcard, so an unscoped (portable API) resource
// is never selected for a resource-group delete. Names compare
// case-insensitively, as ARM does. An empty subscription on either side
// matches any subscription: the emulator serves a single estate.
func (s Scope) InResourceGroup(subscription, resourceGroup string) bool {
	if s.ResourceGroup == "" || !strings.EqualFold(s.ResourceGroup, resourceGroup) {
		return false
	}

	return s.Subscription == "" || subscription == "" || strings.EqualFold(s.Subscription, subscription)
}

// IDInResourceGroup reports whether the ARM resource id or scope string id is
// the resource group resourceGroup of subscription, or lies under it. Leading
// and trailing slashes are ignored and segments compare case-insensitively.
// The match stops at a segment boundary, so rg1 never selects rg10.
func IDInResourceGroup(id, subscription, resourceGroup string) bool {
	if subscription == "" || resourceGroup == "" {
		return false
	}

	got := strings.ToLower(strings.Trim(id, "/"))
	want := strings.ToLower("subscriptions/" + subscription + "/resourcegroups/" + resourceGroup)

	return got == want || strings.HasPrefix(got, want+"/")
}
