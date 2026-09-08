package accesscontextmanager

// The proto package for Access Context Manager is
// google.identity.accesscontextmanager.v1 (note: identity, not cloud), so a
// completed operation's Any-typed response carries these exact @type strings.
const (
	policyTypeURL           = "type.googleapis.com/google.identity.accesscontextmanager.v1.AccessPolicy"
	accessLevelTypeURL      = "type.googleapis.com/google.identity.accesscontextmanager.v1.AccessLevel"
	servicePerimeterTypeURL = "type.googleapis.com/google.identity.accesscontextmanager.v1.ServicePerimeter"
)

// childResourceName builds the full resource name of a child (level/perimeter):
// accessPolicies/{policyNumber}/{coll}/{id}.
func childResourceName(coll, policyNumber, id string) string {
	return "accessPolicies/" + policyNumber + "/" + coll + "/" + id
}

// collForKind maps an operation kind to its nested-collection path segment.
func collForKind(kind string) string {
	if kind == "accessLevel" {
		return accessLevelsColl
	}

	return servicePerimetersColl
}
