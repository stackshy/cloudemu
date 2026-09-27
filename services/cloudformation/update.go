package cloudformation

import (
	"bytes"
	"encoding/json"
	"sort"
)

// UpdateAction is what an update does to one existing resource.
type UpdateAction int

// Update actions, decided per resource from its changed properties.
const (
	// UpdateNone leaves the resource as it is.
	UpdateNone UpdateAction = iota
	// UpdateInPlace changes the existing resource and keeps its physical id.
	UpdateInPlace
	// UpdateReplace creates a new physical resource and deletes the old one.
	UpdateReplace
)

// PlanResourceUpdate compares the resolved properties a resource was last
// applied with to the resolved properties the new template gives it. A change
// replaces the resource when a changed property requires replacement, or when
// the provisioner has no ReplacementSchema. Otherwise it is in place.
func PlanResourceUpdate(p Provisioner, previous, next map[string]any) UpdateAction {
	changed := ChangedProperties(previous, next)
	if len(changed) == 0 {
		return UpdateNone
	}

	schema, ok := p.(ReplacementSchema)
	if !ok {
		return UpdateReplace
	}

	for _, name := range changed {
		if schema.RequiresReplacement(name) {
			return UpdateReplace
		}
	}

	return UpdateInPlace
}

// ChangedProperties returns the sorted top-level property names whose values
// differ. Values compare by their JSON form, so a number read from a template
// equals the same number read back from a snapshot.
func ChangedProperties(previous, next map[string]any) []string {
	names := map[string]bool{}

	for k, v := range next {
		if old, ok := previous[k]; !ok || !sameValue(old, v) {
			names[k] = true
		}
	}

	for k := range previous {
		if _, ok := next[k]; !ok {
			names[k] = true
		}
	}

	out := make([]string, 0, len(names))
	for k := range names {
		out = append(out, k)
	}

	sort.Strings(out)

	return out
}

// SameProperties reports whether two resolved property sets are equal.
func SameProperties(a, b map[string]any) bool {
	return len(ChangedProperties(a, b)) == 0
}

func sameValue(a, b any) bool {
	ja, errA := json.Marshal(a)
	jb, errB := json.Marshal(b)

	if errA != nil || errB != nil {
		return false
	}

	return bytes.Equal(ja, jb)
}
