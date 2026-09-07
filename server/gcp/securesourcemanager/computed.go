package securesourcemanager

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
)

// activeState is the state an instance is minted with at create. Real Secure
// Source Manager provisions an instance through CREATING before reaching ACTIVE;
// CloudEmu has no provisioning data plane, so it reports a stable ACTIVE. The
// value is output-only/computed in both the API and the Terraform schema, so a
// stable value never drifts a refresh.
const activeState = "ACTIVE"

// hostSuffix is the domain the deterministic host_config / uris hostnames live
// under, mirroring the real *.sourcemanager.dev hosting domain.
const hostSuffix = ".sourcemanager.dev"

// hashLen is the number of hex characters of the resource-name digest folded
// into a deterministic hostname, keeping it stable yet resource-unique.
const hashLen = 12

// jsonNull is the JSON null literal, treated as an absent value.
const jsonNull = "null"

// errRepositoryNoInstance surfaces a repository create that omits the required
// `instance` reference as a 400, matching the real API.
var errRepositoryNoInstance = errors.New("repository must set the instance it is hosted in")

// validateRepository enforces the repository's required `instance` reference:
// the full resource name of the instance hosting it must be present, matching
// the real API (a 400 otherwise). The referenced instance is NOT checked for
// existence — Terraform may pass a project-number-normalized name that would not
// match a project-id-keyed store, and a false 404 would break an apply that
// creates the instance and repository in the same plan (see BUILDOUT_BACKLOG.md).
func validateRepository(fields map[string]json.RawMessage) error {
	raw, ok := fields["instance"]
	if !ok || len(raw) == 0 || string(raw) == jsonNull {
		return errRepositoryNoInstance
	}

	var instance string
	if json.Unmarshal(raw, &instance) != nil || instance == "" {
		return errRepositoryNoInstance
	}

	return nil
}

// seedInstance mints the instance's output-only computed blocks once at create,
// deterministically from its identity, so a GET reports them byte-identically
// across refreshes. This is the classic Secure Source Manager drift point: the
// state and every host_config URL must be identical on the create response and
// every later read. A caller-supplied value is left untouched.
func seedInstance(fields map[string]json.RawMessage, project, location, id string) {
	if !has(fields, "state") {
		fields["state"] = json.RawMessage(`"` + activeState + `"`)
	}

	if !has(fields, "hostConfig") {
		base := hostBase(resourceName(instancesColl, project, location, id), id)
		host := map[string]string{
			"html":    "https://" + base + host(location),
			"api":     "https://" + base + "-api" + host(location),
			"gitHttp": "https://" + base + "-git" + host(location),
			"gitSsh":  base + "-ssh" + host(location),
		}
		putComputed(fields, "hostConfig", host)
	}
}

// seedRepository mints the repository's output-only computed blocks once at
// create: a system-generated uid and the uris block, both stable across reads.
// The uid is a fresh UUID (stored, so stable); the uris are derived
// deterministically from the repository identity. A caller-supplied value is
// left untouched.
func seedRepository(fields map[string]json.RawMessage, project, location, id string) {
	if !has(fields, "uid") {
		fields["uid"] = json.RawMessage(`"` + idgen.UUID() + `"`)
	}

	if !has(fields, "uris") {
		base := hostBase(resourceName(repositoriesColl, project, location, id), id)
		h := "https://" + base + host(location)
		uris := map[string]string{
			"html":     h + "/" + id,
			"gitHttps": h + "/" + id + ".git",
			"api":      h + "/api/v1/" + id,
		}
		putComputed(fields, "uris", uris)
	}
}

// hostBase folds a resource name's digest into a stable, resource-unique
// hostname prefix of the form "{id}-{hash}".
func hostBase(name, id string) string {
	sum := sha256.Sum256([]byte(name))

	return id + "-" + hex.EncodeToString(sum[:])[:hashLen]
}

// host returns the location-scoped hosting-domain suffix ".{location}.sourcemanager.dev".
func host(location string) string {
	return "." + location + hostSuffix
}

// has reports whether key is present in fields with a non-null value.
func has(fields map[string]json.RawMessage, key string) bool {
	v, ok := fields[key]

	return ok && len(v) > 0 && string(v) != jsonNull
}

// putComputed marshals obj into fields under key, leaving fields unchanged on a
// marshal error.
func putComputed(fields map[string]json.RawMessage, key string, obj any) {
	if raw, err := json.Marshal(obj); err == nil {
		fields[key] = raw
	}
}
