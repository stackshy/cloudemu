package route53

import (
	"net/http"
	"strings"
)

// opID names the Route 53 API operation a request runs. classify picks it
// from the request, and ServeHTTP and IAMChecks both use that choice, so the
// operation IAM authorizes is the one that runs. opUnknown is every request
// the handler answers with an error and no side effect.
type opID = string

// The Route 53 operations this handler serves.
const (
	opUnknown opID = ""

	opCreateHostedZone              opID = "CreateHostedZone"
	opListHostedZones               opID = "ListHostedZones"
	opGetHostedZone                 opID = "GetHostedZone"
	opDeleteHostedZone              opID = "DeleteHostedZone"
	opUpdateHostedZoneComment       opID = "UpdateHostedZoneComment"
	opChangeResourceRecordSets      opID = "ChangeResourceRecordSets"
	opListResourceRecordSets        opID = "ListResourceRecordSets"
	opAssociateVPCWithHostedZone    opID = "AssociateVPCWithHostedZone"
	opDisassociateVPCFromHostedZone opID = "DisassociateVPCFromHostedZone"
	opGetChange                     opID = "GetChange"
	opGetHostedZoneCount            opID = "GetHostedZoneCount"
	opListHostedZonesByName         opID = "ListHostedZonesByName"
	opListHostedZonesByVPC          opID = "ListHostedZonesByVPC"
	opTestDNSAnswer                 opID = "TestDNSAnswer"
	opCreateHealthCheck             opID = "CreateHealthCheck"
	opListHealthChecks              opID = "ListHealthChecks"
	opGetHealthCheck                opID = "GetHealthCheck"
	opUpdateHealthCheck             opID = "UpdateHealthCheck"
	opDeleteHealthCheck             opID = "DeleteHealthCheck"
	opChangeTagsForResource         opID = "ChangeTagsForResource"
	opListTagsForResource           opID = "ListTagsForResource"
)

// failure is the error ServeHTTP writes for a request classify cannot name.
type failure int

const (
	// failMethod is a known path with a method it does not serve (405).
	failMethod failure = iota
	// failPath is an unrecognized path under /hostedzone/{id} (404).
	failPath
	// failTags is a tagging request with no resource id, or with a method the
	// tagging API does not serve.
	failTags
)

// opArgs is what classify extracts from the request.
type opArgs struct {
	// id is the hosted zone, health check, change or tagged resource id, as
	// dispatch passes it to the driver.
	id string
	// tagType is the {ResourceType} segment of a tagging request.
	tagType string
	// fail is the error branch of an opUnknown request.
	fail failure
}

// classify names the operation r runs and the ids it acts on. It reads the
// path and method only, the same way for ServeHTTP and IAMChecks.
func classify(r *http.Request) (opID, opArgs) {
	p := r.URL.Path

	switch {
	case strings.HasPrefix(p, tagsPrefix):
		return classifyTags(r.Method, strings.TrimPrefix(p, tagsPrefix))
	case p == healthCheckPrefix || strings.HasPrefix(p, healthCheckPrefix+"/"):
		return classifyHealthCheck(r.Method, strings.Trim(strings.TrimPrefix(p, healthCheckPrefix), "/"))
	case strings.HasPrefix(p, changePrefix):
		id := strings.TrimPrefix(strings.TrimPrefix(p, changePrefix), "/")
		id = strings.TrimPrefix(id, "change/")

		return onMethod(r.Method, http.MethodGet, opGetChange, opArgs{id: id})
	}

	switch p {
	case hostedZoneCountPath:
		return onMethod(r.Method, http.MethodGet, opGetHostedZoneCount, opArgs{})
	case hostedZonesByNamePath:
		return onMethod(r.Method, http.MethodGet, opListHostedZonesByName, opArgs{})
	case hostedZonesByVPCPath:
		return onMethod(r.Method, http.MethodGet, opListHostedZonesByVPC, opArgs{})
	case testDNSAnswerPath:
		return onMethod(r.Method, http.MethodGet, opTestDNSAnswer, opArgs{})
	}

	return classifyHostedZone(r.Method, strings.Trim(strings.TrimPrefix(p, pathPrefix), "/"))
}

// onMethod returns op when method is want, else the 405 branch.
func onMethod(method, want string, op opID, a opArgs) (opID, opArgs) {
	if method != want {
		a.fail = failMethod
		return opUnknown, a
	}

	return op, a
}

// byMethod picks the operation of method from ops, or the 405 branch.
func byMethod(method string, ops map[string]opID, a opArgs) (opID, opArgs) {
	if op, ok := ops[method]; ok {
		return op, a
	}

	a.fail = failMethod

	return opUnknown, a
}

// classifyHostedZone names the operations of /hostedzone[/{id}[/{sub}]].
func classifyHostedZone(method, tail string) (opID, opArgs) {
	if tail == "" {
		return byMethod(method, map[string]opID{
			http.MethodPost: opCreateHostedZone,
			http.MethodGet:  opListHostedZones,
		}, opArgs{})
	}

	id, sub, _ := strings.Cut(tail, "/")
	a := opArgs{id: trimZonePrefix(id)}

	switch sub {
	case "":
		return byMethod(method, map[string]opID{
			http.MethodGet:    opGetHostedZone,
			http.MethodDelete: opDeleteHostedZone,
			http.MethodPost:   opUpdateHostedZoneComment,
		}, a)
	case rrsetSeg:
		return byMethod(method, map[string]opID{
			http.MethodPost: opChangeResourceRecordSets,
			http.MethodGet:  opListResourceRecordSets,
		}, a)
	case associateVPCSeg:
		return onMethod(method, http.MethodPost, opAssociateVPCWithHostedZone, a)
	case disassociateVPCSeg:
		return onMethod(method, http.MethodPost, opDisassociateVPCFromHostedZone, a)
	default:
		a.fail = failPath
		return opUnknown, a
	}
}

// classifyHealthCheck names the operations of /healthcheck[/{id}].
func classifyHealthCheck(method, id string) (opID, opArgs) {
	if id == "" {
		return byMethod(method, map[string]opID{
			http.MethodPost: opCreateHealthCheck,
			http.MethodGet:  opListHealthChecks,
		}, opArgs{})
	}

	return byMethod(method, map[string]opID{
		http.MethodGet:    opGetHealthCheck,
		http.MethodPost:   opUpdateHealthCheck,
		http.MethodDelete: opDeleteHealthCheck,
	}, opArgs{id: id})
}

// classifyTags names the operations of /tags/{ResourceType}/{ResourceId}.
func classifyTags(method, tail string) (opID, opArgs) {
	resourceType, resourceID, _ := strings.Cut(tail, "/")
	a := opArgs{id: resourceID, tagType: resourceType, fail: failTags}

	if resourceID == "" {
		return opUnknown, a
	}

	switch method {
	case http.MethodPost:
		return opChangeTagsForResource, a
	case http.MethodGet:
		return opListTagsForResource, a
	default:
		return opUnknown, a
	}
}
