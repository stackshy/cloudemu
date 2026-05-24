// Package cloudasset serves the GCP Cloud Asset Inventory v1 REST API
// against a *resourcediscovery.Engine.
//
// Filter syntax (documented subset of the real Cloud Asset filter
// expression):
//
//	service:storage.googleapis.com      service:compute.googleapis.com
//	assetType:storage.googleapis.com/Bucket
//	location:us-east1                    location:us-central1
//	labels.env:prod                      tags.env:prod
//
// Multiple terms are whitespace-separated and AND-composed. Unknown keys
// are tolerated and ignored (matches Cloud Asset's permissive behavior
// for unknown fields). Contradictory terms (two distinct services in the
// same query, or two values for the same label) flip ForceEmpty so the
// handler short-circuits to an empty result — a single resource cannot
// belong to two services or hold two values for one label, so the real
// API would also return zero rows.
package cloudasset

import (
	"strings"

	"github.com/stackshy/cloudemu/resourcediscovery"
)

// GCP service identifiers as they appear in Cloud Asset assetType strings.
const (
	gcpServiceCompute        = "compute.googleapis.com"
	gcpServiceStorage        = "storage.googleapis.com"
	gcpServiceFirestore      = "firestore.googleapis.com"
	gcpServiceCloudFunctions = "cloudfunctions.googleapis.com"
)

// Fully-qualified GCP asset type strings — used in both directions
// (parsing assetType filters AND emitting type in response rows).
const (
	atComputeInstance = "compute.googleapis.com/Instance"
	atNetwork         = "compute.googleapis.com/Network"
	atSubnetwork      = "compute.googleapis.com/Subnetwork"
	atFirewall        = "compute.googleapis.com/Firewall"
	atStorageBucket   = "storage.googleapis.com/Bucket"
	atFirestoreDB     = "firestore.googleapis.com/Database"
	atFirestoreColl   = "firestore.googleapis.com/Collection"
	atCloudFunction   = "cloudfunctions.googleapis.com/Function"
	atCloudFunctionV1 = "cloudfunctions.googleapis.com/CloudFunction"
)

// Portable service identifiers as emitted by resourcediscovery walkers.
const (
	portableCompute    = "compute"
	portableNetworking = "networking"
	portableStorage    = "storage"
	portableDatabase   = "database"
	portableServerless = "serverless"
)

// parsedFilter is the result of filter parsing — an engine Query plus
// any caller-detected contradictions.
type parsedFilter struct {
	Query      resourcediscovery.Query
	ForceEmpty bool

	serviceSet bool
	typeSet    bool
	labelFirst map[string]string
}

// parseFilter converts a Cloud Asset filter expression into a Query.
// Empty filter returns a zero Query (matches all). Unknown tokens are
// silently ignored.
func parseFilter(expr string) parsedFilter {
	out := parsedFilter{}
	if strings.TrimSpace(expr) == "" {
		return out
	}

	for _, token := range strings.Fields(expr) {
		applyToken(&out, token)
	}

	return out
}

func applyToken(out *parsedFilter, token string) {
	key, val, ok := strings.Cut(token, ":")
	if !ok || val == "" {
		return
	}

	switch {
	case key == "service":
		applyService(out, val)
	case key == "assetType":
		applyAssetType(out, val)
	case key == "location":
		out.Query.Region = val
	case strings.HasPrefix(key, "labels."):
		addLabel(out, strings.TrimPrefix(key, "labels."), val)
	case strings.HasPrefix(key, "tags."):
		addLabel(out, strings.TrimPrefix(key, "tags."), val)
	}
}

func applyService(out *parsedFilter, service string) {
	if out.serviceSet {
		out.ForceEmpty = true
		return
	}

	out.serviceSet = true

	svc := gcpServiceToPortable(service)
	if svc == "" {
		return
	}

	newServices := expandPortableService(svc)

	// Cross-clause contradiction: if an earlier assetType: pinned a
	// narrower service set, a service: clause that doesn't overlap means
	// "this resource must belong to two different services" — impossible,
	// so flag ForceEmpty.
	if out.typeSet && len(out.Query.Services) > 0 && !servicesIntersect(out.Query.Services, newServices) {
		out.ForceEmpty = true
		return
	}

	out.Query.Services = newServices
}

func applyAssetType(out *parsedFilter, assetType string) {
	if out.typeSet {
		out.ForceEmpty = true
		return
	}

	out.typeSet = true

	svc, typ := mapGCPAssetType(assetType)
	if svc != "" {
		newServices := []string{svc}

		// Cross-clause contradiction: same idea as applyService — if a
		// service: clause already narrowed Services and the new assetType
		// belongs to a different service, no resource can satisfy both.
		if out.serviceSet && len(out.Query.Services) > 0 && !servicesIntersect(out.Query.Services, newServices) {
			out.ForceEmpty = true
			return
		}

		out.Query.Services = newServices
	}

	if typ != "" {
		out.Query.Type = typ
	}
}

// servicesIntersect returns true when the two service-name sets share at
// least one element. Used by the cross-clause contradiction checks above.
func servicesIntersect(a, b []string) bool {
	set := make(map[string]struct{}, len(a))
	for _, s := range a {
		set[s] = struct{}{}
	}

	for _, s := range b {
		if _, ok := set[s]; ok {
			return true
		}
	}

	return false
}

func addLabel(out *parsedFilter, key, value string) {
	if prev, seen := out.labelFirst[key]; seen && prev != value {
		out.ForceEmpty = true
		return
	}

	if out.labelFirst == nil {
		out.labelFirst = make(map[string]string)
	}

	out.labelFirst[key] = value

	if out.Query.Tags == nil {
		out.Query.Tags = make(map[string]string)
	}

	out.Query.Tags[key] = value
}

// gcpServiceToPortable maps a GCP service name (storage.googleapis.com)
// to the portable service identifier. compute.googleapis.com is special:
// it spans both portable "compute" and "networking" — the caller uses
// expandPortableService to get the right Services set.
func gcpServiceToPortable(service string) string {
	switch service {
	case gcpServiceCompute:
		// caller should expand
		return portableCompute
	case gcpServiceStorage:
		return portableStorage
	case gcpServiceFirestore:
		return portableDatabase
	case gcpServiceCloudFunctions:
		return portableServerless
	default:
		return ""
	}
}

// expandPortableService turns a portable service into the slice the engine
// matcher expects. compute.googleapis.com covers both VMs and networking
// resources in real GCP, so a service:compute.googleapis.com filter must
// match both portable services.
func expandPortableService(svc string) []string {
	if svc == portableCompute {
		return []string{portableCompute, portableNetworking}
	}

	return []string{svc}
}

// mapGCPAssetType translates a fully-qualified GCP asset type
// (compute.googleapis.com/Instance) to the portable (service, type) pair
// the engine uses. Returns ("", "") for unmapped types.
func mapGCPAssetType(assetType string) (service, typ string) {
	switch assetType {
	case atComputeInstance:
		return portableCompute, "Instance"
	case atNetwork:
		return portableNetworking, "VPC"
	case atSubnetwork:
		return portableNetworking, "Subnet"
	case atFirewall:
		return portableNetworking, "SecurityGroup"
	case atStorageBucket:
		return portableStorage, "Bucket"
	case atFirestoreDB, atFirestoreColl:
		return portableDatabase, "Table"
	case atCloudFunction, atCloudFunctionV1:
		return portableServerless, "Function"
	default:
		return "", ""
	}
}

// portableToGCPAssetType is the inverse — turns the engine's (service,
// type) pair into the canonical GCP assetType string the API emits.
func portableToGCPAssetType(service, typ string) string {
	switch service + "/" + typ {
	case portableCompute + "/Instance":
		return atComputeInstance
	case portableNetworking + "/VPC":
		return atNetwork
	case portableNetworking + "/Subnet":
		return atSubnetwork
	case portableNetworking + "/SecurityGroup":
		return atFirewall
	case portableStorage + "/Bucket":
		return atStorageBucket
	case portableDatabase + "/Table":
		return atFirestoreDB
	case portableServerless + "/Function":
		return atCloudFunction
	default:
		return service + "/" + typ
	}
}
