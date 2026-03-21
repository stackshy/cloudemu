package vpc

import (
	"context"
	"sort"

	"github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/internal/idgen"
	"github.com/stackshy/cloudemu/networking/driver"
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
		return errors.Newf(errors.NotFound, "network ACL %q not found", id)
	}

	if acl.IsDefault {
		return errors.Newf(errors.FailedPrecondition, "cannot delete default network ACL %q", id)
	}

	m.networkACLs.Delete(id)

	return nil
}

// DescribeNetworkACLs returns network ACLs matching the given IDs, or all if empty.
func (m *Mock) DescribeNetworkACLs(_ context.Context, ids []string) ([]driver.NetworkACL, error) {
	return describeResources(m.networkACLs, ids, toNetworkACLInfo), nil
}

// AddNetworkACLRule adds a rule to the specified network ACL, keeping rules sorted by number.
func (m *Mock) AddNetworkACLRule(_ context.Context, aclID string, rule *driver.NetworkACLRule) error {
	acl, ok := m.networkACLs.Get(aclID)
	if !ok {
		return errors.Newf(errors.NotFound, "network ACL %q not found", aclID)
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
