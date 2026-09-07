package datafusion

import (
	"crypto/sha256"
	"encoding/hex"

	dfdriver "github.com/stackshy/cloudemu/v2/services/datafusion/driver"
)

// defaultVersion is the deterministic Data Fusion version injected when a caller
// supplies none. It is byte-stable, so a Terraform computed `version` attribute
// reads it back without drifting.
const defaultVersion = "6.10.1"

const (
	// bucketHashLen / tenantHashLen / agentHashLen are the hex-digit widths of
	// the deterministic tokens embedded in the synthetic output fields.
	bucketHashLen = 20
	tenantHashLen = 20
	agentHashLen  = 12
)

// computedOutputs returns the output-only fields CloudEmu derives for an
// instance. Every value is a pure function of the instance's immutable identity
// (project, location, id) or its stored lifecycle state/timestamps, so it is
// byte-stable across GETs — the Terraform drift point. No clock or randomness is
// consulted on read.
func computedOutputs(r *dfdriver.Resource) map[string]any {
	endpoint := serviceEndpoint(r.Project, r.Location, r.ID)

	return map[string]any{
		"name":             instanceResourceName(r.Project, r.Location, r.ID),
		"state":            r.State,
		"createTime":       formatTime(r.CreateTime),
		"updateTime":       formatTime(r.UpdateTime),
		"serviceEndpoint":  endpoint,
		"apiEndpoint":      endpoint + "/api/v3",
		"gcsBucket":        gcsBucket(r.Project, r.Location, r.ID),
		"tenantProjectId":  tenantProjectID(r.Project, r.ID),
		"p4ServiceAccount": p4ServiceAccount(r.Project),
		"serviceAccount":   serviceAccount(r.Project, r.ID),
	}
}

// instanceResourceName builds the full resource name of an instance.
func instanceResourceName(project, location, id string) string {
	return "projects/" + project + "/locations/" + location + "/" + instancesSeg + "/" + id
}

// serviceEndpoint derives the deterministic UI/REST endpoint of an instance, in
// the real datafusion.googleusercontent.com shape.
func serviceEndpoint(project, location, id string) string {
	return "https://" + id + "-" + project + "-dot-" + location + ".datafusion.googleusercontent.com"
}

// gcsBucket derives the deterministic Cloud Storage staging bucket of an
// instance, in the real gs://df-<hash>-<location> shape.
func gcsBucket(project, location, id string) string {
	return "gs://df-" + shortHash(project+"/"+location+"/"+id, bucketHashLen) + "-" + location
}

// tenantProjectID derives the deterministic tenant project of an instance.
func tenantProjectID(project, id string) string {
	return "df-" + shortHash(project+"/"+id, tenantHashLen) + "-tp"
}

// p4ServiceAccount derives the deterministic Data Fusion P4 service agent of a
// project.
func p4ServiceAccount(project string) string {
	return "service-" + shortHash(project, agentHashLen) + "@gcp-sa-datafusion.iam.gserviceaccount.com"
}

// serviceAccount derives the deterministic (deprecated) management service
// account of an instance.
func serviceAccount(project, id string) string {
	return "cloud-datafusion-management-sa@" + tenantProjectID(project, id) + ".iam.gserviceaccount.com"
}

// shortHash returns the first n hex characters of the SHA-256 of s, giving a
// deterministic opaque token for the synthetic output fields.
func shortHash(s string, n int) string {
	sum := sha256.Sum256([]byte(s))
	h := hex.EncodeToString(sum[:])

	if n > len(h) {
		n = len(h)
	}

	return h[:n]
}
