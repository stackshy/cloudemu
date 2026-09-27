package eks

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"

	eksdriver "github.com/stackshy/cloudemu/v2/providers/aws/eks/driver"
	"github.com/stackshy/cloudemu/v2/services/kubernetes"
)

// A managed nodegroup's desired size, labels and taints become real Node objects
// in the cluster's in-memory data plane, shaped the way EKS-managed nodes look:
// ip-10-0-x-y hostnames, the eks.amazonaws.com/* and well-known topology labels,
// the nodegroup's taints with EKS effect names mapped to Kubernetes ones, and
// capacity and allocatable derived from the instance type.
//
// Lock order: every sync runs under m.mu and calls into the data plane
// (APIServer.Lookup, then ClusterState.SyncNodePool). The data plane never calls
// back into EKS, so m.mu -> APIServer.mu -> ClusterState.mu is the only order.

const (
	// labelNodegroup and friends are the labels EKS puts on managed nodes.
	labelNodegroup      = "eks.amazonaws.com/nodegroup"
	labelNodegroupImage = "eks.amazonaws.com/nodegroup-image"
	labelCapacityType   = "eks.amazonaws.com/capacityType"
	labelLaunchTmplID   = "eks.amazonaws.com/sourceLaunchTemplateId"
	labelLaunchTmplVer  = "eks.amazonaws.com/sourceLaunchTemplateVersion"
	labelInstanceType   = "node.kubernetes.io/instance-type"
	labelRegion         = "topology.kubernetes.io/region"
	labelOS             = "kubernetes.io/os"
	labelArch           = "kubernetes.io/arch"

	capacityTypeOnDemand = "ON_DEMAND"
	archARM64            = "arm64"
	usEast1              = "us-east-1"

	// kibPerGiB converts GiB to KiB.
	kibPerGiB = 1024 * 1024
	// memCapacityPercent is the share of nominal memory the kubelet reports as
	// capacity after the kernel's own reservation (about 94% on EC2).
	memCapacityPercent = 94
	// kubeReservedBaseMiB, kubeReservedPerPodMiB and evictionHardMiB are the
	// EKS AMI's kube-reserved memory formula (255Mi + 11Mi per pod) plus the
	// 100Mi hard eviction threshold.
	kubeReservedBaseMiB   = 255
	kubeReservedPerPodMiB = 11
	evictionHardMiB       = 100
	kibPerMiB             = 1024
	// ephemeralAllocPercent is the share of the root volume left allocatable.
	ephemeralAllocPercent = 90
	percent               = 100
	// instanceIDHexLen and amiIDHexLen are the hex lengths of EC2 ids.
	instanceIDHexLen = 17
	// kubeletBuildHexLen is the length of the build hash in an EKS kubelet
	// version (v1.30.4-eks-a737599).
	kubeletBuildHexLen = 7
	// maxFallbackZones caps the zones synthesized when subnets do not resolve.
	maxFallbackZones = 6
)

// instanceShape is the vCPU count, nominal memory and max pods (VPC CNI ENI
// limit) of an EC2 instance type.
type instanceShape struct {
	vcpu    int
	memGiB  int
	maxPods int
}

// defaultInstanceShape is used for instance types missing from the table.
var defaultInstanceShape = instanceShape{vcpu: 2, memGiB: 8, maxPods: 29} //nolint:gochecknoglobals // read-only fallback

// instanceShapes covers the instance types nodegroups commonly use.
//
//nolint:gochecknoglobals // read-only lookup table of published EC2 shapes
var instanceShapes = map[string]instanceShape{
	"t3.micro": {2, 1, 4}, "t3.small": {2, 2, 11}, "t3.medium": {2, 4, 17}, "t3.large": {2, 8, 35},
	"t3.xlarge": {4, 16, 58}, "t3.2xlarge": {8, 32, 58},
	"t3a.medium": {2, 4, 17}, "t3a.large": {2, 8, 35},
	"m5.large": {2, 8, 29}, "m5.xlarge": {4, 16, 58}, "m5.2xlarge": {8, 32, 58}, "m5.4xlarge": {16, 64, 234},
	"m6i.large": {2, 8, 29}, "m6i.xlarge": {4, 16, 58}, "m6i.2xlarge": {8, 32, 58},
	"m6g.large": {2, 8, 29}, "m6g.xlarge": {4, 16, 58}, "m7g.large": {2, 8, 29}, "m7g.xlarge": {4, 16, 58},
	"c5.large": {2, 4, 29}, "c5.xlarge": {4, 8, 58}, "c6g.large": {2, 4, 29}, "c6i.large": {2, 4, 29},
	"r5.large": {2, 16, 29}, "r5.xlarge": {4, 32, 58}, "r6g.large": {2, 16, 29},
	"g4dn.xlarge": {4, 16, 29},
}

// kubeletPatch returns the kubelet patch release for a Kubernetes version. It
// is derived from the supported version range (supportedMinMinor through
// catalogMaxMinor) so every version the provider accepts has one: the newest
// minor is on an early patch and each older minor has had more patch releases.
func kubeletPatch(version string) int {
	const firstPatch, patchesPerMinor = 2, 3

	minor, ok := parseMinor(version)
	if !ok || minor > catalogMaxMinor {
		return firstPatch
	}

	return firstPatch + patchesPerMinor*(catalogMaxMinor-minor)
}

// serverPatchVersion is the "1.<minor>.<patch>" the control plane reports on
// /version. It uses the same patch as the nodes' kubelet, so a cluster and its
// nodegroups at one minor agree on the patch release.
func serverPatchVersion(version string) string {
	return fmt.Sprintf("%s.%d", version, kubeletPatch(version))
}

// k8sStateLocked returns the data-plane state of a cluster, or nil when no data
// plane is wired or the cluster is not registered. Caller holds m.mu.
func (m *Mock) k8sStateLocked(clusterName string) *kubernetes.ClusterState {
	if m.k8sAPI == nil {
		return nil
	}

	uid, ok := m.k8sUIDs[clusterName]
	if !ok {
		return nil
	}

	return m.k8sAPI.Lookup(uid)
}

// syncNodegroupNodesLocked converges the nodegroup's Node objects to count
// nodes (its desired size, or 0 on delete). Caller holds m.mu.
func (m *Mock) syncNodegroupNodesLocked(ng *eksdriver.Nodegroup, count int) {
	state := m.k8sStateLocked(ng.ClusterName)
	if state == nil {
		return
	}

	state.SyncNodePool(m.nodePoolFor(ng, count))
}

// nodePoolFor builds the data-plane pool spec for a nodegroup.
func (m *Mock) nodePoolFor(ng *eksdriver.Nodegroup, count int) kubernetes.NodePool {
	region := arnRegion(ng.ARN, m.opts.Region)
	instanceType := defaultNodegroupInstanceType

	if len(ng.InstanceTypes) > 0 {
		instanceType = ng.InstanceTypes[0]
	}

	shape, ok := instanceShapes[instanceType]
	if !ok {
		shape = defaultInstanceShape
	}

	osName, arch := amiPlatform(ng.AmiType)
	capacity, allocatable := nodeResources(shape, ng.DiskSize)

	return kubernetes.NodePool{
		Name:        ng.NodegroupName,
		Count:       count,
		Labels:      m.nodegroupNodeLabels(ng, region, instanceType, osName, arch),
		Taints:      nodegroupNodeTaints(ng.Taints),
		Zones:       m.nodegroupZones(ng.Subnets, region),
		Capacity:    capacity,
		Allocatable: allocatable,
		Info:        nodegroupNodeInfo(ng, osName, arch),
		NodeName:    func(ip string) string { return nodeHostname(ip, region) },
		ProviderID: func(zone, name string) string {
			return "aws:///" + zone + "/i-" + shortHash(m.opts.AccountID+"/"+ng.ClusterName+"/"+name, instanceIDHexLen)
		},
	}
}

// nodeHostname is the EC2 private DNS name for ip: ec2.internal in us-east-1,
// <region>.compute.internal everywhere else.
func nodeHostname(ip, region string) string {
	host := "ip-" + strings.ReplaceAll(ip, ".", "-")
	if region == usEast1 {
		return host + ".ec2.internal"
	}

	return host + "." + region + ".compute.internal"
}

// nodegroupNodeLabels returns the labels every node of the nodegroup carries:
// the EKS and well-known labels, then the nodegroup's own labels.
func (*Mock) nodegroupNodeLabels(ng *eksdriver.Nodegroup, region, instanceType, osName, arch string) map[string]string {
	capacityType := ng.CapacityType
	if capacityType == "" {
		capacityType = capacityTypeOnDemand
	}

	labels := map[string]string{
		labelNodegroup:                             ng.NodegroupName,
		labelNodegroupImage:                        amiID(ng.AmiType, ng.Version, region),
		labelCapacityType:                          capacityType,
		labelInstanceType:                          instanceType,
		"beta.kubernetes.io/instance-type":         instanceType,
		labelRegion:                                region,
		"failure-domain.beta.kubernetes.io/region": region,
		labelOS:                   osName,
		"beta.kubernetes.io/os":   osName,
		labelArch:                 arch,
		"beta.kubernetes.io/arch": arch,
	}

	if lt := ng.LaunchTemplate; lt != nil && lt.ID != "" {
		labels[labelLaunchTmplID] = lt.ID
		labels[labelLaunchTmplVer] = lt.Version
	}

	for k, v := range ng.Labels {
		labels[k] = v
	}

	return labels
}

// nodegroupNodeTaints maps EKS taints to Kubernetes taints (NO_SCHEDULE ->
// NoSchedule and so on).
func nodegroupNodeTaints(in []eksdriver.Taint) []corev1.Taint {
	out := make([]corev1.Taint, 0, len(in))

	for _, t := range in {
		out = append(out, corev1.Taint{Key: t.Key, Value: t.Value, Effect: taintEffect(t.Effect)})
	}

	return out
}

// taintEffect maps an EKS taint effect to its Kubernetes name.
func taintEffect(effect string) corev1.TaintEffect {
	switch effect {
	case "NO_SCHEDULE":
		return corev1.TaintEffectNoSchedule
	case "NO_EXECUTE":
		return corev1.TaintEffectNoExecute
	case "PREFER_NO_SCHEDULE":
		return corev1.TaintEffectPreferNoSchedule
	default:
		return corev1.TaintEffect(effect)
	}
}

// nodegroupZones returns the availability zones of the nodegroup's subnets, in
// subnet order without repeats. Subnets that do not resolve fall back to one
// synthesized zone per subnet (<region>a, <region>b, ...).
func (m *Mock) nodegroupZones(subnets []string, region string) []string {
	var zones []string

	if m.subnetResolver != nil && len(subnets) > 0 {
		if infos, err := m.subnetResolver.DescribeSubnets(context.Background(), subnets); err == nil {
			seen := map[string]bool{}

			for _, s := range infos {
				if s.AvailabilityZone != "" && !seen[s.AvailabilityZone] {
					zones = append(zones, s.AvailabilityZone)
					seen[s.AvailabilityZone] = true
				}
			}
		}
	}

	if len(zones) > 0 {
		return zones
	}

	n := min(max(len(subnets), 1), maxFallbackZones)
	for i := range n {
		zones = append(zones, region+string(rune('a'+i)))
	}

	return zones
}

// amiPlatform returns the node OS and architecture for an EKS AMI type.
func amiPlatform(amiType string) (osName, arch string) {
	osName, arch = "linux", "amd64"

	if strings.HasPrefix(amiType, "WINDOWS_") {
		osName = "windows"
	}

	if strings.Contains(amiType, "ARM_64") {
		arch = archARM64
	}

	return osName, arch
}

// nodeResources derives a node's capacity and allocatable resources from its
// instance shape and root volume size, using the EKS AMI reservation formulas.
func nodeResources(shape instanceShape, diskGiB int) (capacity, allocatable kubernetes.NodeResources) {
	memKi := shape.memGiB * kibPerGiB * memCapacityPercent / percent
	reservedKi := (kubeReservedBaseMiB + kubeReservedPerPodMiB*shape.maxPods + evictionHardMiB) * kibPerMiB
	diskKi := diskGiB * kibPerGiB
	pods := fmt.Sprint(shape.maxPods)

	capacity = kubernetes.NodeResources{
		CPU: fmt.Sprint(shape.vcpu), Memory: fmt.Sprintf("%dKi", memKi),
		Pods: pods, EphemeralStorage: fmt.Sprintf("%dKi", diskKi),
	}
	allocatable = kubernetes.NodeResources{
		CPU: fmt.Sprintf("%dm", shape.vcpu*1000-kubeReservedCPUMilli(shape.vcpu)), Memory: fmt.Sprintf("%dKi", memKi-reservedKi),
		Pods: pods, EphemeralStorage: fmt.Sprintf("%dKi", diskKi*ephemeralAllocPercent/percent),
	}

	return capacity, allocatable
}

// kubeReservedCPUMilli is the EKS AMI's kube-reserved CPU: 6% of the first
// core, 1% of the second, 0.5% of cores three and four and 0.25% of the rest.
//
//nolint:mnd // the published reservation formula
func kubeReservedCPUMilli(vcpu int) int {
	switch {
	case vcpu <= 1:
		return 60
	case vcpu <= 4:
		return 70 + (vcpu-2)*5
	default:
		return 80 + (vcpu-4)*5/2
	}
}

// nodegroupNodeInfo is the node info an EKS AMI of amiType reports.
func nodegroupNodeInfo(ng *eksdriver.Nodegroup, osName, arch string) kubernetes.NodeInfo {
	kernelArch := "x86_64"
	if arch == archARM64 {
		kernelArch = "aarch64"
	}

	info := kubernetes.NodeInfo{
		KubeletVersion: fmt.Sprintf("v%s.%d-eks-%s",
			ng.Version, kubeletPatch(ng.Version), shortHash(ng.Version, kubeletBuildHexLen)),
		OperatingSystem:         osName,
		Architecture:            arch,
		OSImage:                 "Amazon Linux 2023.6.20241111",
		KernelVersion:           "6.1.115-126.197.amzn2023." + kernelArch,
		ContainerRuntimeVersion: "containerd://1.7.23",
	}

	switch {
	case strings.HasPrefix(ng.AmiType, "AL2_"):
		info.OSImage = "Amazon Linux 2"
		info.KernelVersion = "5.10.228-219.884.amzn2." + kernelArch
	case strings.HasPrefix(ng.AmiType, "BOTTLEROCKET_"):
		info.OSImage = "Bottlerocket OS 1.26.2 (aws-k8s-" + ng.Version + ")"
		info.KernelVersion = "6.1.112"
		info.ContainerRuntimeVersion = "containerd://1.7.22+bottlerocket"
	case strings.HasPrefix(ng.AmiType, "WINDOWS_"):
		info.OSImage = "Windows Server 2022 Datacenter"
		info.KernelVersion = "10.0.20348.2849"
		info.ContainerRuntimeVersion = "containerd://1.7.20"
	}

	return info
}

// amiID is a stable synthetic AMI id for an AMI type, version and region.
func amiID(amiType, version, region string) string {
	return "ami-" + shortHash(amiType+"/"+version+"/"+region, instanceIDHexLen)
}

// shortHash returns the first n hex characters of the SHA-256 of s.
func shortHash(s string, n int) string {
	sum := sha256.Sum256([]byte(s))

	return hex.EncodeToString(sum[:])[:n]
}
