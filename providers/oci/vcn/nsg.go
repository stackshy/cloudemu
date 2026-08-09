package vcn

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

type nsgData struct {
	ID           string
	Name         string
	Description  string
	VCNID        string
	IngressRules []driver.SecurityRule
	EgressRules  []driver.SecurityRule
	Tags         map[string]string
}

// CreateSecurityGroup creates a network security group in a VCN. An NSG is
// empty on creation; OCI's default-allow rules live on the security list.
func (m *Mock) CreateSecurityGroup(_ context.Context, cfg driver.SecurityGroupConfig) (*driver.SecurityGroupInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if cfg.VPCID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "VCN OCID is required")
	}

	if !m.vcns.Has(cfg.VPCID) {
		return nil, cerrors.Newf(cerrors.NotFound, "VCN %q not found", cfg.VPCID)
	}

	id := m.newOCID(typeNSG)
	nsg := &nsgData{
		ID:           id,
		Name:         cfg.Name,
		Description:  cfg.Description,
		VCNID:        cfg.VPCID,
		IngressRules: []driver.SecurityRule{},
		EgressRules:  []driver.SecurityRule{},
		Tags:         copyTags(cfg.Tags),
	}

	m.nsgs.Set(id, nsg)
	m.record(id)

	info := toNSGInfo(nsg)

	return &info, nil
}

// DeleteSecurityGroup deletes a network security group, refusing while VNICs
// are still members of it.
func (m *Mock) DeleteSecurityGroup(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.nsgs.Has(id) {
		return nsgNotFound(id)
	}

	for _, v := range m.vnics.All() {
		for _, member := range v.NSGIDs {
			if member == id {
				return cerrors.Newf(cerrors.FailedPrecondition,
					"network security group %q still has VNICs in it", id)
			}
		}
	}

	m.nsgs.Delete(id)
	m.forget(id)

	return nil
}

// DescribeSecurityGroups returns NSGs matching the given OCIDs, or all if empty.
func (m *Mock) DescribeSecurityGroups(_ context.Context, ids []string) ([]driver.SecurityGroupInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return describeResources(m.nsgs, ids, toNSGInfo), nil
}

// AddIngressRule adds an ingress security rule to an NSG.
func (m *Mock) AddIngressRule(_ context.Context, groupID string, rule driver.SecurityRule) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return mutate(m.nsgs, groupID, nsgNotFound(groupID), func(nsg *nsgData) error {
		next, err := addRule(nsg.IngressRules, rule, "ingress", groupID)
		if err != nil {
			return err
		}

		nsg.IngressRules = next

		return nil
	})
}

// AddEgressRule adds an egress security rule to an NSG.
func (m *Mock) AddEgressRule(_ context.Context, groupID string, rule driver.SecurityRule) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return mutate(m.nsgs, groupID, nsgNotFound(groupID), func(nsg *nsgData) error {
		next, err := addRule(nsg.EgressRules, rule, "egress", groupID)
		if err != nil {
			return err
		}

		nsg.EgressRules = next

		return nil
	})
}

// RemoveIngressRule removes a matching ingress rule from an NSG.
func (m *Mock) RemoveIngressRule(_ context.Context, groupID string, rule driver.SecurityRule) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return mutate(m.nsgs, groupID, nsgNotFound(groupID), func(nsg *nsgData) error {
		remaining, found := dropRule(nsg.IngressRules, rule)
		if !found {
			return cerrors.Newf(cerrors.NotFound, "ingress rule not found in network security group %q", groupID)
		}

		nsg.IngressRules = remaining

		return nil
	})
}

// RemoveEgressRule removes a matching egress rule from an NSG.
func (m *Mock) RemoveEgressRule(_ context.Context, groupID string, rule driver.SecurityRule) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return mutate(m.nsgs, groupID, nsgNotFound(groupID), func(nsg *nsgData) error {
		remaining, found := dropRule(nsg.EgressRules, rule)
		if !found {
			return cerrors.Newf(cerrors.NotFound, "egress rule not found in network security group %q", groupID)
		}

		nsg.EgressRules = remaining

		return nil
	})
}

// UpdateSecurityGroupTags merges freeform tags into the NSG's tag map.
func (m *Mock) UpdateSecurityGroupTags(_ context.Context, id string, tags map[string]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.nsgs.Update(id, func(nsg *nsgData) *nsgData {
		nsg.Tags = mergeTagMap(nsg.Tags, tags)
		return nsg
	}) {
		return nsgNotFound(id)
	}

	return nil
}

// RemoveSecurityGroupTags removes the given freeform tag keys from an NSG.
func (m *Mock) RemoveSecurityGroupTags(_ context.Context, id string, keys []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.nsgs.Update(id, func(nsg *nsgData) *nsgData {
		nsg.Tags = removeTagMapKeys(nsg.Tags, keys)
		return nsg
	}) {
		return nsgNotFound(id)
	}

	return nil
}

func nsgNotFound(id string) error {
	return cerrors.Newf(cerrors.NotFound, "network security group %q not found", id)
}

// addRule appends a rule, refusing an exact duplicate. A portable rule carries
// no identity of its own, so the wire layer addresses one by its contents and
// two identical rules would share a handle.
func addRule(
	rules []driver.SecurityRule, rule driver.SecurityRule, direction, groupID string,
) ([]driver.SecurityRule, error) {
	for _, r := range rules {
		if r == rule {
			return nil, cerrors.Newf(cerrors.AlreadyExists,
				"%s rule already exists in network security group %q", direction, groupID)
		}
	}

	return appendItem(rules, rule), nil
}

// dropRule removes the first rule equal to want, reporting whether it was there.
func dropRule(rules []driver.SecurityRule, want driver.SecurityRule) ([]driver.SecurityRule, bool) {
	for i, r := range rules {
		if r == want {
			return removeAt(rules, i), true
		}
	}

	return rules, false
}

func toNSGInfo(nsg *nsgData) driver.SecurityGroupInfo {
	ingress := make([]driver.SecurityRule, len(nsg.IngressRules))
	copy(ingress, nsg.IngressRules)

	egress := make([]driver.SecurityRule, len(nsg.EgressRules))
	copy(egress, nsg.EgressRules)

	return driver.SecurityGroupInfo{
		ID:           nsg.ID,
		Name:         nsg.Name,
		Description:  nsg.Description,
		VPCID:        nsg.VCNID,
		IngressRules: ingress,
		EgressRules:  egress,
		Tags:         copyTags(nsg.Tags),
	}
}
