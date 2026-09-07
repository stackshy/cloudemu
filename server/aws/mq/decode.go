package mq

import (
	"encoding/json"

	"github.com/stackshy/cloudemu/v2/services/mq/driver"
)

// userWire is the restJson1 shape of a broker user in a request body.
type userWire struct {
	Username        string   `json:"username"`
	Password        string   `json:"password"`
	ConsoleAccess   bool     `json:"consoleAccess"`
	Groups          []string `json:"groups"`
	ReplicationUser bool     `json:"replicationUser"`
}

// configRefWire is the restJson1 shape of a broker's configuration reference.
type configRefWire struct {
	ID       string `json:"id"`
	Revision int32  `json:"revision"`
}

// rawString decodes a JSON string field from a raw request body, returning ""
// when the field is absent or not a string.
func rawString(raw map[string]json.RawMessage, key string) string {
	v, ok := raw[key]
	if !ok {
		return ""
	}

	var s string
	if json.Unmarshal(v, &s) != nil {
		return ""
	}

	return s
}

// usersFromBody decodes the modeled users list from a raw broker request body.
func usersFromBody(raw map[string]json.RawMessage) []driver.User {
	v, ok := raw["users"]
	if !ok {
		return nil
	}

	var wire []userWire
	if json.Unmarshal(v, &wire) != nil {
		return nil
	}

	out := make([]driver.User, 0, len(wire))
	for i := range wire {
		out = append(out, driver.User{
			Username:        wire[i].Username,
			Password:        wire[i].Password,
			ConsoleAccess:   wire[i].ConsoleAccess,
			Groups:          wire[i].Groups,
			ReplicationUser: wire[i].ReplicationUser,
		})
	}

	return out
}

// userFromBody decodes a single broker user from a raw request body, with the
// username supplied out-of-band from the URI (create) or overriding it (update).
func userFromBody(raw map[string]json.RawMessage, username string) *driver.User {
	return &driver.User{
		Username:        username,
		Password:        rawString(raw, "password"),
		ConsoleAccess:   rawBool(raw, "consoleAccess"),
		Groups:          rawStringSlice(raw, "groups"),
		ReplicationUser: rawBool(raw, "replicationUser"),
	}
}

// configRefFromBody decodes the optional configuration reference from a raw
// broker request body.
func configRefFromBody(raw map[string]json.RawMessage) *driver.ConfigRef {
	v, ok := raw["configuration"]
	if !ok {
		return nil
	}

	var wire configRefWire
	if json.Unmarshal(v, &wire) != nil || wire.ID == "" {
		return nil
	}

	return &driver.ConfigRef{ID: wire.ID, Revision: wire.Revision}
}

func rawBool(raw map[string]json.RawMessage, key string) bool {
	v, ok := raw[key]
	if !ok {
		return false
	}

	var b bool
	if json.Unmarshal(v, &b) != nil {
		return false
	}

	return b
}

func rawStringSlice(raw map[string]json.RawMessage, key string) []string {
	v, ok := raw[key]
	if !ok {
		return nil
	}

	var s []string
	if json.Unmarshal(v, &s) != nil {
		return nil
	}

	return s
}

// maxQueryInt bounds a parsed query integer so the result fits an int32 without
// overflow; MQ's maxResults tops out well below this.
const maxQueryInt = 1 << 20

// atoiDefault parses a non-negative query int32, returning 0 when empty or
// invalid. It is bounded by maxQueryInt so the conversion cannot overflow.
func atoiDefault(s string) int32 {
	n := 0

	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}

		n = n*10 + int(c-'0')
		if n > maxQueryInt {
			return maxQueryInt
		}
	}

	return int32(n)
}
