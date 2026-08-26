package bigtable

import "strings"

// fieldMask is a parsed google.protobuf.FieldMask taken from the ?updateMask
// query param. A nil *fieldMask means the client sent no mask, so the patch
// handlers fall back to their presence-heuristic behavior for back-compat.
type fieldMask struct {
	// paths are normalized tokens: lowercased with underscores stripped, so
	// snake_case and camelCase spellings of the same path compare equal
	// ("deletion_protection" and "deletionProtection" both -> "deletionprotection").
	paths []string
}

// parseFieldMask reads a comma-separated FieldMask. It returns nil when the
// param is absent or empty so callers keep their legacy update behavior.
func parseFieldMask(raw string) *fieldMask {
	if strings.TrimSpace(raw) == "" {
		return nil
	}

	m := &fieldMask{}

	for _, p := range strings.Split(raw, ",") {
		if p = normalizeMaskPath(p); p != "" {
			m.paths = append(m.paths, p)
		}
	}

	if len(m.paths) == 0 {
		return nil
	}

	return m
}

func normalizeMaskPath(s string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), "_", ""))
}

// has reports whether the mask names field, either exactly or as the leading
// segment of a dotted sub-path (e.g. "singleClusterRouting.clusterId" is a
// change to "singleClusterRouting"). field is normalized by this method.
func (m *fieldMask) has(field string) bool {
	field = normalizeMaskPath(field)

	for _, p := range m.paths {
		if p == field || strings.HasPrefix(p, field+".") {
			return true
		}
	}

	return false
}

// contains reports whether any masked path contains sub as a substring, used
// for the dotted autoscaling/routing paths whose exact spelling varies across
// clients. sub is normalized by this method.
func (m *fieldMask) contains(sub string) bool {
	sub = normalizeMaskPath(sub)

	for _, p := range m.paths {
		if strings.Contains(p, sub) {
			return true
		}
	}

	return false
}
