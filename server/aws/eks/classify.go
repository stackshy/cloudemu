package eks

import (
	"net/http"
	"strings"
)

// opID names the EKS API operation a request runs. classify picks it from the
// request, and ServeHTTP and IAMChecks both use that choice, so the operation
// IAM authorizes is the one that runs. opUnknown is every request the handler
// answers with an error and no side effect.
type opID = string

// The EKS operations this handler serves.
const (
	opUnknown opID = ""

	opListAccessPolicies         opID = "ListAccessPolicies"
	opDescribeAddonVersions      opID = "DescribeAddonVersions"
	opDescribeAddonConfiguration opID = "DescribeAddonConfiguration"

	opTagResource         opID = "TagResource"
	opUntagResource       opID = "UntagResource"
	opListTagsForResource opID = "ListTagsForResource"

	opCreateCluster        opID = "CreateCluster"
	opListClusters         opID = "ListClusters"
	opDescribeCluster      opID = "DescribeCluster"
	opDeleteCluster        opID = "DeleteCluster"
	opUpdateClusterConfig  opID = "UpdateClusterConfig"
	opUpdateClusterVersion opID = "UpdateClusterVersion"
	opListUpdates          opID = "ListUpdates"
	opDescribeUpdate       opID = "DescribeUpdate"

	opCreateNodegroup        opID = "CreateNodegroup"
	opListNodegroups         opID = "ListNodegroups"
	opDescribeNodegroup      opID = "DescribeNodegroup"
	opDeleteNodegroup        opID = "DeleteNodegroup"
	opUpdateNodegroupConfig  opID = "UpdateNodegroupConfig"
	opUpdateNodegroupVersion opID = "UpdateNodegroupVersion"

	opCreateFargateProfile   opID = "CreateFargateProfile"
	opListFargateProfiles    opID = "ListFargateProfiles"
	opDescribeFargateProfile opID = "DescribeFargateProfile"
	opDeleteFargateProfile   opID = "DeleteFargateProfile"

	opCreateAddon   opID = "CreateAddon"
	opListAddons    opID = "ListAddons"
	opDescribeAddon opID = "DescribeAddon"
	opDeleteAddon   opID = "DeleteAddon"
	opUpdateAddon   opID = "UpdateAddon"

	opCreateAccessEntry            opID = "CreateAccessEntry"
	opListAccessEntries            opID = "ListAccessEntries"
	opDescribeAccessEntry          opID = "DescribeAccessEntry"
	opUpdateAccessEntry            opID = "UpdateAccessEntry"
	opDeleteAccessEntry            opID = "DeleteAccessEntry"
	opAssociateAccessPolicy        opID = "AssociateAccessPolicy"
	opListAssociatedAccessPolicies opID = "ListAssociatedAccessPolicies"
	opDisassociateAccessPolicy     opID = "DisassociateAccessPolicy"
)

// failKind is the error branch of an opUnknown request.
type failKind int

const (
	// failMethod is a known path with a method it does not serve (405).
	failMethod failKind = iota
	// failNotFound is an unrecognized path (404 ResourceNotFoundException).
	failNotFound
	// failMalformed is a path with a bad percent-escape (400).
	failMalformed
	// failTagsMethod is a tagging request with a method the tagging API does
	// not serve.
	failTagsMethod
)

// opArgs is what classify extracts from the request.
type opArgs struct {
	// cluster is the cluster name, as dispatch passes it to the driver.
	cluster string
	// child is the nodegroup, Fargate profile, add-on or update id, or the
	// principal ARN of an access entry.
	child string
	// policyARN is the access policy DisassociateAccessPolicy removes.
	policyARN string
	// tagARN is the resource ARN of a tagging request, and tag what it names.
	tagARN string
	tag    tagRef
	// fail is the error branch of an opUnknown request, and failMsg its
	// message.
	fail    failKind
	failMsg string
}

// unknown returns the opUnknown result for an error branch.
func unknown(a *opArgs, kind failKind, msg string) (opID, opArgs) {
	a.fail, a.failMsg = kind, msg
	return opUnknown, *a
}

// pick returns the operation ops maps method to, or the 405 branch.
func pick(method string, ops map[string]opID, a *opArgs) (opID, opArgs) {
	if op, ok := ops[method]; ok {
		return op, *a
	}

	return unknown(a, failMethod, "")
}

// classify names the operation r runs and the resources it acts on, the same
// way for ServeHTTP and IAMChecks.
func classify(r *http.Request) (opID, opArgs) {
	switch r.URL.Path {
	case pathAccessPolicies:
		return pick(r.Method, map[string]opID{http.MethodGet: opListAccessPolicies}, &opArgs{})
	case pathAddonVersions:
		return pick(r.Method, map[string]opID{http.MethodGet: opDescribeAddonVersions}, &opArgs{})
	case pathAddonSchemas:
		return pick(r.Method, map[string]opID{http.MethodGet: opDescribeAddonConfiguration}, &opArgs{})
	}

	if arn, ok := strings.CutPrefix(r.URL.Path, tagsPrefix); ok {
		a := opArgs{tagARN: arn, tag: parseTagARN(arn)}

		op, a := pick(r.Method, map[string]opID{
			http.MethodPost: opTagResource, http.MethodDelete: opUntagResource, http.MethodGet: opListTagsForResource,
		}, &a)
		if op == opUnknown {
			a.fail = failTagsMethod
		}

		return op, a
	}

	parts, ok := splitPath(r.URL.EscapedPath())
	if !ok {
		return unknown(&opArgs{}, failMalformed, "malformed path: "+r.URL.Path)
	}

	return classifyClusterPath(r.Method, r.URL.Path, parts)
}

// classifyClusterPath names the operations of /clusters/... by segment count.
func classifyClusterPath(method, path string, parts []string) (opID, opArgs) {
	switch len(parts) {
	case 0:
		return pick(method, map[string]opID{http.MethodPost: opCreateCluster, http.MethodGet: opListClusters}, &opArgs{})
	case pathSegsCluster:
		return pick(method, map[string]opID{http.MethodGet: opDescribeCluster, http.MethodDelete: opDeleteCluster},
			&opArgs{cluster: parts[0]})
	case pathSegsClusterSubresource:
		return classifySubresource(method, parts[0], parts[1])
	case pathSegsChildResource:
		return classifyChild(method, parts[0], parts[1], parts[2])
	case pathSegsChildAction:
		return classifyChildAction(method, parts[0], parts[1], parts[2], parts[3])
	case pathSegsPolicyAssociation:
		a := opArgs{cluster: parts[0], child: parts[2], policyARN: parts[4]}
		if parts[1] != segAccessEntries || parts[3] != segAccessPolicies {
			return unknown(&a, failNotFound, "unsupported path: "+path)
		}

		return pick(method, map[string]opID{http.MethodDelete: opDisassociateAccessPolicy}, &a)
	default:
		return unknown(&opArgs{}, failNotFound, "unsupported path: "+path)
	}
}

// classifySubresource names the operations of /clusters/{name}/{sub}.
func classifySubresource(method, cluster, sub string) (opID, opArgs) {
	a := opArgs{cluster: cluster}

	var ops map[string]opID

	switch sub {
	case segUpdateConfig:
		ops = map[string]opID{http.MethodPost: opUpdateClusterConfig}
	case segUpdates:
		ops = map[string]opID{http.MethodPost: opUpdateClusterVersion, http.MethodGet: opListUpdates}
	case segNodeGroups:
		ops = map[string]opID{http.MethodPost: opCreateNodegroup, http.MethodGet: opListNodegroups}
	case segFargateProfiles:
		ops = map[string]opID{http.MethodPost: opCreateFargateProfile, http.MethodGet: opListFargateProfiles}
	case segAddons:
		ops = map[string]opID{http.MethodPost: opCreateAddon, http.MethodGet: opListAddons}
	case segAccessEntries:
		ops = map[string]opID{http.MethodPost: opCreateAccessEntry, http.MethodGet: opListAccessEntries}
	default:
		return unknown(&a, failNotFound, "unknown cluster sub-resource: "+sub)
	}

	return pick(method, ops, &a)
}

// classifyChild names the operations of /clusters/{name}/{kind}/{child}.
func classifyChild(method, cluster, kind, child string) (opID, opArgs) {
	a := opArgs{cluster: cluster, child: child}

	var ops map[string]opID

	switch kind {
	case segNodeGroups:
		ops = map[string]opID{http.MethodGet: opDescribeNodegroup, http.MethodDelete: opDeleteNodegroup}
	case segFargateProfiles:
		ops = map[string]opID{http.MethodGet: opDescribeFargateProfile, http.MethodDelete: opDeleteFargateProfile}
	case segAddons:
		ops = map[string]opID{http.MethodGet: opDescribeAddon, http.MethodDelete: opDeleteAddon}
	case segAccessEntries:
		ops = map[string]opID{
			http.MethodGet: opDescribeAccessEntry, http.MethodPost: opUpdateAccessEntry, http.MethodDelete: opDeleteAccessEntry,
		}
	case segUpdates:
		ops = map[string]opID{http.MethodGet: opDescribeUpdate}
	default:
		return unknown(&a, failNotFound, "unknown child resource: "+kind)
	}

	return pick(method, ops, &a)
}

// classifyChildAction names the operations of
// /clusters/{name}/{kind}/{child}/{action}.
func classifyChildAction(method, cluster, kind, child, action string) (opID, opArgs) {
	a := opArgs{cluster: cluster, child: child}

	if kind == segAccessEntries && action == segAccessPolicies {
		return pick(method, map[string]opID{http.MethodPost: opAssociateAccessPolicy, http.MethodGet: opListAssociatedAccessPolicies}, &a)
	}

	if method != http.MethodPost {
		return unknown(&a, failMethod, "")
	}

	switch {
	case kind == segNodeGroups && action == segUpdateConfig:
		return opUpdateNodegroupConfig, a
	case kind == segNodeGroups && action == segUpdateVersion:
		return opUpdateNodegroupVersion, a
	case kind == segAddons && action == segUpdate:
		return opUpdateAddon, a
	default:
		return unknown(&a, failNotFound, "unknown child action: "+kind+"/"+action)
	}
}

// serviceName is the IAM service prefix of EKS actions and the service field
// of EKS ARNs.
const serviceName = "eks"

// The resource types of EKS ARNs (Service Authorization Reference, Amazon
// EKS, "Resource types").
const (
	arnKindCluster     = "cluster"
	arnKindNodegroup   = "nodegroup"
	arnKindFargate     = "fargateprofile"
	arnKindAddon       = "addon"
	arnKindAccessEntry = "access-entry"
)

// tagRef is what a tagging ARN names: an EKS ARN
// arn:<partition>:eks:<region>:<account>:<kind>/<cluster>[/<name>...]. kind is
// empty when the ARN is not one.
type tagRef struct {
	region, account, kind string
	// cluster and name are the cluster and child resource names; name is
	// empty for a cluster or an access entry.
	cluster, name string
}

// parseTagARN reads a tagging ARN the way the provider resolves it: a
// cluster by cluster/<name>, a nodegroup, Fargate profile or add-on by
// <kind>/<cluster>/<name> (a trailing id is ignored), and an access entry by
// its whole ARN.
func parseTagARN(arn string) tagRef {
	const fields = 6 // arn, partition, service, region, account, resource

	parts := strings.SplitN(arn, ":", fields)
	if len(parts) != fields || parts[0] != "arn" || parts[2] != serviceName || parts[3] == "" || parts[4] == "" {
		return tagRef{}
	}

	ref, ok := parseResourcePath(strings.Split(parts[5], "/"))
	if !ok {
		return tagRef{}
	}

	ref.region, ref.account = parts[3], parts[4]

	return ref
}

// parseResourcePath reads the <kind>/<cluster>[/<name>...] part of an EKS ARN.
func parseResourcePath(segs []string) (tagRef, bool) {
	const (
		clusterSegs = 2
		childSegs   = 3
	)

	if len(segs) < clusterSegs || segs[1] == "" {
		return tagRef{}, false
	}

	switch kind := segs[0]; kind {
	case arnKindCluster:
		return tagRef{kind: kind, cluster: segs[1]}, len(segs) == clusterSegs
	case arnKindNodegroup, arnKindFargate, arnKindAddon:
		ok := len(segs) >= childSegs && segs[2] != ""
		if !ok {
			return tagRef{}, false
		}

		return tagRef{kind: kind, cluster: segs[1], name: segs[2]}, true
	case arnKindAccessEntry:
		return tagRef{kind: kind, cluster: segs[1]}, len(segs) > clusterSegs
	default:
		return tagRef{}, false
	}
}

// foreign reports whether the tagging ARN names nothing this handler serves:
// not an EKS resource ARN, or one of another account or region.
func (t *tagRef) foreign(accountID, region string) bool {
	return t.kind == "" || (accountID != "" && t.account != accountID) || (region != "" && t.region != region)
}
