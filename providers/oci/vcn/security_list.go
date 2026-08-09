package vcn

import (
	"context"
	"sort"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// Rule vocabulary shared by the default security list.
const (
	protocolAll  = "-1"
	protocolTCP  = "tcp"
	actionAllow  = "allow"
	cidrAnywhere = "0.0.0.0/0"
	portSSH      = 22
)

// A security list is OCI's subnet-attached rule collection, the closest
// equivalent of the portable network ACL. Unlike an ACL it has no deny rules,
// so every rule the mock stores is an allow and rule numbers are positional.
type securityListData struct {
	ID        string
	VCNID     string
	Rules     []driver.NetworkACLRule
	Tags      map[string]string
	IsDefault bool
}

// CreateNetworkACL creates a security list in a VCN.
func (m *Mock) CreateNetworkACL(_ context.Context, vpcID string, tags map[string]string) (*driver.NetworkACL, error) {
	if vpcID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "VCN OCID is required")
	}

	if !m.vcns.Has(vpcID) {
		return nil, cerrors.Newf(cerrors.NotFound, "VCN %q not found", vpcID)
	}

	return m.addSecurityList(vpcID, tags, false), nil
}

// newDefaultSecurityList creates the security list OCI attaches to a new VCN.
func (m *Mock) newDefaultSecurityList(v *vcnData) string {
	sl := m.addSecurityList(v.ID, nil, true)

	return sl.ID
}

// addSecurityList stores a security list carrying OCI's default rule set.
func (m *Mock) addSecurityList(vcnID string, tags map[string]string, isDefault bool) *driver.NetworkACL {
	id := m.newOCID(typeSecurityList)
	sl := &securityListData{
		ID:        id,
		VCNID:     vcnID,
		Rules:     defaultSecurityRules(),
		Tags:      copyTags(tags),
		IsDefault: isDefault,
	}

	m.securityLists.Set(id, sl)
	m.record(id)

	info := toSecurityListInfo(sl)

	return &info
}

// defaultSecurityRules is OCI's out-of-the-box pair: inbound SSH and
// unrestricted egress.
func defaultSecurityRules() []driver.NetworkACLRule {
	return []driver.NetworkACLRule{
		{
			RuleNumber: 1,
			Protocol:   protocolTCP,
			Action:     actionAllow,
			CIDR:       cidrAnywhere,
			FromPort:   portSSH,
			ToPort:     portSSH,
		},
		{
			RuleNumber: 1,
			Protocol:   protocolAll,
			Action:     actionAllow,
			CIDR:       cidrAnywhere,
			Egress:     true,
		},
	}
}

// DeleteNetworkACL deletes a security list. The VCN's default list can only
// be removed with the VCN itself.
func (m *Mock) DeleteNetworkACL(_ context.Context, id string) error {
	sl, ok := m.securityLists.Get(id)
	if !ok {
		return securityListNotFound(id)
	}

	if sl.IsDefault {
		return cerrors.Newf(cerrors.FailedPrecondition, "cannot delete default security list %q", id)
	}

	m.securityLists.Delete(id)
	m.forget(id)

	return nil
}

// DescribeNetworkACLs returns security lists matching the given OCIDs, or all
// if empty.
func (m *Mock) DescribeNetworkACLs(_ context.Context, ids []string) ([]driver.NetworkACL, error) {
	return describeResources(m.securityLists, ids, toSecurityListInfo), nil
}

// AddNetworkACLRule adds a rule to a security list, keeping rules ordered by
// rule number. A rule number is unique within its direction: it is the only
// handle RemoveNetworkACLRule addresses a rule by.
func (m *Mock) AddNetworkACLRule(_ context.Context, aclID string, rule *driver.NetworkACLRule) error {
	if rule.Action != "" && rule.Action != actionAllow {
		return cerrors.New(cerrors.InvalidArgument, "security list rules are allow-only")
	}

	added := *rule
	added.Action = actionAllow

	return mutate(m.securityLists, aclID, securityListNotFound(aclID), func(sl *securityListData) error {
		for _, r := range sl.Rules {
			if r.RuleNumber == added.RuleNumber && r.Egress == added.Egress {
				return cerrors.Newf(cerrors.AlreadyExists,
					"rule %d already exists in security list %q", added.RuleNumber, aclID)
			}
		}

		rules := appendItem(sl.Rules, added)
		sort.SliceStable(rules, func(i, j int) bool {
			return rules[i].RuleNumber < rules[j].RuleNumber
		})

		sl.Rules = rules

		return nil
	})
}

// RemoveNetworkACLRule removes a rule by rule number and direction.
func (m *Mock) RemoveNetworkACLRule(_ context.Context, aclID string, ruleNumber int, egress bool) error {
	return mutate(m.securityLists, aclID, securityListNotFound(aclID), func(sl *securityListData) error {
		for i, r := range sl.Rules {
			if r.RuleNumber == ruleNumber && r.Egress == egress {
				sl.Rules = removeAt(sl.Rules, i)

				return nil
			}
		}

		return cerrors.Newf(cerrors.NotFound, "rule %d not found in security list %q", ruleNumber, aclID)
	})
}

func securityListNotFound(id string) error {
	return cerrors.Newf(cerrors.NotFound, "security list %q not found", id)
}

// ReplaceNetworkACLRules swaps a security list's whole rule set, which is how
// OCI's UpdateSecurityList behaves.
func (m *Mock) ReplaceNetworkACLRules(_ context.Context, aclID string, rules []driver.NetworkACLRule) error {
	if !m.securityLists.Update(aclID, func(sl *securityListData) *securityListData {
		sl.Rules = append([]driver.NetworkACLRule(nil), rules...)
		return sl
	}) {
		return securityListNotFound(aclID)
	}

	return nil
}

func toSecurityListInfo(sl *securityListData) driver.NetworkACL {
	rules := make([]driver.NetworkACLRule, len(sl.Rules))
	copy(rules, sl.Rules)

	return driver.NetworkACL{
		ID:        sl.ID,
		VPCID:     sl.VCNID,
		Rules:     rules,
		Tags:      copyTags(sl.Tags),
		IsDefault: sl.IsDefault,
	}
}
