package vpc

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// ModifySecurityGroupRule replaces the permission fields (protocol, port range,
// single target, and description) of the ingress/egress rule identified by
// ruleID within groupID, preserving the rule's RuleID, egress side, and Tags.
// It backs the AWS-only ec2:ModifySecurityGroupRules action and is exposed via
// the wire layer's optional sgRuleMutator interface — Azure/GCP/OCI have no
// concept of an sgr- rule id, so this is deliberately not on the shared driver.
//
// A missing group is NotFound (mapped to InvalidGroup.NotFound); an absent
// ruleID is a NotFound naming the rule (mapped to
// InvalidSecurityGroupRuleId.NotFound).
//
//nolint:gocritic // hugeParam: updated mirrors the driver's by-value SecurityRule shape.
func (m *Mock) ModifySecurityGroupRule(_ context.Context, groupID, ruleID string, updated driver.SecurityRule) error {
	if !m.securityGroups.Has(groupID) {
		return errors.Newf(errors.NotFound, "security group %q not found", groupID)
	}

	found := false

	m.securityGroups.Update(groupID, func(sg *sgData) *sgData {
		if applyRuleUpdate(sg.IngressRules, ruleID, updated) || applyRuleUpdate(sg.EgressRules, ruleID, updated) {
			found = true
		}

		return sg
	})

	if !found {
		return errors.Newf(errors.NotFound, "security group rule %q not found", ruleID)
	}

	return nil
}

// applyRuleUpdate overwrites the permission fields of the rule in rules whose
// RuleID equals ruleID, keeping its RuleID and Tags. It reports whether a
// matching rule was found.
//
//nolint:gocritic // hugeParam: updated mirrors the driver's by-value SecurityRule shape.
func applyRuleUpdate(rules []driver.SecurityRule, ruleID string, updated driver.SecurityRule) bool {
	for i := range rules {
		if rules[i].RuleID != ruleID {
			continue
		}

		rules[i].Protocol = updated.Protocol
		rules[i].FromPort = updated.FromPort
		rules[i].ToPort = updated.ToPort
		rules[i].CIDR = updated.CIDR
		rules[i].IPv6CIDR = updated.IPv6CIDR
		rules[i].PrefixListID = updated.PrefixListID
		rules[i].ReferencedGroupID = updated.ReferencedGroupID
		rules[i].ReferencedGroupOwnerID = updated.ReferencedGroupOwnerID
		rules[i].Description = updated.Description

		return true
	}

	return false
}

// SetSecurityGroupRuleDescription sets (or clears, when description is empty)
// the free-text description of the rule identified by ruleID on the ingress or
// egress list of groupID, per egress. It backs
// ec2:UpdateSecurityGroupRuleDescriptions{Ingress,Egress} and, like
// ModifySecurityGroupRule, is exposed only through the AWS-only optional
// interface. Direction is honored: an egress rule id passed with egress=false
// misses and yields a rule-not-found error.
func (m *Mock) SetSecurityGroupRuleDescription(_ context.Context, groupID, ruleID string, egress bool, description string) error {
	if !m.securityGroups.Has(groupID) {
		return errors.Newf(errors.NotFound, "security group %q not found", groupID)
	}

	found := false

	m.securityGroups.Update(groupID, func(sg *sgData) *sgData {
		rules := sg.IngressRules
		if egress {
			rules = sg.EgressRules
		}

		for i := range rules {
			if rules[i].RuleID == ruleID {
				rules[i].Description = description
				found = true
			}
		}

		return sg
	})

	if !found {
		return errors.Newf(errors.NotFound, "security group rule %q not found", ruleID)
	}

	return nil
}
