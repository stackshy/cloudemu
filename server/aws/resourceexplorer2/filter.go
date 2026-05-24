package resourceexplorer2

import (
	"strings"

	"github.com/stackshy/cloudemu/resourcediscovery"
)

// parseFilter parses Resource Explorer's documented query subset into a
// resourcediscovery.Query. Supported tokens:
//
//	service:<name>          → Query.Service (after AWS→portable mapping)
//	region:<name>           → Query.Region
//	tag.<key>:<value>       → Query.Tags[key] = value
//	tag.<key>:              → Query.Tags[key] = "" (key-only match)
//
// Tokens are whitespace-separated. Unknown tokens are tolerated and
// ignored — matches Resource Explorer's permissive parser behavior for a
// minimal-impact-on-real-callers SDK round-trip.
func parseFilter(query string) resourcediscovery.Query {
	q := resourcediscovery.Query{}
	if strings.TrimSpace(query) == "" {
		return q
	}

	for _, token := range strings.Fields(query) {
		applyToken(&q, token)
	}

	return q
}

func applyToken(q *resourcediscovery.Query, token string) {
	if strings.HasPrefix(token, "tag.") {
		applyTagToken(q, strings.TrimPrefix(token, "tag."))
		return
	}

	key, val, ok := strings.Cut(token, ":")
	if !ok {
		return
	}

	switch key {
	case "service":
		q.Services = append(q.Services, awsToPortableServices(val)...)
	case "region":
		q.Region = val
	}
}

func applyTagToken(q *resourcediscovery.Query, body string) {
	key, val, _ := strings.Cut(body, ":")
	if key == "" {
		return
	}

	if q.Tags == nil {
		q.Tags = make(map[string]string)
	}

	q.Tags[key] = val
}

// awsToPortableServices maps an AWS service name to the set of portable
// service names that match it. Most AWS services map 1:1; "ec2" expands to
// {compute, networking} because real AWS uses ec2 for both VMs and VPC
// resources while cloudemu's portable API splits them.
func awsToPortableServices(s string) []string {
	switch s {
	case awsServiceEC2:
		return []string{portableServiceCompute, portableServiceNetworking}
	case awsServiceS3:
		return []string{portableServiceStorage}
	case awsServiceDynamoDB:
		return []string{portableServiceDatabase}
	case awsServiceLambda:
		return []string{portableServiceServerless}
	default:
		return []string{s}
	}
}
