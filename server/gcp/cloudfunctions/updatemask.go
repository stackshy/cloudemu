package cloudfunctions

import (
	"net/url"
	"strings"
)

// updateMask is the parsed FieldMask from a PATCH ?updateMask= query parameter.
// A gen1/gen2 update sends a comma-separated list of field paths (for example
// "description,serviceConfig.maxInstanceCount"); only the listed fields are
// written, so a field named in the mask but absent from the body is CLEARED.
//
// When no updateMask is sent the mask is "all": the handler falls back to the
// legacy merge-non-zero-fields behavior so an unmasked PATCH never blanks a
// field the client didn't mention.
type updateMask struct {
	paths map[string]struct{}
	all   bool
}

// parseUpdateMask reads the "updateMask" query parameter into an updateMask.
func parseUpdateMask(q url.Values) updateMask {
	raw := strings.TrimSpace(q.Get("updateMask"))
	if raw == "" {
		return updateMask{all: true}
	}

	paths := make(map[string]struct{})

	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimSpace(p); p != "" {
			paths[p] = struct{}{}
		}
	}

	if len(paths) == 0 {
		return updateMask{all: true}
	}

	return updateMask{paths: paths}
}

// covers reports whether field should be written under this mask: either no mask
// was sent (legacy all), the mask names the field exactly, or the mask names an
// ancestor path (mask "serviceConfig" covers "serviceConfig.maxInstanceCount").
func (m updateMask) covers(field string) bool {
	if m.all {
		return true
	}

	if _, ok := m.paths[field]; ok {
		return true
	}

	for p := field; ; {
		i := strings.LastIndex(p, ".")
		if i < 0 {
			return false
		}

		p = p[:i]
		if _, ok := m.paths[p]; ok {
			return true
		}
	}
}

// explicit reports whether a mask was actually sent. When true, a covered field
// missing from the body is cleared; when false (legacy), only non-zero values
// are applied.
func (m updateMask) explicit() bool {
	return !m.all
}

// applyMaskedStr writes val into cur when the mask covers field. With an explicit
// mask an empty val clears cur; without one only a non-empty val is applied.
func applyMaskedStr(m updateMask, field string, cur *string, val string) {
	if !m.covers(field) {
		return
	}

	if m.explicit() || val != "" {
		*cur = val
	}
}

// applyMaskedInt is applyMaskedStr for an int field, treating zero as empty.
func applyMaskedInt(m updateMask, field string, cur *int, val int) {
	if !m.covers(field) {
		return
	}

	if m.explicit() || val != 0 {
		*cur = val
	}
}
