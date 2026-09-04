package vnet

import (
	"context"
	"sort"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// Default ACL rule constants.
const (
	defaultACLRuleNumber = 100
	allTrafficProtocol   = "-1"
	allTrafficCIDR       = "0.0.0.0/0"
	actionAllow          = "allow"
)

type networkACLData struct {
	ID        string
	VPCID     string
	Rules     []driver.NetworkACLRule
	Tags      map[string]string
	IsDefault bool
}

// CreateNetworkACL creates a network ACL for the specified VNet.
func (m *Mock) CreateNetworkACL(
	_ context.Context, vpcID string, tags map[string]string,
) (*driver.NetworkACL, error) {
	if vpcID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "VNet ID is required")
	}

	if !m.vpcs.Has(vpcID) {
		return nil, cerrors.Newf(cerrors.NotFound, "vnet %q not found", vpcID)
	}

	id := idgen.GenerateID("acl-")
	acl := &networkACLData{
		ID:        id,
		VPCID:     vpcID,
		Rules:     defaultACLRules(),
		Tags:      copyTags(tags),
		IsDefault: false,
	}
	m.networkACLs.Set(id, acl)

	info := toNetworkACLInfo(acl)

	return &info, nil
}

// defaultACLRules returns the default allow-all rules for a new ACL.
func defaultACLRules() []driver.NetworkACLRule {
	return []driver.NetworkACLRule{
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
	}
}

// DeleteNetworkACL deletes the network ACL with the given ID.
func (m *Mock) DeleteNetworkACL(_ context.Context, id string) error {
	acl, ok := m.networkACLs.Get(id)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "network ACL %q not found", id)
	}

	if acl.IsDefault {
		return cerrors.Newf(cerrors.FailedPrecondition, "cannot delete default network ACL %q", id)
	}

	m.networkACLs.Delete(id)

	return nil
}

// DescribeNetworkACLs returns network ACLs matching the given IDs, or all if empty.
func (m *Mock) DescribeNetworkACLs(_ context.Context, ids []string) ([]driver.NetworkACL, error) {
	return describeResources(m.networkACLs, ids, toNetworkACLInfo), nil
}

// AddNetworkACLRule adds a rule to the specified network ACL, keeping rules
// sorted. The rule slice is rebuilt fresh onto a copy of the ACL under the
// store lock (copy-on-write), never mutating the shared pointer in place.
func (m *Mock) AddNetworkACLRule(_ context.Context, aclID string, rule *driver.NetworkACLRule) error {
	if !m.networkACLs.Update(aclID, func(acl *networkACLData) *networkACLData {
		rules := append(append([]driver.NetworkACLRule(nil), acl.Rules...), *rule)
		sort.Slice(rules, func(i, j int) bool {
			return rules[i].RuleNumber < rules[j].RuleNumber
		})

		cp := *acl
		cp.Rules = rules

		return &cp
	}) {
		return cerrors.Newf(cerrors.NotFound, "network ACL %q not found", aclID)
	}

	return nil
}

// RemoveNetworkACLRule removes a rule by rule number and direction, copy-on-write.
func (m *Mock) RemoveNetworkACLRule(
	_ context.Context, aclID string, ruleNumber int, egress bool,
) error {
	removed := false

	if !m.networkACLs.Update(aclID, func(acl *networkACLData) *networkACLData {
		idx := -1

		for i := range acl.Rules {
			if acl.Rules[i].RuleNumber == ruleNumber && acl.Rules[i].Egress == egress {
				idx = i
				break
			}
		}

		if idx == -1 {
			return acl
		}

		removed = true
		next := make([]driver.NetworkACLRule, 0, len(acl.Rules)-1)
		next = append(next, acl.Rules[:idx]...)
		next = append(next, acl.Rules[idx+1:]...)

		cp := *acl
		cp.Rules = next

		return &cp
	}) {
		return cerrors.Newf(cerrors.NotFound, "network ACL %q not found", aclID)
	}

	if !removed {
		return cerrors.Newf(cerrors.NotFound, "rule %d not found in network ACL %q", ruleNumber, aclID)
	}

	return nil
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
