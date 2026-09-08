package datafusion

import (
	"encoding/json"
	"strings"

	dfdriver "github.com/stackshy/cloudemu/v2/services/datafusion/driver"
)

// cloneResource returns a deep copy of r so a stored resource is never aliased
// by a value handed back to a caller (which the wire layer would otherwise be
// free to mutate). The Fields raw map's bytes are copied.
func cloneResource(r *dfdriver.Resource) dfdriver.Resource {
	out := *r
	out.Fields = cloneRawMap(r.Fields)

	return out
}

// cloneRawMap deep-copies an opaque body passthrough map, copying each
// json.RawMessage's bytes so a mutation of one never aliases the stored copy.
func cloneRawMap(in map[string]json.RawMessage) map[string]json.RawMessage {
	if len(in) == 0 {
		return nil
	}

	out := make(map[string]json.RawMessage, len(in))
	for k, v := range in {
		out[k] = append(json.RawMessage(nil), v...)
	}

	return out
}

// applyMask folds the masked top-level body fields from desired into the stored
// resource. A mask path's first segment names the top-level field to replace; a
// masked field absent from desired is deleted. An empty mask replaces every
// field present in desired (lenient full-body update).
func applyMask(r *dfdriver.Resource, desired map[string]json.RawMessage, mask []string) {
	if r.Fields == nil {
		r.Fields = map[string]json.RawMessage{}
	}

	if len(mask) == 0 {
		for k, v := range desired {
			r.Fields[k] = append(json.RawMessage(nil), v...)
		}

		return
	}

	for _, path := range mask {
		field := path
		if i := strings.IndexByte(path, '.'); i >= 0 {
			field = path[:i]
		}

		if v, ok := desired[field]; ok {
			r.Fields[field] = append(json.RawMessage(nil), v...)
			continue
		}

		delete(r.Fields, field)
	}
}
