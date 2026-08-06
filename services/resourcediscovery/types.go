// Package resourcediscovery is a cross-service inventory engine. It reads
// from existing service drivers (compute, networking, storage, database,
// serverless) and returns a normalized view of every resource a provider
// holds, with tags resolved per service.
//
// The engine follows the topology package as a precedent: it owns no state,
// constructs from driver interfaces, and is query-driven. It is the
// foundation for the SDK-compat handlers in the AWS Resource Explorer +
// Resource Groups Tagging API, Azure Resource Graph, and GCP Cloud Asset
// Inventory packages.
package resourcediscovery

import "time"

// Resource is the normalized cross-cloud resource shape. Every walker emits
// resources in this form so callers can filter, search, and tag-query
// uniformly regardless of provider or service.
//
// The attribute slots below (SKU, Kind, ManagedBy, Zones, Properties) are a
// uniform, resource-agnostic way to carry the type-specific shape a real cloud
// API returns (a VM size, a disk tier + size, a DB compute SKU, …). Every
// walker fills the same slots from its driver's fields, and every row-builder
// renders them the same way, so no layer branches on a specific resource or
// provider type. All slots are optional — an empty slot is omitted downstream.
type Resource struct {
	Provider  string
	Service   string
	Type      string
	ID        string
	ARN       string
	Region    string
	Tags      map[string]string
	CreatedAt time.Time

	// SKU is the size/tier identifier (VM size, disk tier, DB compute SKU).
	SKU string
	// SKUTier is the optional SKU tier (e.g. Premium, Standard, Burstable) that
	// real cloud APIs carry alongside the SKU name under the `sku` object.
	SKUTier string
	// SKUCapacity is the optional SKU capacity (e.g. a scale set's instance
	// count) that real cloud APIs carry under `sku.capacity`. Zero is omitted.
	SKUCapacity int
	// Kind is an optional resource sub-kind.
	Kind string
	// ManagedBy is the id of an owning/parent resource (e.g. a disk's VM).
	ManagedBy string
	// Zones are the availability zones the resource occupies.
	Zones []string
	// Properties is an open bag of resource-specific attributes (e.g. disk
	// size, OS type, HA mode) keyed by the cloud-native property name.
	Properties map[string]any
}

// Query filters a list operation. All non-empty fields must match. Tags match
// on key presence and (if value is non-empty) equality.
//
// Services is an any-of set: a resource matches if its Service is in the
// slice. An empty/nil slice means "no service filter". This shape supports
// cases like AWS's "ec2" which spans both compute and networking — the
// caller can pass Services: []string{"compute", "networking"}.
//
// Type is a single exact-match type filter. Types is an any-of set on the
// resource Type, for callers that select several types at once (e.g. a KQL
// `where type in~ ('a', 'b')`); an empty/nil slice means "no type-set filter".
// Type and Types are independent — both must pass when both are set.
type Query struct {
	Services []string
	Type     string
	Types    []string
	Region   string
	Tags     map[string]string
}

// matches returns true if r satisfies every non-empty field of q.
func (q *Query) matches(r *Resource) bool {
	if !sliceMatch(q.Services, r.Service) {
		return false
	}

	if !fieldMatch(q.Type, r.Type) {
		return false
	}

	if !sliceMatch(q.Types, r.Type) {
		return false
	}

	if !fieldMatch(q.Region, r.Region) {
		return false
	}

	return tagsMatch(q.Tags, r.Tags)
}

// fieldMatch returns true when want is empty (no filter) or equals got.
func fieldMatch(want, got string) bool {
	return want == "" || want == got
}

// sliceMatch returns true when want is empty (no filter) or got is in want.
func sliceMatch(want []string, got string) bool {
	if len(want) == 0 {
		return true
	}

	for _, w := range want {
		if w == got {
			return true
		}
	}

	return false
}

// tagsMatch returns true when every required key is present, and any
// non-empty required value equals the actual value.
func tagsMatch(required, actual map[string]string) bool {
	for k, v := range required {
		got, ok := actual[k]
		if !ok {
			return false
		}

		if v != "" && got != v {
			return false
		}
	}

	return true
}
