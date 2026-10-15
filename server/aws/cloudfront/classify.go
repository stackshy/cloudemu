package cloudfront

import (
	"net/http"
	"strings"
)

// opID names the CloudFront API operation a request runs. classify picks it
// from the request, and ServeHTTP and IAMChecks both use that choice, so the
// operation IAM authorizes is the one that runs. opUnknown is every request
// the handler answers with an error and no side effect.
type opID = string

// The CloudFront operations this handler serves.
const (
	opUnknown opID = ""

	opCreateDistribution         opID = "CreateDistribution"
	opCreateDistributionWithTags opID = "CreateDistributionWithTags"
	opListDistributions          opID = "ListDistributions"
	opGetDistribution            opID = "GetDistribution"
	opDeleteDistribution         opID = "DeleteDistribution"
	opGetDistributionConfig      opID = "GetDistributionConfig"
	opUpdateDistribution         opID = "UpdateDistribution"
	opCreateInvalidation         opID = "CreateInvalidation"
	opListInvalidations          opID = "ListInvalidations"
	opGetInvalidation            opID = "GetInvalidation"
	opListTagsForResource        opID = "ListTagsForResource"
	opTagResource                opID = "TagResource"
	opUntagResource              opID = "UntagResource"
)

// serviceName is the IAM service prefix of CloudFront actions and the service
// field of CloudFront ARNs.
const serviceName = "cloudfront"

// arnDistributionPrefix is the resource part of a distribution ARN.
const arnDistributionPrefix = "distribution/"

// failure is the error ServeHTTP writes for a request classify cannot name.
type failure int

const (
	// failMethod is a known path with a method it does not serve (405).
	failMethod failure = iota
	// failPath is an unrecognized path under a distribution (404).
	failPath
	// failTagging is a tagging request that is not List, Tag or Untag (400).
	failTagging
)

// opArgs is what classify extracts from the request.
type opArgs struct {
	// id is the distribution id the operation acts on.
	id string
	// invalidationID is the invalidation GetInvalidation reads.
	invalidationID string
	// resource is the Resource ARN of a tagging request.
	resource string
	// resourceAccount is the account a tagging ARN names, and id the
	// distribution id it names; both are empty when it is not a distribution
	// ARN.
	resourceAccount string
	// fail is the error branch of an opUnknown request.
	fail failure
}

// classify names the operation r runs and the ids it acts on, the same way
// for ServeHTTP and IAMChecks.
func classify(r *http.Request) (opID, opArgs) {
	p := r.URL.Path

	if p == taggingPrefix || strings.HasPrefix(p, taggingPrefix+"/") {
		return classifyTagging(r)
	}

	tail := strings.Trim(strings.TrimPrefix(p, distPrefix), "/")
	if tail == "" {
		return classifyCollection(r)
	}

	return classifyDistribution(r.Method, strings.Split(tail, "/"))
}

func classifyTagging(r *http.Request) (opID, opArgs) {
	q := r.URL.Query()
	a := opArgs{resource: q.Get("Resource")}
	a.resourceAccount, a.id = parseDistributionARN(a.resource)

	switch {
	case r.Method == http.MethodGet:
		return opListTagsForResource, a
	case r.Method == http.MethodPost && q.Get("Operation") == "Tag":
		return opTagResource, a
	case r.Method == http.MethodPost && q.Get("Operation") == "Untag":
		return opUntagResource, a
	default:
		a.fail = failTagging
		return opUnknown, a
	}
}

func classifyCollection(r *http.Request) (opID, opArgs) {
	switch r.Method {
	case http.MethodPost:
		if r.URL.Query().Has("WithTags") {
			return opCreateDistributionWithTags, opArgs{}
		}

		return opCreateDistribution, opArgs{}
	case http.MethodGet:
		return opListDistributions, opArgs{}
	default:
		return opUnknown, opArgs{fail: failMethod}
	}
}

// classifyDistribution names the operations of /distribution/{Id}[/config |
// /invalidation[/{InvId}]].
func classifyDistribution(method string, segs []string) (opID, opArgs) {
	const (
		segsDistribution = 1
		segsSub          = 2
		segsSubItem      = 3
	)

	a := opArgs{id: segs[0]}

	var ops map[string]opID

	switch {
	case len(segs) == segsDistribution:
		ops = map[string]opID{http.MethodGet: opGetDistribution, http.MethodDelete: opDeleteDistribution}
	case len(segs) == segsSub && segs[1] == configSeg:
		ops = map[string]opID{http.MethodGet: opGetDistributionConfig, http.MethodPut: opUpdateDistribution}
	case len(segs) == segsSub && segs[1] == invalidationSeg:
		ops = map[string]opID{http.MethodPost: opCreateInvalidation, http.MethodGet: opListInvalidations}
	case len(segs) == segsSubItem && segs[1] == invalidationSeg:
		a.invalidationID = segs[2]
		ops = map[string]opID{http.MethodGet: opGetInvalidation}
	default:
		a.fail = failPath
		return opUnknown, a
	}

	if op, ok := ops[method]; ok {
		return op, a
	}

	a.fail = failMethod

	return opUnknown, a
}

// parseDistributionARN returns the account and distribution id of
// arn:<partition>:cloudfront::<account>:distribution/<id>, or two empty
// strings when arn is not a distribution ARN.
func parseDistributionARN(arn string) (account, id string) {
	const fields = 6 // arn, partition, service, region, account, resource

	parts := strings.SplitN(arn, ":", fields)
	if len(parts) != fields || parts[0] != "arn" || parts[1] == "" || parts[2] != serviceName ||
		parts[3] != "" || parts[4] == "" {
		return "", ""
	}

	id, ok := strings.CutPrefix(parts[5], arnDistributionPrefix)
	if !ok || id == "" || strings.ContainsAny(id, "/:") {
		return "", ""
	}

	return parts[4], id
}
