package eks

import "time"

// JSON shapes for AWS EKS REST/JSON requests and responses. Field names use
// camelCase to match what the real aws-sdk-go-v2 EKS client emits.
//
// Pointer/optional fields use omitempty so absent values round-trip cleanly.

// vpcConfigRequest is the request body shape for VPC config in
// CreateCluster and UpdateClusterConfig.
type vpcConfigRequest struct {
	SubnetIDs             []string `json:"subnetIds,omitempty"`
	SecurityGroupIDs      []string `json:"securityGroupIds,omitempty"`
	EndpointPublicAccess  *bool    `json:"endpointPublicAccess,omitempty"`
	EndpointPrivateAccess *bool    `json:"endpointPrivateAccess,omitempty"`
	PublicAccessCidrs     []string `json:"publicAccessCidrs,omitempty"`
}

// vpcConfigResponse is the response shape EKS returns for VPC config.
type vpcConfigResponse struct {
	SubnetIDs              []string `json:"subnetIds"`
	SecurityGroupIDs       []string `json:"securityGroupIds"`
	ClusterSecurityGroupID string   `json:"clusterSecurityGroupId,omitempty"`
	VpcID                  string   `json:"vpcId,omitempty"`
	EndpointPublicAccess   bool     `json:"endpointPublicAccess"`
	EndpointPrivateAccess  bool     `json:"endpointPrivateAccess"`
	PublicAccessCidrs      []string `json:"publicAccessCidrs,omitempty"`
}

// certificate carries the base64 CA blob used in kubeconfig generation.
type certificate struct {
	Data string `json:"data"`
}

// clusterJSON is the EKS cluster resource shape.
type clusterJSON struct {
	Name                 string             `json:"name"`
	Arn                  string             `json:"arn"`
	CreatedAt            float64            `json:"createdAt"`
	Version              string             `json:"version,omitempty"`
	Endpoint             string             `json:"endpoint,omitempty"`
	RoleArn              string             `json:"roleArn,omitempty"`
	ResourcesVpcConfig   *vpcConfigResponse `json:"resourcesVpcConfig,omitempty"`
	Status               string             `json:"status"`
	CertificateAuthority *certificate       `json:"certificateAuthority,omitempty"`
	PlatformVersion      string             `json:"platformVersion,omitempty"`
	Tags                 map[string]string  `json:"tags,omitempty"`
}

// nodegroupScalingConfigJSON mirrors the SDK shape for nodegroup scaling.
type nodegroupScalingConfigJSON struct {
	MinSize     *int32 `json:"minSize,omitempty"`
	MaxSize     *int32 `json:"maxSize,omitempty"`
	DesiredSize *int32 `json:"desiredSize,omitempty"`
}

// nodegroupJSON is the EKS nodegroup resource shape.
type nodegroupJSON struct {
	NodegroupName  string                      `json:"nodegroupName"`
	NodegroupArn   string                      `json:"nodegroupArn"`
	ClusterName    string                      `json:"clusterName"`
	Version        string                      `json:"version,omitempty"`
	ReleaseVersion string                      `json:"releaseVersion,omitempty"`
	CreatedAt      float64                     `json:"createdAt"`
	ModifiedAt     float64                     `json:"modifiedAt"`
	Status         string                      `json:"status"`
	CapacityType   string                      `json:"capacityType,omitempty"`
	ScalingConfig  *nodegroupScalingConfigJSON `json:"scalingConfig,omitempty"`
	InstanceTypes  []string                    `json:"instanceTypes,omitempty"`
	Subnets        []string                    `json:"subnets,omitempty"`
	AmiType        string                      `json:"amiType,omitempty"`
	NodeRole       string                      `json:"nodeRole,omitempty"`
	Labels         map[string]string           `json:"labels,omitempty"`
	DiskSize       *int32                      `json:"diskSize,omitempty"`
	Tags           map[string]string           `json:"tags,omitempty"`
}

// fargateProfileSelectorJSON matches Pods to a Fargate profile.
type fargateProfileSelectorJSON struct {
	Namespace string            `json:"namespace,omitempty"`
	Labels    map[string]string `json:"labels,omitempty"`
}

// fargateProfileJSON is the EKS Fargate profile resource shape.
type fargateProfileJSON struct {
	FargateProfileName  string                       `json:"fargateProfileName"`
	FargateProfileArn   string                       `json:"fargateProfileArn"`
	ClusterName         string                       `json:"clusterName"`
	CreatedAt           float64                      `json:"createdAt"`
	PodExecutionRoleArn string                       `json:"podExecutionRoleArn,omitempty"`
	Subnets             []string                     `json:"subnets,omitempty"`
	Selectors           []fargateProfileSelectorJSON `json:"selectors,omitempty"`
	Status              string                       `json:"status"`
	Tags                map[string]string            `json:"tags,omitempty"`
}

// addonJSON is the EKS addon resource shape.
type addonJSON struct {
	AddonName             string            `json:"addonName"`
	ClusterName           string            `json:"clusterName"`
	Status                string            `json:"status"`
	AddonVersion          string            `json:"addonVersion,omitempty"`
	AddonArn              string            `json:"addonArn,omitempty"`
	CreatedAt             float64           `json:"createdAt"`
	ModifiedAt            float64           `json:"modifiedAt"`
	ServiceAccountRoleArn string            `json:"serviceAccountRoleArn,omitempty"`
	ConfigurationValues   string            `json:"configurationValues,omitempty"`
	Tags                  map[string]string `json:"tags,omitempty"`
}

// updateJSON is the EKS Update envelope returned from update operations.
type updateJSON struct {
	ID        string  `json:"id"`
	Type      string  `json:"type,omitempty"`
	Status    string  `json:"status,omitempty"`
	CreatedAt float64 `json:"createdAt"`
}

// Request bodies decoded from POST/PUT JSON.

type createClusterRequest struct {
	Name               string            `json:"name"`
	Version            string            `json:"version,omitempty"`
	RoleArn            string            `json:"roleArn,omitempty"`
	ResourcesVpcConfig *vpcConfigRequest `json:"resourcesVpcConfig,omitempty"`
	Tags               map[string]string `json:"tags,omitempty"`
}

type updateClusterConfigRequest struct {
	ResourcesVpcConfig *vpcConfigRequest `json:"resourcesVpcConfig,omitempty"`
	Tags               map[string]string `json:"tags,omitempty"`
}

type updateClusterVersionRequest struct {
	Version string `json:"version,omitempty"`
}

type createNodegroupRequest struct {
	NodegroupName  string                      `json:"nodegroupName"`
	NodeRole       string                      `json:"nodeRole,omitempty"`
	Subnets        []string                    `json:"subnets,omitempty"`
	InstanceTypes  []string                    `json:"instanceTypes,omitempty"`
	AmiType        string                      `json:"amiType,omitempty"`
	CapacityType   string                      `json:"capacityType,omitempty"`
	DiskSize       *int32                      `json:"diskSize,omitempty"`
	Version        string                      `json:"version,omitempty"`
	ReleaseVersion string                      `json:"releaseVersion,omitempty"`
	ScalingConfig  *nodegroupScalingConfigJSON `json:"scalingConfig,omitempty"`
	Labels         map[string]string           `json:"labels,omitempty"`
	Tags           map[string]string           `json:"tags,omitempty"`
}

type updateNodegroupConfigRequest struct {
	ScalingConfig *nodegroupScalingConfigJSON `json:"scalingConfig,omitempty"`
	Labels        *labelsUpdate               `json:"labels,omitempty"`
}

// labelsUpdate is the request shape for nodegroup label changes; the SDK
// sends addOrUpdateLabels and removeLabels separately. The mock applies
// addOrUpdateLabels and ignores removeLabels for Wave 1.
type labelsUpdate struct {
	AddOrUpdateLabels map[string]string `json:"addOrUpdateLabels,omitempty"`
	RemoveLabels      []string          `json:"removeLabels,omitempty"`
}

type updateNodegroupVersionRequest struct {
	Version        string `json:"version,omitempty"`
	ReleaseVersion string `json:"releaseVersion,omitempty"`
}

type createFargateProfileRequest struct {
	FargateProfileName  string                       `json:"fargateProfileName"`
	PodExecutionRoleArn string                       `json:"podExecutionRoleArn,omitempty"`
	Subnets             []string                     `json:"subnets,omitempty"`
	Selectors           []fargateProfileSelectorJSON `json:"selectors,omitempty"`
	Tags                map[string]string            `json:"tags,omitempty"`
}

type createAddonRequest struct {
	AddonName             string            `json:"addonName"`
	AddonVersion          string            `json:"addonVersion,omitempty"`
	ServiceAccountRoleArn string            `json:"serviceAccountRoleArn,omitempty"`
	ConfigurationValues   string            `json:"configurationValues,omitempty"`
	Tags                  map[string]string `json:"tags,omitempty"`
}

type updateAddonRequest struct {
	AddonVersion          string `json:"addonVersion,omitempty"`
	ServiceAccountRoleArn string `json:"serviceAccountRoleArn,omitempty"`
	ConfigurationValues   string `json:"configurationValues,omitempty"`
}

// Response envelopes.

type clusterEnvelope struct {
	Cluster clusterJSON `json:"cluster"`
}

type nodegroupEnvelope struct {
	Nodegroup nodegroupJSON `json:"nodegroup"`
}

type fargateProfileEnvelope struct {
	FargateProfile fargateProfileJSON `json:"fargateProfile"`
}

type addonEnvelope struct {
	Addon addonJSON `json:"addon"`
}

type updateEnvelope struct {
	Update updateJSON `json:"update"`
}

type listClustersResponse struct {
	Clusters []string `json:"clusters"`
}

type listNodegroupsResponse struct {
	Nodegroups []string `json:"nodegroups"`
}

type listFargateProfilesResponse struct {
	FargateProfileNames []string `json:"fargateProfileNames"`
}

type listAddonsResponse struct {
	Addons []string `json:"addons"`
}

// nanosPerSecond converts UnixNano to fractional seconds (the EKS wire format).
const nanosPerSecond = 1_000_000_000.0

// epochSeconds converts a Go time.Time to the AWS-style fractional epoch
// seconds the EKS REST/JSON SDK expects.
func epochSeconds(t time.Time) float64 {
	return float64(t.UnixNano()) / nanosPerSecond
}
