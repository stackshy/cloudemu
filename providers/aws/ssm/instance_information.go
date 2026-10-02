package ssm

import (
	"context"
	"slices"
	"sort"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	ssmdriver "github.com/stackshy/cloudemu/v2/providers/aws/ssm/driver"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
)

var _ ssmdriver.ManagedNodes = (*Mock)(nil)

// Managed-node values the emulator reports for every EC2 instance.
const (
	pingOnline         = "Online"
	pingConnectionLost = "ConnectionLost"
	resourceEC2        = "EC2Instance"
	sourceEC2          = "AWS::EC2::Instance"
	agentVersion       = "3.3.1611.0"

	nodeKeyPingStatus    = "PingStatus"
	nodeKeyPlatformType  = "PlatformType"
	nodeKeyPlatformTypes = "PlatformTypes"
	nodeKeyResourceType  = "ResourceType"
	nodeKeyAgentVersion  = "AgentVersion"
	nodeKeyInstanceIDs   = "InstanceIds"
	nodeKeySourceIDs     = "SourceIds"
	nodeKeySourceTypes   = "SourceTypes"
	nodeKeyTagKey        = "tag-key"
)

// DescribeInstanceInformation lists the managed nodes: every EC2 instance that
// is not terminated. Filters are AND-combined and the values of one filter
// OR-combined.
func (m *Mock) DescribeInstanceInformation(
	ctx context.Context, filters []ssmdriver.InstanceInformationFilter,
) ([]ssmdriver.InstanceInformation, error) {
	for _, f := range filters {
		if err := validNodeFilter(f); err != nil {
			return nil, err
		}
	}

	out := make([]ssmdriver.InstanceInformation, 0)
	if m.instanceResolver == nil {
		return out, nil
	}

	found, err := m.instanceResolver.DescribeInstances(ctx, nil, nil,
		computedriver.DescribeInstancesOptions{IncludeManagedResources: true})
	if err != nil {
		return nil, err
	}

	now := m.opts.Clock.Now()

	for i := range found {
		inst := &found[i]
		if !managedState(inst.State) {
			continue
		}

		info := nodeInformation(inst)
		info.LastPingDateTime = now

		if slices.IndexFunc(filters, func(f ssmdriver.InstanceInformationFilter) bool { return !nodeMatches(&info, inst, f) }) < 0 {
			out = append(out, info)
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].InstanceID < out[j].InstanceID })

	return out, nil
}

// nodeInformation describes one instance as a managed node.
func nodeInformation(inst *computedriver.Instance) ssmdriver.InstanceInformation {
	info := ssmdriver.InstanceInformation{
		InstanceID: inst.ID, PingStatus: pingConnectionLost, AgentVersion: agentVersion, IsLatestVersion: true,
		PlatformType: platformLinux, PlatformName: "Amazon Linux", PlatformVersion: "2023",
		ResourceType: resourceEC2, IPAddress: inst.PrivateIP, SourceID: inst.ID, SourceType: sourceEC2,
	}

	if inst.State == stateRunning {
		info.PingStatus = pingOnline
	}

	if strings.EqualFold(inst.OSType, platformWindows) {
		info.PlatformType, info.PlatformName, info.PlatformVersion = platformWindows, "Microsoft Windows Server 2022 Datacenter", "10.0.20348"
	}

	if inst.PrivateIP != "" {
		info.ComputerName = "ip-" + strings.ReplaceAll(inst.PrivateIP, ".", "-") + ".ec2.internal"
	}

	return info
}

// validNodeFilter rejects an unknown key or an out-of-range value.
func validNodeFilter(f ssmdriver.InstanceInformationFilter) error {
	var allowed []string

	switch f.Key {
	case nodeKeyPingStatus:
		allowed = []string{pingOnline, pingConnectionLost, "Inactive"}
	case nodeKeyPlatformType, nodeKeyPlatformTypes:
		allowed = []string{platformWindows, platformLinux, platformMacOS}
	case nodeKeyResourceType:
		allowed = []string{resourceEC2, "ManagedInstance"}
	case nodeKeyInstanceIDs, nodeKeyAgentVersion, "ActivationIds", "IamRole", "AssociationStatus",
		nodeKeySourceIDs, nodeKeySourceTypes, nodeKeyTagKey:
	default:
		if !strings.HasPrefix(f.Key, "tag:") {
			return ssmErrf(excInvalidFilterKey, errors.InvalidArgument, "The filter key %s is not valid.", f.Key)
		}
	}

	for _, v := range f.Values {
		if allowed != nil && !slices.Contains(allowed, v) {
			return ssmErrf(excInvalidInstanceInfoFilter, errors.InvalidArgument,
				"The filter value %s is not valid for %s.", v, f.Key)
		}
	}

	return nil
}

// nodeMatches applies one filter. Activations, IAM roles and associations
// do not exist for an emulated EC2 node, so those filters match nothing.
func nodeMatches(info *ssmdriver.InstanceInformation, inst *computedriver.Instance, f ssmdriver.InstanceInformationFilter) bool {
	switch f.Key {
	case nodeKeyInstanceIDs, nodeKeySourceIDs:
		return slices.Contains(f.Values, info.InstanceID)
	case nodeKeyPingStatus:
		return slices.Contains(f.Values, info.PingStatus)
	case nodeKeyPlatformType, nodeKeyPlatformTypes:
		return slices.Contains(f.Values, info.PlatformType)
	case nodeKeyResourceType:
		return slices.Contains(f.Values, info.ResourceType)
	case nodeKeySourceTypes:
		return slices.Contains(f.Values, info.SourceType)
	case nodeKeyAgentVersion:
		return slices.Contains(f.Values, info.AgentVersion)
	case nodeKeyTagKey:
		return slices.ContainsFunc(f.Values, func(k string) bool { _, ok := inst.Tags[k]; return ok })
	default:
		if key, ok := strings.CutPrefix(f.Key, "tag:"); ok {
			v, has := inst.Tags[key]

			return has && slices.Contains(f.Values, v)
		}

		return false
	}
}
