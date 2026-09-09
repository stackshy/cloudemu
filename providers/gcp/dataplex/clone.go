package dataplex

import (
	"encoding/json"

	dpdriver "github.com/stackshy/cloudemu/v2/services/dataplex/driver"
)

// cloneResource returns a deep copy of r so a stored resource is never aliased by
// a value handed back to a caller (which the wire layer would otherwise be free
// to mutate). The Fields raw map's bytes are copied.
func cloneResource(r *dpdriver.Resource) dpdriver.Resource {
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
