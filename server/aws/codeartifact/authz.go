package codeartifact

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/awsauthz"
)

// Compile-time check that Handler names the IAM actions of its requests.
var _ awsauthz.Resolver = (*Handler)(nil)

// resourceKind says which ARN an operation is authorized against.
type resourceKind int

const (
	resNone resourceKind = iota
	resDomain
	resRepository
	resPackage
	resGroup
	resTagged
)

// resourceKinds maps an operation to the resource its IAM action applies to.
// Operations absent from the map apply to no resource ("*").
func resourceKinds() map[string]resourceKind {
	kinds := map[string]resourceKind{}

	for _, op := range []string{
		"CreateDomain", "DescribeDomain", "DeleteDomain", "ListRepositoriesInDomain", opGetAuthorizationToken,
		opPutDomainPolicy, "GetDomainPermissionsPolicy", "DeleteDomainPermissionsPolicy",
		"CreatePackageGroup", "ListPackageGroups", "GetAssociatedPackageGroup",
	} {
		kinds[op] = resDomain
	}

	for _, op := range []string{
		"CreateRepository", "DescribeRepository", "UpdateRepository", "DeleteRepository", "AssociateExternalConnection",
		"DisassociateExternalConnection", "GetRepositoryEndpoint", "PutRepositoryPermissionsPolicy",
		"GetRepositoryPermissionsPolicy", "DeleteRepositoryPermissionsPolicy", "ListPackages",
	} {
		kinds[op] = resRepository
	}

	for _, op := range []string{
		"DescribePackage", "PutPackageOriginConfiguration", "DeletePackage", "ListPackageVersions", "DescribePackageVersion",
		"PublishPackageVersion", "ListPackageVersionAssets", "GetPackageVersionAsset", "GetPackageVersionReadme",
		"ListPackageVersionDependencies", "UpdatePackageVersionsStatus", "DisposePackageVersions", "DeletePackageVersions",
		opCopyPackageVersions,
	} {
		kinds[op] = resPackage
	}

	for _, op := range []string{
		"DescribePackageGroup", opUpdatePackageGroup, "DeletePackageGroup", "UpdatePackageGroupOriginConfiguration",
		"ListSubPackageGroups", "ListAssociatedPackages", "ListAllowedRepositoriesForGroup",
	} {
		kinds[op] = resGroup
	}

	for _, op := range []string{"TagResource", "UntagResource", "ListTagsForResource"} {
		kinds[op] = resTagged
	}

	return kinds
}

// IAMChecks names the IAM actions a request needs: codeartifact:<Operation>
// against the domain, repository, package or package group it names, plus
// sts:GetServiceBearerToken for GetAuthorizationToken. ok=false means the
// request is not a CodeArtifact operation, and ServeHTTP then answers it without
// side effects.
func (*Handler) IAMChecks(r *http.Request, s awsauthz.Scope) ([]awsauthz.Check, bool) {
	op := OperationName(r.Method, r.URL.Path)
	if op == "" {
		return nil, false
	}

	checks := []awsauthz.Check{{Action: "codeartifact:" + op, Resource: resourceARN(op, r, s)}}

	if op == opGetAuthorizationToken {
		checks = append(checks, awsauthz.Check{Action: "sts:GetServiceBearerToken", Resource: "*"})
	}

	return checks, true
}

// resourceARN builds the ARN of the resource an operation names. An empty result
// means the request does not name it, which the gate evaluates conservatively.
func resourceARN(op string, r *http.Request, s awsauthz.Scope) string {
	q := r.URL.Query()

	switch resourceKinds()[op] {
	case resNone:
		return "*"
	case resDomain:
		return domainARN(op, r, q, s)
	case resRepository:
		return repositoryARN(q, s)
	case resPackage:
		return packageARN(op, q, s)
	case resGroup:
		return groupARN(op, r, q, s)
	case resTagged:
		return q.Get("resourceArn")
	default:
		return "*"
	}
}

// domainARN names the domain of an operation; PutDomainPermissionsPolicy carries
// it in the body.
func domainARN(op string, r *http.Request, q url.Values, s awsauthz.Scope) string {
	domain := q.Get("domain")

	if op == opPutDomainPolicy {
		var body putDomainPolicyRequest
		if !awsauthz.JSONBody(r, &body) {
			return ""
		}

		domain = body.Domain
	}

	if domain == "" {
		return ""
	}

	return s.ARN("codeartifact", "domain/"+domain)
}

func repositoryARN(q url.Values, s awsauthz.Scope) string {
	if q.Get("domain") == "" || q.Get("repository") == "" {
		return ""
	}

	return s.ARN("codeartifact", "repository/"+q.Get("domain")+"/"+q.Get("repository"))
}

// packageARN names a package, or for CopyPackageVersions the package in the
// destination repository.
func packageARN(op string, q url.Values, s awsauthz.Scope) string {
	repo := q.Get("repository")
	if op == opCopyPackageVersions {
		repo = q.Get("destination-repository")
	}

	if q.Get("domain") == "" || repo == "" {
		return ""
	}

	return s.ARN("codeartifact", "package/"+strings.Join([]string{
		q.Get("domain"), repo, q.Get("format"), q.Get("namespace"), q.Get("package"),
	}, "/"))
}

// groupARN names a package group; UpdatePackageGroup carries the pattern in the
// body.
func groupARN(op string, r *http.Request, q url.Values, s awsauthz.Scope) string {
	pattern := q.Get("package-group")

	if op == opUpdatePackageGroup {
		var body updatePackageGroupRequest
		if !awsauthz.JSONBody(r, &body) {
			return ""
		}

		pattern = body.PackageGroup
	}

	if q.Get("domain") == "" || pattern == "" {
		return ""
	}

	return s.ARN("codeartifact", "package-group/"+q.Get("domain")+pattern)
}
