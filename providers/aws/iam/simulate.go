package iam

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/iam/driver"
)

// IAM policy simulation is AWS-only, so these methods are not part of the
// portable driver; the wire layer reaches them via a type assertion on the
// Mock. The evaluation reuses the same wildcard matcher CheckPermission uses.

// SimulatePrincipalPolicy evaluates the policies attached to the principal
// named by policySourceARN (plus any extra policy documents) against each
// action/resource pair (IAM SimulatePrincipalPolicy).
func (m *Mock) SimulatePrincipalPolicy(
	_ context.Context, policySourceARN string, actions, resourceARNs, extraPolicies []string,
) ([]driver.SimulationResult, error) {
	entityType, name := parsePrincipalARN(policySourceARN)
	docs := m.gatherPrincipalDocs(entityType, name)
	docs = append(docs, extraPolicies...)

	return simulate(docs, actions, resourceARNs), nil
}

// SimulateCustomPolicy evaluates a set of standalone policy documents against
// each action/resource pair (IAM SimulateCustomPolicy).
func (*Mock) SimulateCustomPolicy(
	_ context.Context, policyDocs, actions, resourceARNs []string,
) ([]driver.SimulationResult, error) {
	return simulate(policyDocs, actions, resourceARNs), nil
}

// simulate evaluates every action against every resource (defaulting to "*"
// when no resources are given) and reports the resulting decision.
func simulate(docs, actions, resourceARNs []string) []driver.SimulationResult {
	resources := resourceARNs
	if len(resources) == 0 {
		resources = []string{"*"}
	}

	results := make([]driver.SimulationResult, 0, len(actions)*len(resources))

	for _, action := range actions {
		for _, resource := range resources {
			results = append(results, driver.SimulationResult{
				ActionName:   action,
				ResourceName: resource,
				Decision:     decide(docs, action, resource),
			})
		}
	}

	return results
}

// decide reduces a set of policy documents to a single simulation decision for
// one action/resource: an explicit Deny wins, then any Allow, else the default
// implicit deny.
func decide(docs []string, action, resource string) string {
	allow, deny := false, false

	for _, doc := range docs {
		a, d := evaluatePolicy(doc, action, resource)
		if d {
			deny = true
		}

		if a {
			allow = true
		}
	}

	switch {
	case deny:
		return "explicitDeny"
	case allow:
		return "allowed"
	default:
		return "implicitDeny"
	}
}

// parsePrincipalARN extracts the entity type ("user", "role", or "group") and
// friendly name from an IAM principal ARN, tolerating an embedded path.
func parsePrincipalARN(arn string) (entityType, name string) {
	for _, t := range []string{"user", "role", "group"} {
		marker := ":" + t + "/"

		idx := strings.Index(arn, marker)
		if idx < 0 {
			continue
		}

		rest := arn[idx+len(marker):]
		if s := strings.LastIndexByte(rest, '/'); s >= 0 {
			rest = rest[s+1:]
		}

		return t, rest
	}

	return "", ""
}

// gatherPrincipalDocs returns every policy document in effect for a principal:
// its attached managed policies and inline policies, plus (for a user) the
// policies of every group it belongs to.
func (m *Mock) gatherPrincipalDocs(entityType, name string) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var docs []string

	switch entityType {
	case "user":
		docs = append(docs, m.managedDocsLocked(m.userPolicies[name])...)
		for _, d := range m.userInlinePolicies[name] {
			docs = append(docs, d)
		}

		for groupName, members := range m.groupUsers {
			if members[name] {
				docs = append(docs, m.groupDocsLocked(groupName)...)
			}
		}
	case "role":
		docs = append(docs, m.managedDocsLocked(m.rolePolicies[name])...)

		if rd, ok := m.roles.Get(name); ok {
			for _, d := range rd.inlinePolicies {
				docs = append(docs, d)
			}
		}
	case "group":
		docs = append(docs, m.groupDocsLocked(name)...)
	}

	return docs
}

// groupDocsLocked returns a group's managed and inline policy documents. The
// caller must hold m.mu.
func (m *Mock) groupDocsLocked(name string) []string {
	docs := m.managedDocsLocked(m.groupPolicies[name])
	for _, d := range m.groupInlinePolicies[name] {
		docs = append(docs, d)
	}

	return docs
}

// managedDocsLocked resolves a set of managed-policy ARNs to their default
// policy documents. The caller must hold m.mu.
func (m *Mock) managedDocsLocked(arns map[string]bool) []string {
	docs := make([]string, 0, len(arns))

	for arn := range arns {
		if p, ok := m.policies.Get(arn); ok && p.PolicyDocument != "" {
			docs = append(docs, p.PolicyDocument)
		}
	}

	return docs
}
