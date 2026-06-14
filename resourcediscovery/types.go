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
type Resource struct {
	Provider  string
	Service   string
	Type      string
	ID        string
	ARN       string
	Region    string
	Tags      map[string]string
	CreatedAt time.Time
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
