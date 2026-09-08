package accesscontextmanager

import (
	"encoding/json"

	acmdriver "github.com/stackshy/cloudemu/v2/services/accesscontextmanager/driver"
)

// clonePolicy returns a deep copy of p so a stored policy is never aliased by a
// value handed back to a caller. The Fields raw map's bytes are copied.
func clonePolicy(p *acmdriver.Policy) acmdriver.Policy {
	out := *p
	out.Fields = cloneRawMap(p.Fields)

	return out
}

// cloneChild returns a deep copy of c (a level or perimeter), copying the Fields
// raw map so a mutation of one never aliases the stored copy.
func cloneChild(c *acmdriver.Child) acmdriver.Child {
	out := *c
	out.Fields = cloneRawMap(c.Fields)

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
