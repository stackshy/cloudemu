package vpcaccess

import "encoding/json"

// Connector output-only / API-defaulted values. Real Serverless VPC Access
// provisions a connector through CREATING before reaching READY; CloudEmu has no
// data plane, so it reports a stable READY, which is what the Terraform google
// provider reconciles a connector against (state is a computed attribute).
const (
	readyState = "READY"

	// Defaults the real API fills when the client omits these fields. The
	// Terraform google provider marks min/max instances and throughput
	// Optional+Computed with no schema default, so it sends nothing and reads
	// them back from the API — if the emulator did not fill them, a plan would
	// diff forever. Throughput mirrors 100 Mbps per instance (2→200, 3→300).
	defaultMinInstances  = 2
	defaultMaxInstances  = 3
	defaultMinThroughput = 200
	defaultMaxThroughput = 300

	// defaultMachineType is the Connector's documented machine-type default. The
	// Terraform provider carries the same default and always sends it, so this is
	// a belt-and-braces fill for a raw SDK caller that omits it.
	defaultMachineType = "e2-micro"
)

// seedConnector injects the output-only and API-defaulted fields a connector
// carries so a GET reports them stably across refreshes — the classic
// Serverless VPC Access drift point. A caller-supplied value is always left
// untouched (Terraform may set any of the numeric fields explicitly); only an
// absent field is defaulted. state is minted READY so Terraform reconciles a
// connector clean; connectedProjects is minted empty; the four numeric fields
// and machineType take the real API defaults.
func seedConnector(fields map[string]json.RawMessage) {
	if _, has := fields["state"]; !has {
		fields["state"] = json.RawMessage(`"` + readyState + `"`)
	}

	if _, has := fields["connectedProjects"]; !has {
		fields["connectedProjects"] = json.RawMessage(`[]`)
	}

	if _, has := fields["machineType"]; !has {
		fields["machineType"] = json.RawMessage(`"` + defaultMachineType + `"`)
	}

	seedInt(fields, "minInstances", defaultMinInstances)
	seedInt(fields, "maxInstances", defaultMaxInstances)
	seedInt(fields, "minThroughput", defaultMinThroughput)
	seedInt(fields, "maxThroughput", defaultMaxThroughput)
}

// seedInt fills fields[key] with the integer default only when the key is
// absent, so a caller-supplied value (including an explicit zero, were it ever
// valid) is preserved.
func seedInt(fields map[string]json.RawMessage, key string, def int) {
	if _, has := fields[key]; has {
		return
	}

	if raw, err := json.Marshal(def); err == nil {
		fields[key] = raw
	}
}
