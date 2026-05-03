package eks

import (
	"math"
	"net/http"

	eksdriver "github.com/stackshy/cloudemu/providers/aws/eks/driver"
)

// safeInt32 narrows an int to int32, clamping at math.MaxInt32 / math.MinInt32.
// EKS scaling and disk-size fields are int32 on the wire; the driver uses int
// for ergonomics. A direct int->int32 cast would trip gosec G115; explicit
// clamping makes the truncation behavior deliberate.
func safeInt32(v int) int32 {
	switch {
	case v > math.MaxInt32:
		return math.MaxInt32
	case v < math.MinInt32:
		return math.MinInt32
	default:
		return int32(v)
	}
}

// Cluster operations.

func (h *Handler) createCluster(w http.ResponseWriter, r *http.Request) {
	var body createClusterRequest
	if !decodeJSON(w, r, &body) {
		return
	}

	cfg := eksdriver.ClusterConfig{
		Name:    body.Name,
		Version: body.Version,
		RoleArn: body.RoleArn,
		Tags:    body.Tags,
	}

	if body.ResourcesVpcConfig != nil {
		cfg.VPCConfig = vpcRequestToDriver(body.ResourcesVpcConfig)
	}

	cluster, err := h.eks.CreateCluster(r.Context(), cfg)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, clusterEnvelope{Cluster: toClusterJSON(cluster)})
}

func (h *Handler) describeCluster(w http.ResponseWriter, r *http.Request, name string) {
	cluster, err := h.eks.DescribeCluster(r.Context(), name)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, clusterEnvelope{Cluster: toClusterJSON(cluster)})
}

func (h *Handler) listClusters(w http.ResponseWriter, r *http.Request) {
	names, err := h.eks.ListClusters(r.Context())
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, listClustersResponse{Clusters: names})
}

func (h *Handler) updateClusterConfig(w http.ResponseWriter, r *http.Request, name string) {
	var body updateClusterConfigRequest
	if !decodeJSON(w, r, &body) {
		return
	}

	var vpc eksdriver.VPCConfig
	if body.ResourcesVpcConfig != nil {
		vpc = vpcRequestToDriver(body.ResourcesVpcConfig)
	}

	upd, err := h.eks.UpdateClusterConfig(r.Context(), name, vpc, body.Tags)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, updateEnvelope{Update: toUpdateJSON(upd)})
}

func (h *Handler) updateClusterVersion(w http.ResponseWriter, r *http.Request, name string) {
	var body updateClusterVersionRequest
	if !decodeJSON(w, r, &body) {
		return
	}

	upd, err := h.eks.UpdateClusterVersion(r.Context(), name, body.Version)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, updateEnvelope{Update: toUpdateJSON(upd)})
}

func (h *Handler) deleteCluster(w http.ResponseWriter, r *http.Request, name string) {
	cluster, err := h.eks.DeleteCluster(r.Context(), name)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, clusterEnvelope{Cluster: toClusterJSON(cluster)})
}

// Nodegroup operations.

func (h *Handler) createNodegroup(w http.ResponseWriter, r *http.Request, clusterName string) {
	var body createNodegroupRequest
	if !decodeJSON(w, r, &body) {
		return
	}

	cfg := eksdriver.NodegroupConfig{
		ClusterName:    clusterName,
		NodegroupName:  body.NodegroupName,
		NodeRole:       body.NodeRole,
		Subnets:        body.Subnets,
		InstanceTypes:  body.InstanceTypes,
		AmiType:        body.AmiType,
		CapacityType:   body.CapacityType,
		Version:        body.Version,
		ReleaseVersion: body.ReleaseVersion,
		Labels:         body.Labels,
		Tags:           body.Tags,
	}

	if body.DiskSize != nil {
		cfg.DiskSize = int(*body.DiskSize)
	}

	if body.ScalingConfig != nil {
		cfg.ScalingConfig = scalingFromJSON(body.ScalingConfig)
	}

	ng, err := h.eks.CreateNodegroup(r.Context(), cfg)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, nodegroupEnvelope{Nodegroup: toNodegroupJSON(ng)})
}

func (h *Handler) describeNodegroup(w http.ResponseWriter, r *http.Request, clusterName, ngName string) {
	ng, err := h.eks.DescribeNodegroup(r.Context(), clusterName, ngName)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, nodegroupEnvelope{Nodegroup: toNodegroupJSON(ng)})
}

func (h *Handler) listNodegroups(w http.ResponseWriter, r *http.Request, clusterName string) {
	names, err := h.eks.ListNodegroups(r.Context(), clusterName)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, listNodegroupsResponse{Nodegroups: names})
}

func (h *Handler) updateNodegroupConfig(w http.ResponseWriter, r *http.Request, clusterName, ngName string) {
	var body updateNodegroupConfigRequest
	if !decodeJSON(w, r, &body) {
		return
	}

	var (
		scaling *eksdriver.NodegroupScalingConfig
		labels  map[string]string
	)

	if body.ScalingConfig != nil {
		s := scalingFromJSON(body.ScalingConfig)
		scaling = &s
	}

	if body.Labels != nil {
		labels = body.Labels.AddOrUpdateLabels
	}

	upd, err := h.eks.UpdateNodegroupConfig(r.Context(), clusterName, ngName, scaling, labels)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, updateEnvelope{Update: toUpdateJSON(upd)})
}

func (h *Handler) updateNodegroupVersion(w http.ResponseWriter, r *http.Request, clusterName, ngName string) {
	var body updateNodegroupVersionRequest
	if !decodeJSON(w, r, &body) {
		return
	}

	upd, err := h.eks.UpdateNodegroupVersion(r.Context(), clusterName, ngName, body.Version, body.ReleaseVersion)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, updateEnvelope{Update: toUpdateJSON(upd)})
}

func (h *Handler) deleteNodegroup(w http.ResponseWriter, r *http.Request, clusterName, ngName string) {
	ng, err := h.eks.DeleteNodegroup(r.Context(), clusterName, ngName)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, nodegroupEnvelope{Nodegroup: toNodegroupJSON(ng)})
}

// Fargate profile operations.

func (h *Handler) createFargateProfile(w http.ResponseWriter, r *http.Request, clusterName string) {
	var body createFargateProfileRequest
	if !decodeJSON(w, r, &body) {
		return
	}

	cfg := eksdriver.FargateProfileConfig{
		ClusterName:        clusterName,
		FargateProfileName: body.FargateProfileName,
		PodExecutionRole:   body.PodExecutionRoleArn,
		Subnets:            body.Subnets,
		Tags:               body.Tags,
	}

	for _, s := range body.Selectors {
		cfg.Selectors = append(cfg.Selectors, eksdriver.FargateProfileSelector{
			Namespace: s.Namespace,
			Labels:    s.Labels,
		})
	}

	fp, err := h.eks.CreateFargateProfile(r.Context(), cfg)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, fargateProfileEnvelope{FargateProfile: toFargateProfileJSON(fp)})
}

func (h *Handler) describeFargateProfile(w http.ResponseWriter, r *http.Request, clusterName, profileName string) {
	fp, err := h.eks.DescribeFargateProfile(r.Context(), clusterName, profileName)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, fargateProfileEnvelope{FargateProfile: toFargateProfileJSON(fp)})
}

func (h *Handler) listFargateProfiles(w http.ResponseWriter, r *http.Request, clusterName string) {
	names, err := h.eks.ListFargateProfiles(r.Context(), clusterName)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, listFargateProfilesResponse{FargateProfileNames: names})
}

func (h *Handler) deleteFargateProfile(w http.ResponseWriter, r *http.Request, clusterName, profileName string) {
	fp, err := h.eks.DeleteFargateProfile(r.Context(), clusterName, profileName)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, fargateProfileEnvelope{FargateProfile: toFargateProfileJSON(fp)})
}

// Add-on operations.

func (h *Handler) createAddon(w http.ResponseWriter, r *http.Request, clusterName string) {
	var body createAddonRequest
	if !decodeJSON(w, r, &body) {
		return
	}

	cfg := eksdriver.AddonConfig{
		ClusterName:           clusterName,
		AddonName:             body.AddonName,
		AddonVersion:          body.AddonVersion,
		ServiceAccountRoleArn: body.ServiceAccountRoleArn,
		ConfigurationValues:   body.ConfigurationValues,
		Tags:                  body.Tags,
	}

	ad, err := h.eks.CreateAddon(r.Context(), cfg)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, addonEnvelope{Addon: toAddonJSON(ad)})
}

func (h *Handler) describeAddon(w http.ResponseWriter, r *http.Request, clusterName, addonName string) {
	ad, err := h.eks.DescribeAddon(r.Context(), clusterName, addonName)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, addonEnvelope{Addon: toAddonJSON(ad)})
}

func (h *Handler) listAddons(w http.ResponseWriter, r *http.Request, clusterName string) {
	names, err := h.eks.ListAddons(r.Context(), clusterName)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, listAddonsResponse{Addons: names})
}

func (h *Handler) updateAddon(w http.ResponseWriter, r *http.Request, clusterName, addonName string) {
	var body updateAddonRequest
	if !decodeJSON(w, r, &body) {
		return
	}

	cfg := eksdriver.AddonConfig{
		ClusterName:           clusterName,
		AddonName:             addonName,
		AddonVersion:          body.AddonVersion,
		ServiceAccountRoleArn: body.ServiceAccountRoleArn,
		ConfigurationValues:   body.ConfigurationValues,
	}

	upd, err := h.eks.UpdateAddon(r.Context(), cfg)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, updateEnvelope{Update: toUpdateJSON(upd)})
}

func (h *Handler) deleteAddon(w http.ResponseWriter, r *http.Request, clusterName, addonName string) {
	ad, err := h.eks.DeleteAddon(r.Context(), clusterName, addonName)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, addonEnvelope{Addon: toAddonJSON(ad)})
}

// Conversion helpers.

func vpcRequestToDriver(v *vpcConfigRequest) eksdriver.VPCConfig {
	out := eksdriver.VPCConfig{
		SubnetIDs:         v.SubnetIDs,
		SecurityGroupIDs:  v.SecurityGroupIDs,
		PublicAccessCidrs: v.PublicAccessCidrs,
	}

	if v.EndpointPublicAccess != nil {
		out.EndpointPublicAccess = *v.EndpointPublicAccess
	}

	if v.EndpointPrivateAccess != nil {
		out.EndpointPrivateAccess = *v.EndpointPrivateAccess
	}

	return out
}

func scalingFromJSON(s *nodegroupScalingConfigJSON) eksdriver.NodegroupScalingConfig {
	out := eksdriver.NodegroupScalingConfig{}

	if s.MinSize != nil {
		out.MinSize = int(*s.MinSize)
	}

	if s.MaxSize != nil {
		out.MaxSize = int(*s.MaxSize)
	}

	if s.DesiredSize != nil {
		out.DesiredSize = int(*s.DesiredSize)
	}

	return out
}

func toClusterJSON(c *eksdriver.Cluster) clusterJSON {
	out := clusterJSON{
		Name:            c.Name,
		Arn:             c.ARN,
		CreatedAt:       epochSeconds(c.CreatedAt),
		Version:         c.Version,
		Endpoint:        c.Endpoint,
		RoleArn:         c.RoleArn,
		Status:          c.Status,
		PlatformVersion: c.PlatformVersion,
		Tags:            c.Tags,
		ResourcesVpcConfig: &vpcConfigResponse{
			SubnetIDs:             c.VPCConfig.SubnetIDs,
			SecurityGroupIDs:      c.VPCConfig.SecurityGroupIDs,
			EndpointPublicAccess:  c.VPCConfig.EndpointPublicAccess,
			EndpointPrivateAccess: c.VPCConfig.EndpointPrivateAccess,
			PublicAccessCidrs:     c.VPCConfig.PublicAccessCidrs,
		},
	}

	if c.CertificateAuthority != "" {
		out.CertificateAuthority = &certificate{Data: c.CertificateAuthority}
	}

	return out
}

func toNodegroupJSON(n *eksdriver.Nodegroup) nodegroupJSON {
	minSize := safeInt32(n.ScalingConfig.MinSize)
	maxSize := safeInt32(n.ScalingConfig.MaxSize)
	desired := safeInt32(n.ScalingConfig.DesiredSize)
	disk := safeInt32(n.DiskSize)

	out := nodegroupJSON{
		NodegroupName:  n.NodegroupName,
		NodegroupArn:   n.ARN,
		ClusterName:    n.ClusterName,
		Version:        n.Version,
		ReleaseVersion: n.ReleaseVersion,
		CreatedAt:      epochSeconds(n.CreatedAt),
		ModifiedAt:     epochSeconds(n.CreatedAt),
		Status:         n.Status,
		CapacityType:   n.CapacityType,
		ScalingConfig: &nodegroupScalingConfigJSON{
			MinSize:     &minSize,
			MaxSize:     &maxSize,
			DesiredSize: &desired,
		},
		InstanceTypes: n.InstanceTypes,
		Subnets:       n.Subnets,
		AmiType:       n.AmiType,
		NodeRole:      n.NodeRole,
		Labels:        n.Labels,
		Tags:          n.Tags,
	}

	if n.DiskSize > 0 {
		out.DiskSize = &disk
	}

	return out
}

func toFargateProfileJSON(fp *eksdriver.FargateProfile) fargateProfileJSON {
	out := fargateProfileJSON{
		FargateProfileName:  fp.FargateProfileName,
		FargateProfileArn:   fp.ARN,
		ClusterName:         fp.ClusterName,
		CreatedAt:           epochSeconds(fp.CreatedAt),
		PodExecutionRoleArn: fp.PodExecutionRole,
		Subnets:             fp.Subnets,
		Status:              fp.Status,
		Tags:                fp.Tags,
	}

	for _, s := range fp.Selectors {
		out.Selectors = append(out.Selectors, fargateProfileSelectorJSON{
			Namespace: s.Namespace,
			Labels:    s.Labels,
		})
	}

	return out
}

func toAddonJSON(ad *eksdriver.Addon) addonJSON {
	return addonJSON{
		AddonName:             ad.AddonName,
		ClusterName:           ad.ClusterName,
		Status:                ad.Status,
		AddonVersion:          ad.AddonVersion,
		AddonArn:              ad.ARN,
		CreatedAt:             epochSeconds(ad.CreatedAt),
		ModifiedAt:            epochSeconds(ad.ModifiedAt),
		ServiceAccountRoleArn: ad.ServiceAccountRoleArn,
		ConfigurationValues:   ad.ConfigurationValues,
		Tags:                  ad.Tags,
	}
}

func toUpdateJSON(u *eksdriver.ClusterUpdate) updateJSON {
	return updateJSON{
		ID:        u.ID,
		Type:      u.Type,
		Status:    u.Status,
		CreatedAt: epochSeconds(u.CreatedAt),
	}
}
