package iothub

import (
	"encoding/json"

	"github.com/stackshy/cloudemu/v2/providers/azure/iothub"
)

// hubRequest is the ARM IoT Hub PUT/PATCH body. location, tags and sku are
// top-level; the rest live under properties.
type hubRequest struct {
	Location   string                `json:"location"`
	Tags       map[string]string     `json:"tags,omitempty"`
	Sku        *skuWire              `json:"sku,omitempty"`
	Properties *hubPropertiesRequest `json:"properties,omitempty"`
}

// skuWire is the hub SKU block. Capacity is a pointer so an omitted value falls
// back to the stored (or default) capacity.
type skuWire struct {
	Name     string `json:"name,omitempty"`
	Tier     string `json:"tier,omitempty"`
	Capacity *int64 `json:"capacity,omitempty"`
}

// hubPropertiesRequest is the writable subset of hub properties.
type hubPropertiesRequest struct {
	Features                      *string                    `json:"features,omitempty"`
	MinTLSVersion                 *string                    `json:"minTlsVersion,omitempty"`
	PublicNetworkAccess           *string                    `json:"publicNetworkAccess,omitempty"`
	DisableLocalAuth              *bool                      `json:"disableLocalAuth,omitempty"`
	EnableFileUploadNotifications *bool                      `json:"enableFileUploadNotifications,omitempty"`
	AuthorizationPolicies         []policyWire               `json:"authorizationPolicies,omitempty"`
	EventHubEndpoints             map[string]eventHubReqWire `json:"eventHubEndpoints,omitempty"`
	Routing                       json.RawMessage            `json:"routing,omitempty"`
}

// eventHubReqWire is the writable subset of the built-in events endpoint.
type eventHubReqWire struct {
	PartitionCount      *int64 `json:"partitionCount,omitempty"`
	RetentionTimeInDays *int64 `json:"retentionTimeInDays,omitempty"`
}

// policyWire is the wire shape of a shared-access authorization rule.
type policyWire struct {
	KeyName      string `json:"keyName,omitempty"`
	PrimaryKey   string `json:"primaryKey,omitempty"`
	SecondaryKey string `json:"secondaryKey,omitempty"`
	Rights       string `json:"rights,omitempty"`
}

// hubResponse is the ARM representation of an IoT hub. Keys are never echoed here
// — the plain GET/PUT/PATCH response omits authorizationPolicies, matching real
// IoT Hub behavior where keys are retrieved only via the listkeys actions.
type hubResponse struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Type       string            `json:"type"`
	Location   string            `json:"location"`
	Tags       map[string]string `json:"tags,omitempty"`
	Etag       string            `json:"etag"`
	Sku        skuWire           `json:"sku"`
	Properties hubPropertiesResp `json:"properties"`
}

// hubPropertiesResp is the hub properties block. The computed fields
// (provisioningState, state, hostName, eventHubEndpoints) are stable across reads.
type hubPropertiesResp struct {
	ProvisioningState             string                      `json:"provisioningState"`
	State                         string                      `json:"state"`
	HostName                      string                      `json:"hostName"`
	Features                      string                      `json:"features,omitempty"`
	MinTLSVersion                 string                      `json:"minTlsVersion,omitempty"`
	PublicNetworkAccess           string                      `json:"publicNetworkAccess,omitempty"`
	DisableLocalAuth              *bool                       `json:"disableLocalAuth,omitempty"`
	EnableFileUploadNotifications *bool                       `json:"enableFileUploadNotifications,omitempty"`
	EventHubEndpoints             map[string]eventHubRespWire `json:"eventHubEndpoints"`
	Routing                       json.RawMessage             `json:"routing,omitempty"`
}

// eventHubRespWire is the built-in events endpoint on the wire.
type eventHubRespWire struct {
	RetentionTimeInDays int64    `json:"retentionTimeInDays"`
	PartitionCount      int64    `json:"partitionCount"`
	PartitionIDs        []string `json:"partitionIds"`
	Path                string   `json:"path"`
	Endpoint            string   `json:"endpoint"`
}

// hubListResponse is the ARM hub list envelope. nextLink is omitted — the
// emulator returns a single page.
type hubListResponse struct {
	Value []hubResponse `json:"value"`
}

// keysListResponse is the paginated listkeys body: the hub's shared-access
// policies with their stable keys.
type keysListResponse struct {
	Value []policyWire `json:"value"`
}

// consumerGroupResponse is the ARM representation of an event-hub consumer group.
type consumerGroupResponse struct {
	ID         string             `json:"id"`
	Name       string             `json:"name"`
	Type       string             `json:"type"`
	Etag       string             `json:"etag"`
	Properties consumerGroupProps `json:"properties"`
}

// consumerGroupProps is the consumer group properties block.
type consumerGroupProps struct {
	Name string `json:"name"`
}

// consumerGroupListResponse is the ARM consumer group list envelope.
type consumerGroupListResponse struct {
	Value []consumerGroupResponse `json:"value"`
}

// hubInputFromRequest builds a hub create/update Input from a request body.
// Pointer fields are carried through so an absent field falls back to the stored
// (or default) value in the driver, which makes a PATCH body merge on its own.
func hubInputFromRequest(req *hubRequest) iothub.HubInput {
	in := iothub.HubInput{Tags: req.Tags}

	if req.Sku != nil {
		if req.Sku.Name != "" {
			name := req.Sku.Name
			in.SkuName = &name
		}

		in.SkuCapacity = req.Sku.Capacity
	}

	applyPropertiesToInput(&in, req.Properties)

	return in
}

// applyPropertiesToInput copies the writable properties block onto the driver
// input.
func applyPropertiesToInput(in *iothub.HubInput, p *hubPropertiesRequest) {
	if p == nil {
		return
	}

	in.Features = p.Features
	in.MinTLSVersion = p.MinTLSVersion
	in.PublicNetworkAccess = p.PublicNetworkAccess
	in.DisableLocalAuth = p.DisableLocalAuth
	in.EnableFileUploadNotifications = p.EnableFileUploadNotifications
	in.Routing = p.Routing

	if events, ok := p.EventHubEndpoints[eventsKey]; ok {
		in.PartitionCount = events.PartitionCount
		in.RetentionTimeInDays = events.RetentionTimeInDays
	}

	in.Policies = policiesToDriver(p.AuthorizationPolicies)
}

// policiesToDriver maps the wire authorization policies onto the driver shape.
func policiesToDriver(in []policyWire) []iothub.SharedAccessPolicy {
	if in == nil {
		return nil
	}

	out := make([]iothub.SharedAccessPolicy, 0, len(in))
	for i := range in {
		out = append(out, iothub.SharedAccessPolicy{
			KeyName:      in[i].KeyName,
			PrimaryKey:   in[i].PrimaryKey,
			SecondaryKey: in[i].SecondaryKey,
			Rights:       in[i].Rights,
		})
	}

	return out
}

// toHubResponse projects a stored hub onto the ARM wire representation. The
// shared-access-policy keys are deliberately omitted — only the listkeys actions
// surface them.
func toHubResponse(h *iothub.Hub) hubResponse {
	return hubResponse{
		ID:       h.ARMID(),
		Name:     h.Name,
		Type:     hubArmType,
		Location: h.Location,
		Tags:     h.Tags,
		Etag:     h.Etag,
		Sku:      skuWire{Name: h.Sku.Name, Tier: h.Sku.Tier, Capacity: ptrInt64(h.Sku.Capacity)},
		Properties: hubPropertiesResp{
			ProvisioningState:             h.ProvisioningState,
			State:                         h.State,
			HostName:                      h.HostName,
			Features:                      h.Features,
			MinTLSVersion:                 h.MinTLSVersion,
			PublicNetworkAccess:           h.PublicNetworkAccess,
			DisableLocalAuth:              h.DisableLocalAuth,
			EnableFileUploadNotifications: h.EnableFileUploadNotifications,
			EventHubEndpoints:             map[string]eventHubRespWire{eventsKey: toEventHubResp(&h.Events)},
			Routing:                       h.Routing,
		},
	}
}

// toEventHubResp projects the stored built-in events endpoint onto the wire.
func toEventHubResp(e *iothub.EventHubEndpoint) eventHubRespWire {
	return eventHubRespWire{
		RetentionTimeInDays: e.RetentionTimeInDays,
		PartitionCount:      e.PartitionCount,
		PartitionIDs:        e.PartitionIDs,
		Path:                e.Path,
		Endpoint:            e.Endpoint,
	}
}

// toKeysList projects a hub's policies onto the paginated listkeys body.
func toKeysList(policies []iothub.SharedAccessPolicy) keysListResponse {
	out := keysListResponse{Value: make([]policyWire, 0, len(policies))}
	for i := range policies {
		out.Value = append(out.Value, toPolicyWire(&policies[i]))
	}

	return out
}

// toPolicyWire projects one stored policy onto its wire shape.
func toPolicyWire(p *iothub.SharedAccessPolicy) policyWire {
	return policyWire{
		KeyName:      p.KeyName,
		PrimaryKey:   p.PrimaryKey,
		SecondaryKey: p.SecondaryKey,
		Rights:       p.Rights,
	}
}

// toConsumerGroupResponse projects a stored consumer group onto its ARM wire
// representation.
func toConsumerGroupResponse(c *iothub.ConsumerGroup) consumerGroupResponse {
	return consumerGroupResponse{
		ID:         c.ARMID(),
		Name:       c.Name,
		Type:       c.ARMType(),
		Etag:       c.Etag,
		Properties: consumerGroupProps{Name: c.Name},
	}
}

// ptrInt64 returns a pointer to v, so a capacity of 0 still round-trips.
func ptrInt64(v int64) *int64 {
	return &v
}
