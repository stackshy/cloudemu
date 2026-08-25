package vpc

import (
	"context"
	"sort"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// Default ACL rule constants.
const (
	defaultACLRuleNumber = 100
	// defaultDenyRuleNumber is the reserved rule number of the unmodifiable
	// catch-all '*' entry every network ACL carries (one ingress, one egress).
	// Real EC2 numbers it 32767 in the API and denies everything that matches
	// no lower-numbered rule.
	defaultDenyRuleNumber = 32767
	allTrafficProtocol    = "-1"
	allTrafficCIDR        = "0.0.0.0/0"
	actionAllow           = "allow"
	actionDeny            = "deny"
)

type networkACLData struct {
	ID        string
	VPCID     string
	Rules     []driver.NetworkACLRule
	Tags      map[string]string
	IsDefault bool
}

// CreateNetworkACL creates a network ACL for the specified VPC.
func (m *Mock) CreateNetworkACL(_ context.Context, vpcID string, tags map[string]string) (*driver.NetworkACL, error) {
	if vpcID == "" {
		return nil, errors.New(errors.InvalidArgument, "VPC ID is required")
	}

	if !m.vpcs.Has(vpcID) {
		return nil, errors.Newf(errors.NotFound, "vpc %q not found", vpcID)
	}

	id := idgen.GenerateID("acl-")
	acl := &networkACLData{
		ID:        id,
		VPCID:     vpcID,
		Rules:     customACLRules(),
		Tags:      copyTags(tags),
		IsDefault: false,
	}
	m.networkACLs.Set(id, acl)

	info := toNetworkACLInfo(acl)

	return &info, nil
}

// denyAllRules returns the two unmodifiable catch-all '*' DENY entries (rule
// 32767, one ingress and one egress) that every network ACL — default and
// custom — carries. They are the last entries EC2 evaluates and deny anything
// that matched no lower-numbered rule.
func denyAllRules() []driver.NetworkACLRule {
	return []driver.NetworkACLRule{
		{
			RuleNumber: defaultDenyRuleNumber,
			Protocol:   allTrafficProtocol,
			Action:     actionDeny,
			CIDR:       allTrafficCIDR,
			Egress:     false,
		},
		{
			RuleNumber: defaultDenyRuleNumber,
			Protocol:   allTrafficProtocol,
			Action:     actionDeny,
			CIDR:       allTrafficCIDR,
			Egress:     true,
		},
	}
}

// customACLRules returns the entries a freshly created custom ACL carries: only
// the two '*' deny rules. A custom ACL has no numbered allow rule until the
// caller adds one, so it denies all inbound and outbound traffic — matching
// real EC2, where a fresh custom ACL is deny-by-default (not allow-all).
func customACLRules() []driver.NetworkACLRule {
	return denyAllRules()
}

// defaultACLRules returns the entries the VPC's auto-created default ACL
// carries: the numbered allow-all rule (100, both directions) plus the two '*'
// deny rules. This is what makes the default ACL permit all traffic while a
// custom ACL does not.
func defaultACLRules() []driver.NetworkACLRule {
	return append([]driver.NetworkACLRule{
		{
			RuleNumber: defaultACLRuleNumber,
			Protocol:   allTrafficProtocol,
			Action:     actionAllow,
			CIDR:       allTrafficCIDR,
			Egress:     false,
		},
		{
			RuleNumber: defaultACLRuleNumber,
			Protocol:   allTrafficProtocol,
			Action:     actionAllow,
			CIDR:       allTrafficCIDR,
			Egress:     true,
		},
	}, denyAllRules()...)
}

// DeleteNetworkACL deletes the network ACL with the given ID.
func (m *Mock) DeleteNetworkACL(_ context.Context, id string) error {
	acl, ok := m.networkACLs.Get(id)
	if !ok {
		return errors.Newf(errors.NotFound, "network ACL %q not found", id)
	}

	if acl.IsDefault {
		return errors.Newf(errors.FailedPrecondition, "cannot delete default network ACL %q", id)
	}

	// Real EC2 refuses to delete an ACL still associated with a subnet; the
	// caller must move the subnet (ReplaceNetworkAclAssociation) first.
	if m.aclHasAssociation(id) {
		return errors.Newf(errors.FailedPrecondition,
			"network ACL %q is associated with a subnet", id)
	}

	m.networkACLs.Delete(id)

	return nil
}

// DescribeNetworkACLs returns network ACLs matching the given IDs, or all if
// empty, each with its subnet associations attached.
func (m *Mock) DescribeNetworkACLs(_ context.Context, ids []string) ([]driver.NetworkACL, error) {
	acls := describeResources(m.networkACLs, ids, toNetworkACLInfo)
	for i := range acls {
		acls[i].Associations = m.aclAssociationsFor(acls[i].ID)
	}

	return acls, nil
}

// AddNetworkACLRule adds a rule to the specified network ACL, keeping rules sorted by number.
func (m *Mock) AddNetworkACLRule(_ context.Context, aclID string, rule *driver.NetworkACLRule) error {
	acl, ok := m.networkACLs.Get(aclID)
	if !ok {
		return errors.Newf(errors.NotFound, "network ACL %q not found", aclID)
	}

	// A rule number is unique per direction. Real EC2 rejects a second entry at
	// the same (ruleNumber, egress) with NetworkAclEntryAlreadyExists; replacing
	// an entry is a distinct action (ReplaceNetworkAclEntry).
	for _, r := range acl.Rules {
		if r.RuleNumber == rule.RuleNumber && r.Egress == rule.Egress {
			return errors.Newf(errors.AlreadyExists,
				"entry with rule number %d already exists in network ACL %q", rule.RuleNumber, aclID)
		}
	}

	acl.Rules = append(acl.Rules, *rule)
	sort.Slice(acl.Rules, func(i, j int) bool {
		return acl.Rules[i].RuleNumber < acl.Rules[j].RuleNumber
	})

	return nil
}

// RemoveNetworkACLRule removes a rule from the specified network ACL by rule number and direction.
func (m *Mock) RemoveNetworkACLRule(_ context.Context, aclID string, ruleNumber int, egress bool) error {
	acl, ok := m.networkACLs.Get(aclID)
	if !ok {
		return errors.Newf(errors.NotFound, "network ACL %q not found", aclID)
	}

	// The reserved catch-all '*' entry (rule 32767) is immutable: real EC2
	// refuses to delete or replace it, preserving deny-by-default.
	if ruleNumber == defaultDenyRuleNumber {
		return errors.Newf(errors.InvalidArgument,
			"the network ACL entry %d is the reserved default rule and cannot be deleted or modified", ruleNumber)
	}

	for i, r := range acl.Rules {
		if r.RuleNumber == ruleNumber && r.Egress == egress {
			acl.Rules = append(acl.Rules[:i], acl.Rules[i+1:]...)
			return nil
		}
	}

	return errors.Newf(errors.NotFound, "rule %d not found in network ACL %q", ruleNumber, aclID)
}

func toNetworkACLInfo(acl *networkACLData) driver.NetworkACL {
	rules := make([]driver.NetworkACLRule, len(acl.Rules))
	copy(rules, acl.Rules)

	return driver.NetworkACL{
		ID:        acl.ID,
		VPCID:     acl.VPCID,
		Rules:     rules,
		Tags:      copyTags(acl.Tags),
		IsDefault: acl.IsDefault,
	}
}

// aclAssocData binds a subnet to a network ACL. Replacing the ACL for a subnet
// deletes the old binding and creates a new one with a fresh id.
type aclAssocData struct {
	ID           string
	NetworkACLID string
	SubnetID     string
}

// createDefaultNetworkACL creates the default network ACL EC2 auto-creates with
// every VPC (allow-all rules, IsDefault). New subnets associate with it.
func (m *Mock) createDefaultNetworkACL(vpcID string) {
	id := idgen.GenerateID("acl-")
	m.networkACLs.Set(id, &networkACLData{
		ID:        id,
		VPCID:     vpcID,
		Rules:     defaultACLRules(),
		Tags:      map[string]string{},
		IsDefault: true,
	})
}

// deleteDefaultNetworkACL removes the VPC's default ACL and its associations
// when the VPC is torn down.
func (m *Mock) deleteDefaultNetworkACL(vpcID string) {
	for aclID, acl := range m.networkACLs.All() {
		if acl.VPCID != vpcID || !acl.IsDefault {
			continue
		}

		m.networkACLs.Delete(aclID)

		for assocID, a := range m.aclAssocs.All() {
			if a.NetworkACLID == aclID {
				m.aclAssocs.Delete(assocID)
			}
		}
	}
}

// associateDefaultNetworkACL binds a new subnet to its VPC's default ACL.
func (m *Mock) associateDefaultNetworkACL(vpcID, subnetID string) {
	var aclID string

	for _, acl := range m.networkACLs.All() {
		if acl.VPCID == vpcID && acl.IsDefault {
			aclID = acl.ID
			break
		}
	}

	if aclID == "" {
		return
	}

	id := idgen.GenerateID("aclassoc-")
	m.aclAssocs.Set(id, &aclAssocData{ID: id, NetworkACLID: aclID, SubnetID: subnetID})
}

// aclHasAssociation reports whether any subnet is associated with the ACL.
func (m *Mock) aclHasAssociation(aclID string) bool {
	for _, a := range m.aclAssocs.All() {
		if a.NetworkACLID == aclID {
			return true
		}
	}

	return false
}

// aclAssociationsFor returns the subnet associations of the ACL, sorted by id.
func (m *Mock) aclAssociationsFor(aclID string) []driver.NetworkACLAssociation {
	var out []driver.NetworkACLAssociation

	for _, a := range m.aclAssocs.All() {
		if a.NetworkACLID == aclID {
			out = append(out, driver.NetworkACLAssociation{ID: a.ID, NetworkACLID: a.NetworkACLID, SubnetID: a.SubnetID})
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })

	return out
}

// ReplaceNetworkACLAssociation moves the subnet in associationID onto newACLID,
// returning the new association (a fresh id). It is idempotent, matching real
// EC2: re-associating to the same ACL still yields a new association id.
func (m *Mock) ReplaceNetworkACLAssociation(
	_ context.Context, associationID, newACLID string,
) (*driver.NetworkACLAssociation, error) {
	assoc, ok := m.aclAssocs.Get(associationID)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "network ACL association %q not found", associationID)
	}

	acl, ok := m.networkACLs.Get(newACLID)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "network ACL %q not found", newACLID)
	}

	// Real EC2 requires the network ACL and the subnet to be in the same VPC.
	if sub, ok := m.subnets.Get(assoc.SubnetID); ok && sub.VPCID != acl.VPCID {
		return nil, errors.Newf(errors.InvalidArgument,
			"the network ACL %q and subnet %q are not in the same VPC", newACLID, assoc.SubnetID)
	}

	m.aclAssocs.Delete(associationID)

	id := idgen.GenerateID("aclassoc-")
	m.aclAssocs.Set(id, &aclAssocData{ID: id, NetworkACLID: newACLID, SubnetID: assoc.SubnetID})

	return &driver.NetworkACLAssociation{ID: id, NetworkACLID: newACLID, SubnetID: assoc.SubnetID}, nil
}
