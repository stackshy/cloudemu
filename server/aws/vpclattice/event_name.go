package vpclattice

import (
	"net/http"
)

// SigningName is the SigV4 credential-scope service name VPC Lattice requests
// are signed with.
const SigningName = "vpc-lattice"

// Top-level path roots and sub-paths of the REST-JSON routes.
const (
	rootServices            = "services"
	rootTargetGroups        = "targetgroups"
	rootDomainVerifications = "domainverifications"
	rootAuthPolicy          = "authpolicy"
	rootResourcePolicy      = "resourcepolicy"
	rootTags                = "tags"

	pathListeners         = "listeners"
	pathRules             = "rules"
	pathRegisterTargets   = "registertargets"
	pathDeregisterTargets = "deregistertargets"
	pathListTargets       = "listtargets"
)

// collectionNouns maps a top-level collection root to its singular and plural
// operation nouns ("servicenetworks" -> ServiceNetwork/ServiceNetworks).
var collectionNouns = map[string][2]string{ //nolint:gochecknoglobals // static route-name lookup table
	"servicenetworks":                       {"ServiceNetwork", "ServiceNetworks"},
	rootServices:                            {"Service", "Services"},
	rootTargetGroups:                        {"TargetGroup", "TargetGroups"},
	"servicenetworkvpcassociations":         {"ServiceNetworkVpcAssociation", "ServiceNetworkVpcAssociations"},
	"servicenetworkvpcendpointassociations": {"ServiceNetworkVpcEndpointAssociation", "ServiceNetworkVpcEndpointAssociations"},
	"servicenetworkserviceassociations":     {"ServiceNetworkServiceAssociation", "ServiceNetworkServiceAssociations"},
	"servicenetworkresourceassociations":    {"ServiceNetworkResourceAssociation", "ServiceNetworkResourceAssociations"},
	"resourceconfigurations":                {"ResourceConfiguration", "ResourceConfigurations"},
	"resourcegateways":                      {"ResourceGateway", "ResourceGateways"},
	"resourceendpointassociations":          {"ResourceEndpointAssociation", "ResourceEndpointAssociations"},
	"accesslogsubscriptions":                {"AccessLogSubscription", "AccessLogSubscriptions"},
	rootDomainVerifications:                 {"DomainVerification", "DomainVerifications"},
}

// OperationName returns the VPC Lattice API operation name (the CloudTrail
// EventName) for a request, or "" when the method and path match no operation.
// The REST-JSON protocol has no X-Amz-Target header, so the name is derived from
// the same method + path shapes the handler routes on.
func OperationName(method, path string) string {
	segs := splitPath(path)
	if len(segs) == 0 {
		return ""
	}

	switch segs[0] {
	case rootTags:
		return tagOperation(method)
	case rootAuthPolicy:
		return policyOperation(method, "AuthPolicy")
	case rootResourcePolicy:
		return policyOperation(method, "ResourcePolicy")
	}

	return resourceOperation(method, segs[0], segs[1:])
}

func tagOperation(method string) string {
	switch method {
	case http.MethodPost:
		return "TagResource"
	case http.MethodGet:
		return "ListTagsForResource"
	case http.MethodDelete:
		return "UntagResource"
	default:
		return ""
	}
}

func policyOperation(method, noun string) string {
	switch method {
	case http.MethodPut:
		return "Put" + noun
	case http.MethodGet:
		return "Get" + noun
	case http.MethodDelete:
		return "Delete" + noun
	default:
		return ""
	}
}

func resourceOperation(method, root string, rest []string) string {
	// /services/{id}/listeners[/{id}[/rules[/{id}]]] nest under a service.
	if root == rootServices && len(rest) >= 2 && rest[1] == pathListeners {
		return listenerOperation(method, rest[2:])
	}

	// /targetgroups/{id}/registertargets|deregistertargets|listtargets.
	if root == rootTargetGroups && len(rest) == 2 {
		return targetOperation(method, rest[1])
	}

	nouns, ok := collectionNouns[root]
	if !ok || len(rest) > 1 {
		return ""
	}

	return crudOperation(method, nouns[0], nouns[1], root == rootDomainVerifications, len(rest) == 0)
}

func targetOperation(method, action string) string {
	if method != http.MethodPost {
		return ""
	}

	switch action {
	case pathRegisterTargets:
		return "RegisterTargets"
	case pathDeregisterTargets:
		return "DeregisterTargets"
	case pathListTargets:
		return "ListTargets"
	default:
		return ""
	}
}

// listenerOperation names operations under /services/{id}/listeners.
func listenerOperation(method string, rest []string) string {
	if len(rest) >= 2 && rest[1] == pathRules {
		return ruleOperation(method, rest[2:])
	}

	if len(rest) > 1 {
		return ""
	}

	return crudOperation(method, "Listener", "Listeners", false, len(rest) == 0)
}

// ruleOperation names operations under .../listeners/{id}/rules. PATCH on the
// bare collection is BatchUpdateRule.
func ruleOperation(method string, rest []string) string {
	if len(rest) == 0 && method == http.MethodPatch {
		return "BatchUpdateRule"
	}

	if len(rest) > 1 {
		return ""
	}

	return crudOperation(method, "Rule", "Rules", false, len(rest) == 0)
}

// crudOperation maps a verb to Create/List (collection) or Get/Update/Delete
// (single resource). A domain verification is started, not created.
func crudOperation(method, singular, plural string, start, collection bool) string {
	if collection {
		return collectionOperation(method, singular, plural, start)
	}

	switch method {
	case http.MethodGet:
		return "Get" + singular
	case http.MethodPatch:
		return "Update" + singular
	case http.MethodDelete:
		return "Delete" + singular
	default:
		return ""
	}
}

func collectionOperation(method, singular, plural string, start bool) string {
	switch {
	case method == http.MethodPost && start:
		return "Start" + singular
	case method == http.MethodPost:
		return "Create" + singular
	case method == http.MethodGet:
		return "List" + plural
	default:
		return ""
	}
}
