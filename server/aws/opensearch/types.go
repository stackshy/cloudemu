package opensearch

import (
	"encoding/json"

	"github.com/stackshy/cloudemu/v2/services/opensearch/driver"
)

// jsonRaw aliases json.RawMessage for terser handler signatures.
type jsonRaw = json.RawMessage

// marshalRaw marshals v to a json.RawMessage, panicking-free (best-effort).
func marshalRaw(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("null")
	}

	return b
}

// withNext adds a NextToken field to a response map when next is non-empty.
func withNext(m map[string]any, next string) map[string]any {
	if next != "" {
		m["NextToken"] = next
	}

	return m
}

// tag is the wire shape of an OpenSearch tag.
type tag struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

// clusterConfigJSON is the modeled subset of ClusterConfig on the wire. Fields
// are pointers so an absent field is distinguishable from a zero value, and any
// unmodeled field survives through the domain's raw options.
type clusterConfigJSON struct {
	InstanceType           *string `json:"InstanceType,omitempty"`
	InstanceCount          *int32  `json:"InstanceCount,omitempty"`
	DedicatedMasterEnabled *bool   `json:"DedicatedMasterEnabled,omitempty"`
	DedicatedMasterType    *string `json:"DedicatedMasterType,omitempty"`
	DedicatedMasterCount   *int32  `json:"DedicatedMasterCount,omitempty"`
	ZoneAwarenessEnabled   *bool   `json:"ZoneAwarenessEnabled,omitempty"`
	WarmEnabled            *bool   `json:"WarmEnabled,omitempty"`
	WarmType               *string `json:"WarmType,omitempty"`
	WarmCount              *int32  `json:"WarmCount,omitempty"`
}

// toDriver converts a wire cluster config to the driver type.
//
//nolint:gocyclo // one nil-guarded copy per optional field; each branch is trivial.
func (c *clusterConfigJSON) toDriver() driver.ClusterConfig {
	out := driver.ClusterConfig{}
	if c == nil {
		return out
	}

	if c.InstanceType != nil {
		out.InstanceType = *c.InstanceType
	}

	if c.InstanceCount != nil {
		out.InstanceCount = *c.InstanceCount
	}

	if c.DedicatedMasterEnabled != nil {
		out.DedicatedMasterEnabled = *c.DedicatedMasterEnabled
	}

	if c.DedicatedMasterType != nil {
		out.DedicatedMasterType = *c.DedicatedMasterType
	}

	if c.DedicatedMasterCount != nil {
		out.DedicatedMasterCount = *c.DedicatedMasterCount
	}

	if c.ZoneAwarenessEnabled != nil {
		out.ZoneAwarenessEnabled = *c.ZoneAwarenessEnabled
	}

	if c.WarmEnabled != nil {
		out.WarmEnabled = *c.WarmEnabled
	}

	if c.WarmType != nil {
		out.WarmType = *c.WarmType
	}

	if c.WarmCount != nil {
		out.WarmCount = *c.WarmCount
	}

	return out
}

// clusterConfigToWire renders a driver cluster config as its wire shape.
func clusterConfigToWire(c driver.ClusterConfig) clusterConfigJSON {
	instanceType := c.InstanceType
	instanceCount := c.InstanceCount
	dme := c.DedicatedMasterEnabled
	dmt := c.DedicatedMasterType
	dmc := c.DedicatedMasterCount
	zae := c.ZoneAwarenessEnabled
	we := c.WarmEnabled
	wt := c.WarmType
	wc := c.WarmCount

	return clusterConfigJSON{
		InstanceType:           &instanceType,
		InstanceCount:          &instanceCount,
		DedicatedMasterEnabled: &dme,
		DedicatedMasterType:    &dmt,
		DedicatedMasterCount:   &dmc,
		ZoneAwarenessEnabled:   &zae,
		WarmEnabled:            &we,
		WarmType:               &wt,
		WarmCount:              &wc,
	}
}

// createDomainRequest is the CreateDomain request body. Modeled fields are
// promoted; every other option block is captured into RawOptions via a second
// decode pass (see decodeRawOptions).
type createDomainRequest struct {
	DomainName      string             `json:"DomainName"`
	EngineVersion   string             `json:"EngineVersion"`
	EngineMode      string             `json:"EngineMode"`
	IPAddressType   string             `json:"IPAddressType"`
	ClusterConfig   *clusterConfigJSON `json:"ClusterConfig"`
	AccessPolicies  string             `json:"AccessPolicies"`
	AdvancedOptions map[string]string  `json:"AdvancedOptions"`
	TagList         []tag              `json:"TagList"`
}

// updateDomainConfigRequest is the UpdateDomainConfig request body.
type updateDomainConfigRequest struct {
	ClusterConfig   *clusterConfigJSON `json:"ClusterConfig"`
	AccessPolicies  *string            `json:"AccessPolicies"`
	IPAddressType   *string            `json:"IPAddressType"`
	AdvancedOptions map[string]string  `json:"AdvancedOptions"`
	DryRun          bool               `json:"DryRun"`
}

// modeledDomainFields lists the top-level option blocks promoted to typed
// driver fields; every other key in a request body is carried as a raw option.
func modeledDomainFields() map[string]struct{} {
	return map[string]struct{}{
		"DomainName": {}, "EngineVersion": {}, "EngineMode": {}, "IPAddressType": {},
		"ClusterConfig": {}, "AccessPolicies": {}, "AdvancedOptions": {}, "TagList": {},
		"DryRun": {},
	}
}

// decodeRawOptions returns every top-level key of body that is not a modeled
// domain field, so unmodeled option blocks (EBS, VPC, Cognito, etc.) round-trip.
func decodeRawOptions(body []byte) map[string]json.RawMessage {
	var all map[string]json.RawMessage
	if err := json.Unmarshal(body, &all); err != nil {
		return nil
	}

	modeled := modeledDomainFields()

	out := make(map[string]json.RawMessage)

	for k, v := range all {
		if _, skip := modeled[k]; skip {
			continue
		}

		out[k] = v
	}

	if len(out) == 0 {
		return nil
	}

	return out
}

// domainStatusJSON is the wire shape of a domain status.
type domainStatusJSON struct {
	DomainID               string            `json:"DomainId"`
	DomainName             string            `json:"DomainName"`
	ARN                    string            `json:"ARN"`
	Created                bool              `json:"Created"`
	Deleted                bool              `json:"Deleted"`
	Processing             bool              `json:"Processing"`
	UpgradeProcessing      bool              `json:"UpgradeProcessing"`
	EngineVersion          string            `json:"EngineVersion"`
	Endpoint               string            `json:"Endpoint,omitempty"`
	Endpoints              map[string]string `json:"Endpoints,omitempty"`
	DomainProcessingStatus string            `json:"DomainProcessingStatus,omitempty"`
	ClusterConfig          clusterConfigJSON `json:"ClusterConfig"`
	AccessPolicies         string            `json:"AccessPolicies,omitempty"`
	AdvancedOptions        map[string]string `json:"AdvancedOptions,omitempty"`
	IPAddressType          string            `json:"IPAddressType,omitempty"`
}

// domainStatusToWire renders a driver domain status, merging any raw options.
func domainStatusToWire(s *driver.DomainStatus) map[string]json.RawMessage {
	base := domainStatusJSON{
		DomainID:               s.DomainID,
		DomainName:             s.DomainName,
		ARN:                    s.ARN,
		Created:                s.Created,
		Deleted:                s.Deleted,
		Processing:             s.Processing,
		UpgradeProcessing:      s.UpgradeProcessing,
		EngineVersion:          s.EngineVersion,
		Endpoint:               s.Endpoint,
		Endpoints:              s.Endpoints,
		DomainProcessingStatus: s.DomainProcessingStatus,
		ClusterConfig:          clusterConfigToWire(s.ClusterConfig),
		AccessPolicies:         s.AccessPolicies,
		AdvancedOptions:        s.AdvancedOptions,
		IPAddressType:          s.IPAddressType,
	}

	return mergeRaw(base, s.RawOptions)
}

// statusEnvelope wraps a value in the {Options, Status} shape DescribeDomainConfig
// uses for each option block.
func statusEnvelope(options any) map[string]any {
	return map[string]any{
		"Options": options,
		"Status": map[string]any{
			"State":           "Active",
			"PendingDeletion": false,
		},
	}
}

// domainConfigToWire renders a driver domain config as the DomainConfig wire
// shape, wrapping each modeled block in a status envelope.
func domainConfigToWire(c *driver.DomainConfig) map[string]json.RawMessage {
	base := map[string]any{
		"EngineVersion":   statusEnvelope(c.EngineVersion),
		"ClusterConfig":   statusEnvelope(clusterConfigToWire(c.ClusterConfig)),
		"AccessPolicies":  statusEnvelope(c.AccessPolicies),
		"AdvancedOptions": statusEnvelope(c.AdvancedOptions),
		"IPAddressType":   statusEnvelope(c.IPAddressType),
	}

	raw, _ := json.Marshal(base)

	var out map[string]json.RawMessage
	_ = json.Unmarshal(raw, &out)

	for k, v := range c.RawOptions {
		out[k] = v
	}

	return out
}

// mergeRaw marshals base, then overlays any raw option blocks so unmodeled
// options appear alongside the modeled fields.
func mergeRaw(base any, raw map[string]json.RawMessage) map[string]json.RawMessage {
	b, _ := json.Marshal(base)

	var out map[string]json.RawMessage
	_ = json.Unmarshal(b, &out)

	for k, v := range raw {
		out[k] = v
	}

	return out
}

// tagsToMap converts a wire tag list to a map.
func tagsToMap(tags []tag) map[string]string {
	if tags == nil {
		return nil
	}

	out := make(map[string]string, len(tags))
	for _, t := range tags {
		out[t.Key] = t.Value
	}

	return out
}

// mapToTags converts a tag map to a wire tag list.
func mapToTags(m map[string]string) []tag {
	out := make([]tag, 0, len(m))
	for k, v := range m {
		out = append(out, tag{Key: k, Value: v})
	}

	return out
}

// packageToWire renders a driver package as its wire shape.
func packageToWire(p *driver.Package) map[string]any {
	return map[string]any{
		"PackageID":               p.PackageID,
		"PackageName":             p.PackageName,
		"PackageType":             p.PackageType,
		"PackageDescription":      p.PackageDescription,
		"PackageStatus":           p.PackageStatus,
		"CreatedAt":               p.CreatedAt.Unix(),
		"LastUpdatedAt":           p.LastUpdatedAt.Unix(),
		"AvailablePackageVersion": p.AvailableVersion,
		"EngineVersion":           p.EngineVersion,
	}
}

// associationToWire renders a domain-package association as its wire shape.
func associationToWire(a *driver.DomainPackageAssociation) map[string]any {
	return map[string]any{
		"PackageID":           a.PackageID,
		"PackageName":         a.PackageName,
		"PackageType":         a.PackageType,
		"DomainName":          a.DomainName,
		"DomainPackageStatus": a.DomainPackageStatus,
		"PackageVersion":      a.PackageVersion,
		"ReferencePath":       a.ReferencePath,
	}
}

// vpcEndpointToWire renders a driver VPC endpoint as its wire shape.
func vpcEndpointToWire(v *driver.VpcEndpoint) map[string]any {
	return map[string]any{
		"VpcEndpointId":    v.VpcEndpointID,
		"VpcEndpointOwner": v.VpcEndpointOwner,
		"DomainArn":        v.DomainARN,
		"Status":           v.Status,
		"Endpoint":         v.Endpoint,
		"VpcOptions": map[string]any{
			"VPCId":             v.VPCID,
			"SubnetIds":         v.SubnetIDs,
			"SecurityGroupIds":  v.SecurityGroupIDs,
			"AvailabilityZones": v.AvailabilityZones,
		},
	}
}

// outboundToWire renders an outbound connection as its wire shape.
func outboundToWire(c *driver.OutboundConnection) map[string]any {
	return map[string]any{
		"ConnectionId":    c.ConnectionID,
		"ConnectionAlias": c.ConnectionAlias,
		"ConnectionMode":  c.ConnectionMode,
		"ConnectionStatus": map[string]any{
			"StatusCode": c.StatusCode,
			"Message":    c.StatusMessage,
		},
		"LocalDomainInfo":  awsDomainInfo(c.LocalOwnerID, c.LocalDomainName, c.LocalRegion),
		"RemoteDomainInfo": awsDomainInfo(c.RemoteOwnerID, c.RemoteDomainName, c.RemoteRegion),
	}
}

// inboundToWire renders an inbound connection as its wire shape.
func inboundToWire(c *driver.InboundConnection) map[string]any {
	return map[string]any{
		"ConnectionId": c.ConnectionID,
		"ConnectionStatus": map[string]any{
			"StatusCode": c.ConnectionStatus,
			"Message":    c.StatusMessage,
		},
		"LocalDomainInfo":  awsDomainInfo(c.LocalOwnerID, c.LocalDomainName, c.LocalRegion),
		"RemoteDomainInfo": awsDomainInfo(c.RemoteOwnerID, c.RemoteDomainName, c.RemoteRegion),
	}
}

// awsDomainInfo renders the AWSDomainInformation envelope used by connections.
func awsDomainInfo(ownerID, domainName, region string) map[string]any {
	return map[string]any{
		"AWSDomainInformation": map[string]any{
			"OwnerId":    ownerID,
			"DomainName": domainName,
			"Region":     region,
		},
	}
}

// applicationToWire renders an application as its wire shape.
func applicationToWire(a *driver.Application) map[string]any {
	return map[string]any{
		"id":                       a.ID,
		"arn":                      a.ARN,
		"name":                     a.Name,
		"endpoint":                 a.Endpoint,
		"status":                   a.Status,
		"createdAt":                a.CreatedAt.Unix(),
		"lastUpdatedAt":            a.LastUpdatedAt.Unix(),
		"dataSources":              a.DataSources,
		"appConfigs":               a.AppConfigs,
		"iamIdentityCenterOptions": a.IamIdentityCenterOptions,
		"tagList":                  mapToTags(a.TagList),
	}
}

// reservedToWire renders a reserved instance as its wire shape.
func reservedToWire(r *driver.ReservedInstance) map[string]any {
	return map[string]any{
		"ReservationName":            r.ReservationName,
		"ReservedInstanceId":         r.ReservedInstanceID,
		"ReservedInstanceOfferingId": r.ReservedInstanceOfferingID,
		"InstanceType":               r.InstanceType,
		"InstanceCount":              r.InstanceCount,
		"Duration":                   r.Duration,
		"FixedPrice":                 r.FixedPrice,
		"UsagePrice":                 r.UsagePrice,
		"CurrencyCode":               r.CurrencyCode,
		"PaymentOption":              r.PaymentOption,
		"State":                      r.State,
		"StartTime":                  r.StartTime.Unix(),
	}
}

// upgradeStepToWire renders an upgrade step as its wire shape.
func upgradeStepToWire(s driver.UpgradeStep) map[string]any {
	return map[string]any{
		"UpgradeStep":     s.UpgradeStep,
		"StepStatus":      s.StepStatus,
		"ProgressPercent": s.ProgressPercent,
	}
}

// upgradeHistoryToWire renders an upgrade history record as its wire shape.
//
//nolint:gocritic // hugeParam: rendered per-history-record from a range copy.
func upgradeHistoryToWire(h driver.UpgradeHistory) map[string]any {
	steps := make([]map[string]any, 0, len(h.StepsList))
	for _, s := range h.StepsList {
		steps = append(steps, upgradeStepToWire(s))
	}

	return map[string]any{
		"UpgradeName":    h.UpgradeName,
		"StartTimestamp": h.StartTime.Unix(),
		"UpgradeStatus":  h.UpgradeStatus,
		"StepsList":      steps,
	}
}
