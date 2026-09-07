package elasticsan

import (
	"github.com/stackshy/cloudemu/v2/providers/azure/elasticsan"
)

// sanRequest is the ARM PUT/PATCH body. location, tags and sku are top-level;
// the remaining configuration (sizes, zones, publicNetworkAccess) lives under
// properties.
type sanRequest struct {
	Location   string             `json:"location,omitempty"`
	Tags       map[string]string  `json:"tags,omitempty"`
	Sku        *skuWire           `json:"sku,omitempty"`
	Properties *propertiesRequest `json:"properties,omitempty"`
}

// skuWire is the Elastic SAN sku block, top-level on both request and response.
type skuWire struct {
	Name string `json:"name"`
	Tier string `json:"tier,omitempty"`
}

// propertiesRequest is the writable subset of properties. Pointer fields let a
// PATCH overlay only what it names; availabilityZones is top-level under
// properties (it maps to Terraform's `zones`).
type propertiesRequest struct {
	BaseSizeTiB         *int64   `json:"baseSizeTiB,omitempty"`
	ExtendedSizeTiB     *int64   `json:"extendedCapacitySizeTiB,omitempty"`
	AvailabilityZones   []string `json:"availabilityZones,omitempty"`
	PublicNetworkAccess *string  `json:"publicNetworkAccess,omitempty"`
}

// sanResponse is the ARM representation of an Elastic SAN resource.
type sanResponse struct {
	ID         string             `json:"id"`
	Name       string             `json:"name"`
	Type       string             `json:"type"`
	Location   string             `json:"location"`
	Tags       map[string]string  `json:"tags,omitempty"`
	Sku        *skuWire           `json:"sku,omitempty"`
	Properties propertiesResponse `json:"properties"`
}

// propertiesResponse is the properties block. The computed total_* fields and
// provisioningState are stable across reads.
type propertiesResponse struct {
	BaseSizeTiB         int64    `json:"baseSizeTiB"`
	ExtendedSizeTiB     int64    `json:"extendedCapacitySizeTiB"`
	AvailabilityZones   []string `json:"availabilityZones,omitempty"`
	PublicNetworkAccess string   `json:"publicNetworkAccess,omitempty"`
	ProvisioningState   string   `json:"provisioningState"`
	TotalIops           int64    `json:"totalIops"`
	TotalMBps           int64    `json:"totalMBps"`
	TotalSizeTiB        int64    `json:"totalSizeTiB"`
	TotalVolumeSizeGiB  int64    `json:"totalVolumeSizeGiB"`
	VolumeGroupCount    int64    `json:"volumeGroupCount"`
}

// listResponse is the ARM list envelope. nextLink is omitted — the emulator
// returns a single page.
type listResponse struct {
	Value []sanResponse `json:"value"`
}

// toResponse projects a stored resource onto its ARM wire representation.
func toResponse(s *elasticsan.ElasticSan) sanResponse {
	out := sanResponse{
		ID:       s.ARMID(),
		Name:     s.Name,
		Type:     armType,
		Location: s.Location,
		Tags:     s.Tags,
		Properties: propertiesResponse{
			BaseSizeTiB:         s.BaseSizeTiB,
			ExtendedSizeTiB:     s.ExtendedSizeTiB,
			AvailabilityZones:   s.Zones,
			PublicNetworkAccess: s.PublicNetworkAccess,
			ProvisioningState:   s.ProvisioningState,
			TotalIops:           s.TotalIops,
			TotalMBps:           s.TotalMBps,
			TotalSizeTiB:        s.TotalSizeTiB,
			TotalVolumeSizeGiB:  s.TotalVolumeSizeGiB,
			VolumeGroupCount:    s.VolumeGroupCount,
		},
	}

	if s.Sku != nil {
		out.Sku = &skuWire{Name: s.Sku.Name, Tier: s.Sku.Tier}
	}

	return out
}

// inputFromRequest builds a create/update Input from a request body. Pointer and
// slice fields are carried through verbatim so an absent field falls back to the
// stored (or default) value in the driver — which makes a PATCH body, where every
// field is optional, merge correctly on its own.
func inputFromRequest(req *sanRequest) elasticsan.Input {
	in := elasticsan.Input{Tags: req.Tags}

	if req.Sku != nil {
		in.Sku = &elasticsan.Sku{Name: req.Sku.Name, Tier: req.Sku.Tier}
	}

	if req.Properties != nil {
		in.BaseSizeTiB = req.Properties.BaseSizeTiB
		in.ExtendedSizeTiB = req.Properties.ExtendedSizeTiB
		in.Zones = req.Properties.AvailabilityZones
		in.PublicNetworkAccess = req.Properties.PublicNetworkAccess
	}

	return in
}
