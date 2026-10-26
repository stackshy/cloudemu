package codeartifact

import (
	"net/http"
)

// Operation names used by more than one table.
const (
	opGetAuthorizationToken = "GetAuthorizationToken"
	opPutDomainPolicy       = "PutDomainPermissionsPolicy"
	opCopyPackageVersions   = "CopyPackageVersions"
	opUpdatePackageGroup    = "UpdatePackageGroup"
)

// routeDef is one CodeArtifact operation: its verb, path and API name.
type routeDef struct {
	method string
	path   string
	op     string
}

// routeDefs is the single source of truth for routing and for the CloudTrail
// event name of a request: all 48 CodeArtifact operations.
func routeDefs() []routeDef {
	return []routeDef{
		{http.MethodPost, "/v1/domain", "CreateDomain"},
		{http.MethodGet, "/v1/domain", "DescribeDomain"},
		{http.MethodDelete, "/v1/domain", "DeleteDomain"},
		{http.MethodPost, "/v1/domains", "ListDomains"},
		{http.MethodPost, "/v1/repository", "CreateRepository"},
		{http.MethodGet, "/v1/repository", "DescribeRepository"},
		{http.MethodPut, "/v1/repository", "UpdateRepository"},
		{http.MethodDelete, "/v1/repository", "DeleteRepository"},
		{http.MethodPost, "/v1/repositories", "ListRepositories"},
		{http.MethodPost, "/v1/domain/repositories", "ListRepositoriesInDomain"},
		{http.MethodPost, "/v1/repository/external-connection", "AssociateExternalConnection"},
		{http.MethodDelete, "/v1/repository/external-connection", "DisassociateExternalConnection"},
		{http.MethodPost, "/v1/tag", "TagResource"},
		{http.MethodPost, "/v1/untag", "UntagResource"},
		{http.MethodPost, "/v1/tags", "ListTagsForResource"},
		{http.MethodPost, "/v1/authorization-token", opGetAuthorizationToken},
		{http.MethodGet, "/v1/repository/endpoint", "GetRepositoryEndpoint"},
		{http.MethodPut, "/v1/domain/permissions/policy", opPutDomainPolicy},
		{http.MethodGet, "/v1/domain/permissions/policy", "GetDomainPermissionsPolicy"},
		{http.MethodDelete, "/v1/domain/permissions/policy", "DeleteDomainPermissionsPolicy"},
		{http.MethodPut, "/v1/repository/permissions/policy", "PutRepositoryPermissionsPolicy"},
		{http.MethodGet, "/v1/repository/permissions/policy", "GetRepositoryPermissionsPolicy"},
		{http.MethodDelete, "/v1/repository/permissions/policies", "DeleteRepositoryPermissionsPolicy"},
		{http.MethodPost, "/v1/packages", "ListPackages"},
		{http.MethodGet, "/v1/package", "DescribePackage"},
		{http.MethodPost, "/v1/package", "PutPackageOriginConfiguration"},
		{http.MethodDelete, "/v1/package", "DeletePackage"},
		{http.MethodPost, "/v1/package/versions", "ListPackageVersions"},
		{http.MethodGet, "/v1/package/version", "DescribePackageVersion"},
		{http.MethodPost, "/v1/package/version/publish", "PublishPackageVersion"},
		{http.MethodPost, "/v1/package/version/assets", "ListPackageVersionAssets"},
		{http.MethodGet, "/v1/package/version/asset", "GetPackageVersionAsset"},
		{http.MethodGet, "/v1/package/version/readme", "GetPackageVersionReadme"},
		{http.MethodPost, "/v1/package/version/dependencies", "ListPackageVersionDependencies"},
		{http.MethodPost, "/v1/package/versions/update_status", "UpdatePackageVersionsStatus"},
		{http.MethodPost, "/v1/package/versions/dispose", "DisposePackageVersions"},
		{http.MethodPost, "/v1/package/versions/delete", "DeletePackageVersions"},
		{http.MethodPost, "/v1/package/versions/copy", opCopyPackageVersions},
		{http.MethodPost, "/v1/package-group", "CreatePackageGroup"},
		{http.MethodGet, "/v1/package-group", "DescribePackageGroup"},
		{http.MethodPut, "/v1/package-group", opUpdatePackageGroup},
		{http.MethodDelete, "/v1/package-group", "DeletePackageGroup"},
		{http.MethodPut, "/v1/package-group-origin-configuration", "UpdatePackageGroupOriginConfiguration"},
		{http.MethodPost, "/v1/package-groups", "ListPackageGroups"},
		{http.MethodPost, "/v1/package-groups/sub-groups", "ListSubPackageGroups"},
		{http.MethodGet, "/v1/list-associated-packages", "ListAssociatedPackages"},
		{http.MethodGet, "/v1/get-associated-package-group", "GetAssociatedPackageGroup"},
		{http.MethodGet, "/v1/package-group-allowed-repositories", "ListAllowedRepositoriesForGroup"},
	}
}

// OperationName returns the CodeArtifact API operation name (the CloudTrail
// EventName) for a request, or "" when the method and path match no operation.
// The REST-JSON protocol has no X-Amz-Target header, so the name is derived from
// the same method + path pairs the handler routes on.
func OperationName(method, path string) string {
	for _, d := range routeDefs() {
		if d.method == method && d.path == path {
			return d.op
		}
	}

	return ""
}

// capability reports whether the driver implements the optional capability an
// operation needs; the base operations need none.
func (h *Handler) capability(op string) bool {
	switch op {
	case opGetAuthorizationToken, "GetRepositoryEndpoint":
		return h.access != nil
	case opPutDomainPolicy, "GetDomainPermissionsPolicy", "DeleteDomainPermissionsPolicy",
		"PutRepositoryPermissionsPolicy", "GetRepositoryPermissionsPolicy", "DeleteRepositoryPermissionsPolicy":
		return h.policies != nil
	case "ListPackages", "DescribePackage", "PutPackageOriginConfiguration", "DeletePackage":
		return h.packages != nil
	case "ListPackageVersions", "DescribePackageVersion", "PublishPackageVersion", "ListPackageVersionAssets",
		"GetPackageVersionAsset", "GetPackageVersionReadme", "ListPackageVersionDependencies",
		"UpdatePackageVersionsStatus", "DisposePackageVersions", "DeletePackageVersions", opCopyPackageVersions:
		return h.versions != nil
	case "CreatePackageGroup", "DescribePackageGroup", opUpdatePackageGroup, "DeletePackageGroup",
		"UpdatePackageGroupOriginConfiguration", "ListPackageGroups", "ListSubPackageGroups",
		"ListAssociatedPackages", "GetAssociatedPackageGroup", "ListAllowedRepositoriesForGroup":
		return h.groups != nil
	default:
		return true
	}
}

// buildRoutes fills the route table from routeDefs and the operation handlers.
func (h *Handler) buildRoutes() {
	handlers := h.operationHandlers()

	for _, d := range routeDefs() {
		if !h.capability(d.op) {
			continue
		}

		h.routes[routeKey{d.method, d.path}] = handlers[d.op]
		h.paths[d.path] = true
	}
}

// operationHandlers maps each operation name to its handler.
func (h *Handler) operationHandlers() map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{
		"CreateDomain": h.createDomain, "DescribeDomain": h.describeDomain, "DeleteDomain": h.deleteDomain, "ListDomains": h.listDomains,
		"CreateRepository": h.createRepository, "DescribeRepository": h.describeRepository,
		"UpdateRepository": h.updateRepository, "DeleteRepository": h.deleteRepository,
		"ListRepositories": h.listRepositories, "ListRepositoriesInDomain": h.listRepositoriesInDomain,
		"AssociateExternalConnection": h.associateExternalConnection, "DisassociateExternalConnection": h.disassociateExternalConnection,
		"TagResource": h.tagResource, "UntagResource": h.untagResource, "ListTagsForResource": h.listTagsForResource,
		opGetAuthorizationToken: h.getAuthorizationToken, "GetRepositoryEndpoint": h.getRepositoryEndpoint,
		opPutDomainPolicy: h.putDomainPolicy, "GetDomainPermissionsPolicy": h.getDomainPolicy,
		"DeleteDomainPermissionsPolicy": h.deleteDomainPolicy, "PutRepositoryPermissionsPolicy": h.putRepositoryPolicy,
		"GetRepositoryPermissionsPolicy": h.getRepositoryPolicy, "DeleteRepositoryPermissionsPolicy": h.deleteRepositoryPolicy,
		"ListPackages": h.listPackages, "DescribePackage": h.describePackage,
		"PutPackageOriginConfiguration": h.putPackageOriginConfiguration, "DeletePackage": h.deletePackage,
		"ListPackageVersions": h.listPackageVersions, "DescribePackageVersion": h.describePackageVersion,
		"PublishPackageVersion": h.publishPackageVersion, "ListPackageVersionAssets": h.listPackageVersionAssets,
		"GetPackageVersionAsset": h.getPackageVersionAsset, "GetPackageVersionReadme": h.getPackageVersionReadme,
		"ListPackageVersionDependencies": h.listPackageVersionDependencies,
		"UpdatePackageVersionsStatus":    h.updatePackageVersionsStatus, "DisposePackageVersions": h.disposePackageVersions,
		"DeletePackageVersions": h.deletePackageVersions, opCopyPackageVersions: h.copyPackageVersions,
		"CreatePackageGroup": h.createPackageGroup, "DescribePackageGroup": h.describePackageGroup,
		opUpdatePackageGroup: h.updatePackageGroup, "DeletePackageGroup": h.deletePackageGroup,
		"UpdatePackageGroupOriginConfiguration": h.updatePackageGroupOrigin, "ListPackageGroups": h.listPackageGroups,
		"ListSubPackageGroups": h.listSubPackageGroups, "ListAssociatedPackages": h.listAssociatedPackages,
		"GetAssociatedPackageGroup": h.getAssociatedPackageGroup, "ListAllowedRepositoriesForGroup": h.listAllowedRepositories,
	}
}
