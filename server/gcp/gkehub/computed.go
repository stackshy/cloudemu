package gkehub

import "encoding/json"

// The computed output blocks below are minted from stored, stable inputs (the
// resource's minted-once UID and static terminal state) and are byte-identical
// across every read, so a Terraform refresh of the output-only state/uniqueId/
// uid attributes never drifts. A caller cannot pin them — they are stripped from
// the request body (see outputKeys) — so these always overwrite.

// seedMembership injects a membership's output-only uniqueId and READY state.
// Real registration is slow, but CloudEmu settles synchronously, so the
// membership lands READY immediately (a CREATING/UPDATING that never advanced
// would make Terraform wait or drift).
func seedMembership(m map[string]json.RawMessage, uid string) {
	m["uniqueId"] = jsonString(uid)
	m["state"] = json.RawMessage(`{"code":"READY"}`)
}

// seedFeature injects a feature's output-only resourceState (ACTIVE) and the
// aggregate CommonFeatureState (an OK FeatureState). membershipStates is left
// empty — there is no data plane registering per-membership state.
func seedFeature(m map[string]json.RawMessage, _ string) {
	m["resourceState"] = json.RawMessage(`{"state":"ACTIVE"}`)
	m["state"] = json.RawMessage(`{"state":{"code":"OK"}}`)
}

// seedFleet injects a fleet's output-only uid and READY lifecycle state.
func seedFleet(m map[string]json.RawMessage, uid string) {
	m["uid"] = jsonString(uid)
	m["state"] = json.RawMessage(`{"code":"READY"}`)
}

// jsonString renders s as a JSON string literal.
func jsonString(s string) json.RawMessage {
	b, err := json.Marshal(s)
	if err != nil {
		return json.RawMessage(`""`)
	}

	return b
}
