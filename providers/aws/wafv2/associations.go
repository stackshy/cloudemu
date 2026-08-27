package wafv2

import (
	"bytes"
	"context"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/wafv2/driver"
)

// webACLAssociated reports whether any protected resource is still associated
// with the given web ACL ARN. Deleting an associated web ACL is rejected with
// WAFAssociatedItemException, matching real WAFv2.
func (m *Mock) webACLAssociated(webACLARN string) bool {
	m.assocMu.RLock()
	defer m.assocMu.RUnlock()

	for _, aclARN := range m.assoc {
		if aclARN == webACLARN {
			return true
		}
	}

	return false
}

// itemReferencedByWebACL reports whether any web ACL's rules reference the given
// resource ARN. IPSet/RuleGroup/RegexPatternSet reference statements embed the
// referenced ARN verbatim in the rule JSON, so a substring scan of each web
// ACL's stored rules detects an in-use set/group.
func (m *Mock) itemReferencedByWebACL(arn string) bool {
	needle := []byte(arn)

	for _, wd := range m.webACLs.All() {
		wd.mu.RLock()
		referenced := bytes.Contains(wd.acl.Rules, needle)
		wd.mu.RUnlock()

		if referenced {
			return true
		}
	}

	return false
}

// webACLByARN finds a stored web ACL by its ARN.
func (m *Mock) webACLByARN(arn string) (*webACLData, bool) {
	for _, wd := range m.webACLs.All() {
		wd.mu.RLock()
		match := wd.acl.ARN == arn
		wd.mu.RUnlock()

		if match {
			return wd, true
		}
	}

	return nil, false
}

// AssociateWebACL associates a web ACL with a protected resource (ALB, API
// Gateway stage, etc.). Only REGIONAL web ACLs can be associated with resources.
func (m *Mock) AssociateWebACL(_ context.Context, webACLARN, resourceARN string) error {
	if webACLARN == "" || resourceARN == "" {
		return invalidParameter("WebACLArn and ResourceArn are required")
	}

	if _, ok := m.webACLByARN(webACLARN); !ok {
		return nonexistent("web ACL %q not found", webACLARN)
	}

	m.assocMu.Lock()
	defer m.assocMu.Unlock()

	m.assoc[resourceARN] = webACLARN

	return nil
}

// DisassociateWebACL removes the web-ACL association for a resource.
func (m *Mock) DisassociateWebACL(_ context.Context, resourceARN string) error {
	if resourceARN == "" {
		return invalidParameter("ResourceArn is required")
	}

	m.assocMu.Lock()
	defer m.assocMu.Unlock()

	delete(m.assoc, resourceARN)

	return nil
}

// GetWebACLForResource returns the web ACL protecting a resource, or a nil ACL
// with no error when none is associated (matching WAF's behavior).
func (m *Mock) GetWebACLForResource(_ context.Context, resourceARN string) (*driver.WebACL, error) {
	m.assocMu.RLock()
	aclARN, ok := m.assoc[resourceARN]
	m.assocMu.RUnlock()

	if !ok {
		return nil, nil //nolint:nilnil // no association is a valid empty result, not an error.
	}

	wd, ok := m.webACLByARN(aclARN)
	if !ok {
		return nil, nil //nolint:nilnil // association points at a deleted ACL; treat as none.
	}

	wd.mu.RLock()
	defer wd.mu.RUnlock()

	out := copyWebACL(&wd.acl)

	return &out, nil
}

// ListResourcesForWebACL returns the ARNs of resources associated with a web
// ACL, optionally filtered by resource type (matched against the ARN service
// segment).
func (m *Mock) ListResourcesForWebACL(_ context.Context, webACLARN, resourceType string) ([]string, error) {
	m.assocMu.RLock()
	defer m.assocMu.RUnlock()

	out := make([]string, 0)

	for resourceARN, aclARN := range m.assoc {
		if aclARN != webACLARN {
			continue
		}

		if resourceType != "" && !resourceMatchesType(resourceARN, resourceType) {
			continue
		}

		out = append(out, resourceARN)
	}

	return out, nil
}

// resourceMatchesType reports whether a resource ARN corresponds to the given
// WAF ResourceType (APPLICATION_LOAD_BALANCER, API_GATEWAY, …).
func resourceMatchesType(arn, resourceType string) bool {
	switch resourceType {
	case "APPLICATION_LOAD_BALANCER":
		return strings.Contains(arn, ":elasticloadbalancing:")
	case "API_GATEWAY":
		return strings.Contains(arn, ":apigateway:") || strings.Contains(arn, ":execute-api:")
	case "APPSYNC":
		return strings.Contains(arn, ":appsync:")
	case "COGNITO_USER_POOL":
		return strings.Contains(arn, ":cognito-idp:")
	case "APP_RUNNER_SERVICE":
		return strings.Contains(arn, ":apprunner:")
	case "VERIFIED_ACCESS_INSTANCE":
		return strings.Contains(arn, ":ec2:")
	default:
		return true
	}
}
