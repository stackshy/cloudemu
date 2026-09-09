package cloudids

import (
	"encoding/json"
	"hash/fnv"
	"strconv"
)

// Endpoint output-only values. Real Cloud IDS provisions an endpoint through
// CREATING before reaching READY; CloudEmu has no data plane, so it reports a
// stable READY, which is what the Terraform google provider reconciles an
// endpoint against (state is a computed attribute).
const (
	readyState = "READY"

	// forwardingRulePrefix / forwardingRuleSuffix bracket the deterministic
	// endpointForwardingRule. The real API returns a fully-qualified compute
	// forwarding-rule URL; CloudEmu mints a deterministic one from the endpoint
	// identity so it is byte-identical on every read (a computed output-only
	// attribute Terraform stores — the classic Cloud IDS drift point).
	forwardingRuleHost   = "https://www.googleapis.com/compute/v1/"
	forwardingRuleSuffix = "-fwd"

	// endpointIPOctetSpan/Base bound a hashed octet to 1..254 so a synthesized
	// private address never uses the .0 or .255 boundary. octetMask isolates the
	// low byte of a hash shift.
	endpointIPOctetSpan = 254
	endpointIPOctetBase = 1
	octetMask           = 0xFF
	octetShiftHi        = 16
	octetShiftMid       = 8
)

// seedEndpoint injects the output-only fields an endpoint carries so a GET
// reports them stably across refreshes — the classic Cloud IDS drift point. The
// incoming body has already had these keys stripped (see outputKeys), so they are
// always minted here: state is READY (so Terraform reconciles clean),
// endpointForwardingRule is a deterministic compute forwarding-rule URL, and
// endpointIp is a deterministic private address derived from the endpoint
// identity. Being pure functions of project/location/id, both are identical on
// every read, so no plan ever diffs on them.
func seedEndpoint(fields map[string]json.RawMessage, project, location, id string) {
	fields["state"] = json.RawMessage(`"` + readyState + `"`)
	fields["endpointForwardingRule"] = quote(forwardingRule(project, location, id))
	fields["endpointIp"] = quote(endpointIP(project, location, id))
}

// forwardingRule builds the deterministic fully-qualified forwarding-rule URL for
// an endpoint, in the shape the real API returns
// (…/projects/{p}/regions/{region}/forwardingRules/ids-{id}-fwd).
func forwardingRule(project, location, id string) string {
	return forwardingRuleHost + "projects/" + project + "/regions/" + location +
		"/forwardingRules/ids-" + id + forwardingRuleSuffix
}

// endpointIP derives a deterministic private IPv4 address (10.x.y.z) from the
// endpoint identity. The three low octets come from an FNV-1a hash of the full
// resource name, each clamped to 1..254, so the same endpoint always resolves to
// the same address and distinct endpoints almost never collide.
func endpointIP(project, location, id string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(resourceName(project, location, id)))
	sum := h.Sum32()

	a := endpointIPOctetBase + int((sum>>octetShiftHi)&octetMask)%endpointIPOctetSpan
	b := endpointIPOctetBase + int((sum>>octetShiftMid)&octetMask)%endpointIPOctetSpan
	c := endpointIPOctetBase + int(sum&octetMask)%endpointIPOctetSpan

	return "10." + strconv.Itoa(a) + "." + strconv.Itoa(b) + "." + strconv.Itoa(c)
}

// quote renders s as a JSON string literal.
func quote(s string) json.RawMessage {
	b, _ := json.Marshal(s)

	return b
}
