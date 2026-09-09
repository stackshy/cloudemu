package metastore

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
)

// Server-defaulted top-level fields the real Dataproc Metastore API fills when
// the client omits them, plus the output-only values a freshly-created service
// reports. The Terraform google provider marks port (and the output-only
// endpointUri/state/uid/artifactGcsUri) Computed, so it sends nothing and reads
// them back from the API — if the emulator did not fill them, a refresh would
// diff forever (the classic defaulted-field drift point, same lesson as
// Serverless VPC Access). databaseType and releaseChannel are Optional, not
// Computed, in the provider, which supplies its own schema defaults (MYSQL,
// STABLE) and always sends them — the emulator fills the same values for a raw
// SDK/gcloud caller that omits them, so both paths converge.
//
// telemetryConfig and hiveMetastoreConfig are deliberately NOT seeded. The
// provider models both as Optional, non-Computed nested blocks (hive version is
// Required within its block), so it expects an omitted block to stay absent;
// injecting a server default there is the one thing that drifts a plan. They are
// therefore carried through verbatim as raw passthrough, filled only by the
// client (Terraform requires hive version whenever the block is present).
const (
	defaultPort           = 9083
	defaultDatabaseType   = "MYSQL"
	defaultReleaseChannel = "STABLE"
	defaultTier           = "DEVELOPER"

	// activeState is the state a service is minted with at create. Real Dataproc
	// Metastore provisions a service through CREATING before reaching ACTIVE;
	// CloudEmu has no data plane, so it reports a stable ACTIVE, which is what
	// Terraform reconciles a service against (state is a computed attribute).
	activeState = "ACTIVE"
)

// seedService injects the output-only fields a metastore service carries and
// fills the Computed top-level server defaults so a GET reports them stably
// across refreshes — the classic Dataproc Metastore drift point. A
// caller-supplied value is always left untouched; only an absent field is
// defaulted. The output-only identity fields (state, stateMessage, uid,
// endpointUri, artifactGcsUri) are minted here once and stored; port,
// databaseType, releaseChannel, and tier take the real API defaults. The nested
// telemetryConfig/hiveMetastoreConfig blocks are intentionally left untouched
// (see the const block) so an omitted block never drifts a Terraform plan.
func seedService(fields map[string]json.RawMessage, name string) {
	seedString(fields, "databaseType", defaultDatabaseType)
	seedString(fields, "releaseChannel", defaultReleaseChannel)
	seedString(fields, "tier", defaultTier)
	seedString(fields, "state", activeState)
	seedString(fields, "stateMessage", "")
	seedInt(fields, "port", defaultPort)

	seedString(fields, "uid", deriveUID(name))
	seedString(fields, "artifactGcsUri", deriveArtifactGcsURI(name))
	seedString(fields, "endpointUri", deriveEndpointURI(name, effectivePort(fields)))
}

// seedString fills fields[key] with a default string only when the key is
// absent, so a caller-supplied value is preserved.
func seedString(fields map[string]json.RawMessage, key, def string) {
	if _, has := fields[key]; has {
		return
	}

	fields[key] = json.RawMessage(`"` + def + `"`)
}

// seedInt fills fields[key] with an integer default only when the key is absent.
func seedInt(fields map[string]json.RawMessage, key string, def int) {
	if _, has := fields[key]; has {
		return
	}

	if raw, err := json.Marshal(def); err == nil {
		fields[key] = raw
	}
}

// effectivePort returns the service's TCP port, reading a caller-supplied value
// or falling back to the default, so the derived endpointUri reflects it.
func effectivePort(fields map[string]json.RawMessage) int {
	if raw, ok := fields["port"]; ok {
		var p int
		if json.Unmarshal(raw, &p) == nil && p > 0 {
			return p
		}
	}

	return defaultPort
}

// deriveUID mints a stable, globally-unique-looking identifier from the service
// name, formatted as a UUID so it matches the real API's uid shape. Deterministic
// so it never changes across reads.
func deriveUID(name string) string {
	h := hex.EncodeToString(sha256sum(name))

	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// deriveArtifactGcsURI mints the stable gs:// artifact bucket URI a service
// reports, deterministically from the name. The real API returns a
// gs://<bucket>/hive-metastore path; CloudEmu mirrors that shape stably.
func deriveArtifactGcsURI(name string) string {
	h := hex.EncodeToString(sha256sum(name))

	return "gs://gcs-metastore-" + h[0:20] + "/hive-metastore"
}

// deriveEndpointURI mints the stable Thrift endpoint URI a service reports,
// deterministically from the name and its effective port. The real API returns a
// thrift://<host>:<port> endpoint; CloudEmu mirrors that shape stably.
func deriveEndpointURI(name string, port int) string {
	h := hex.EncodeToString(sha256sum(name))

	return "thrift://" + h[0:20] + ".metastore.cloudemu.internal:" + strconv.Itoa(port)
}

// sha256sum returns the SHA-256 digest of s, the deterministic basis for every
// derived computed field.
func sha256sum(s string) []byte {
	sum := sha256.Sum256([]byte(s))

	return sum[:]
}
