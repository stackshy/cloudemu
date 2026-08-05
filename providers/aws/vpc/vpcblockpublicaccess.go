package vpc

import (
	"context"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// DescribeVPCBlockPublicAccessOptions returns the account/region BPA options,
// synthesizing the "off" default when they've never been modified.
func (m *Mock) DescribeVPCBlockPublicAccessOptions(
	_ context.Context,
) (*driver.VPCBlockPublicAccessOptions, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.vpcBPAOptions == nil {
		return &driver.VPCBlockPublicAccessOptions{
			AWSAccountID:             m.opts.AccountID,
			AWSRegion:                m.opts.Region,
			State:                    "default-state",
			InternetGatewayBlockMode: "off",
			ExclusionsAllowed:        "allowed",
			ManagedBy:                "account",
		}, nil
	}

	out := *m.vpcBPAOptions

	return &out, nil
}

// ModifyVPCBlockPublicAccessOptions sets the internet-gateway block mode.
func (m *Mock) ModifyVPCBlockPublicAccessOptions(
	_ context.Context, internetGatewayBlockMode string,
) (*driver.VPCBlockPublicAccessOptions, error) {
	if internetGatewayBlockMode == "" {
		return nil, errors.Newf(errors.InvalidArgument, "InternetGatewayBlockMode is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.vpcBPAOptions = &driver.VPCBlockPublicAccessOptions{
		AWSAccountID:             m.opts.AccountID,
		AWSRegion:                m.opts.Region,
		State:                    "update-complete",
		InternetGatewayBlockMode: internetGatewayBlockMode,
		ExclusionsAllowed:        "allowed",
		ManagedBy:                "account",
		LastUpdateTimestamp:      time.Now().UTC(),
	}

	out := *m.vpcBPAOptions

	return &out, nil
}

// CreateVPCBlockPublicAccessExclusion exempts a VPC or subnet from BPA.
func (m *Mock) CreateVPCBlockPublicAccessExclusion(
	_ context.Context, cfg driver.VPCBlockPublicAccessExclusionConfig,
) (*driver.VPCBlockPublicAccessExclusion, error) {
	if cfg.VPCID == "" && cfg.SubnetID == "" {
		return nil, errors.Newf(errors.InvalidArgument, "one of VpcId or SubnetId is required")
	}

	if cfg.InternetGatewayExclusionMode == "" {
		return nil, errors.Newf(errors.InvalidArgument, "InternetGatewayExclusionMode is required")
	}

	resourceARN, err := m.exclusionResourceARN(cfg)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	id := idgen.GenerateID("vpcbpa-exclude-")
	e := &driver.VPCBlockPublicAccessExclusion{
		ExclusionID:                  id,
		InternetGatewayExclusionMode: cfg.InternetGatewayExclusionMode,
		ResourceARN:                  resourceARN,
		State:                        "create-complete",
		CreationTimestamp:            now,
		LastUpdateTimestamp:          now,
		Tags:                         copyTags(cfg.Tags),
	}
	m.vpcBPAExclusions.Set(id, e)

	out := cloneBPAExclusion(e)

	return &out, nil
}

// exclusionResourceARN validates the referenced VPC/subnet exists and builds its
// ARN. Caller must not hold mu (memstore has its own locking).
func (m *Mock) exclusionResourceARN(cfg driver.VPCBlockPublicAccessExclusionConfig) (string, error) {
	if cfg.SubnetID != "" {
		if !m.subnets.Has(cfg.SubnetID) {
			return "", errors.Newf(errors.InvalidArgument, "subnet %q not found", cfg.SubnetID)
		}

		return m.insightsARN("subnet", cfg.SubnetID), nil
	}

	if !m.vpcs.Has(cfg.VPCID) {
		return "", errors.Newf(errors.InvalidArgument, "vpc %q not found", cfg.VPCID)
	}

	return m.insightsARN("vpc", cfg.VPCID), nil
}

// ModifyVPCBlockPublicAccessExclusion changes an exclusion's mode.
func (m *Mock) ModifyVPCBlockPublicAccessExclusion(
	_ context.Context, id, internetGatewayExclusionMode string,
) (*driver.VPCBlockPublicAccessExclusion, error) {
	if internetGatewayExclusionMode == "" {
		return nil, errors.Newf(errors.InvalidArgument, "InternetGatewayExclusionMode is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	e, ok := m.vpcBPAExclusions.Get(id)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "vpc block public access exclusion %q not found", id)
	}

	e.InternetGatewayExclusionMode = internetGatewayExclusionMode
	e.State = "update-complete"
	e.LastUpdateTimestamp = time.Now().UTC()

	out := cloneBPAExclusion(e)

	return &out, nil
}

// DeleteVPCBlockPublicAccessExclusion deletes an exclusion, returning its
// final state.
func (m *Mock) DeleteVPCBlockPublicAccessExclusion(
	_ context.Context, id string,
) (*driver.VPCBlockPublicAccessExclusion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	e, ok := m.vpcBPAExclusions.Get(id)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "vpc block public access exclusion %q not found", id)
	}

	e.State = "delete-complete"
	e.LastUpdateTimestamp = time.Now().UTC()
	out := cloneBPAExclusion(e)

	m.vpcBPAExclusions.Delete(id)

	return &out, nil
}

// DescribeVPCBlockPublicAccessExclusions returns exclusions matching ids.
func (m *Mock) DescribeVPCBlockPublicAccessExclusions(
	_ context.Context, ids []string,
) ([]driver.VPCBlockPublicAccessExclusion, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return describeResources(m.vpcBPAExclusions, ids, cloneBPAExclusion), nil
}

func cloneBPAExclusion(e *driver.VPCBlockPublicAccessExclusion) driver.VPCBlockPublicAccessExclusion {
	out := *e
	out.Tags = copyTags(e.Tags)

	return out
}
