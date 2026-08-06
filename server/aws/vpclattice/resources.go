package vpclattice

import (
	"encoding/json"
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/vpclattice/driver"
)

// --- resource configurations ---

type wireResourceConfig struct {
	Arn                      string          `json:"arn,omitempty"`
	ID                       string          `json:"id,omitempty"`
	Name                     string          `json:"name,omitempty"`
	Type                     string          `json:"type,omitempty"`
	Status                   string          `json:"status,omitempty"`
	Protocol                 string          `json:"protocol,omitempty"`
	CustomDomainName         string          `json:"customDomainName,omitempty"`
	GroupDomain              string          `json:"groupDomain,omitempty"`
	PortRanges               []string        `json:"portRanges,omitempty"`
	Definition               json.RawMessage `json:"resourceConfigurationDefinition,omitempty"`
	ResourceGatewayID        string          `json:"resourceGatewayId,omitempty"`
	ResourceConfigGroupID    string          `json:"resourceConfigurationGroupId,omitempty"`
	AllowAssociationToShared bool            `json:"allowAssociationToShareableServiceNetwork"`
	CreatedAt                string          `json:"createdAt,omitempty"`
	LastUpdatedAt            string          `json:"lastUpdatedAt,omitempty"`
}

func resourceConfigToWire(c *driver.ResourceConfiguration) wireResourceConfig {
	w := wireResourceConfig{
		Arn: c.ARN, ID: c.ID, Name: c.Name, Type: c.Type, Status: c.Status, Protocol: c.Protocol,
		CustomDomainName: c.CustomDomainName, GroupDomain: c.GroupDomain, PortRanges: c.PortRanges,
		ResourceGatewayID: c.ResourceGatewayID, ResourceConfigGroupID: c.ResourceConfigGroupID,
		AllowAssociationToShared: c.AllowAssociationToShared, CreatedAt: c.CreatedAt, LastUpdatedAt: c.LastUpdatedAt,
	}
	if len(c.Definition) > 0 {
		w.Definition = json.RawMessage(c.Definition)
	}

	return w
}

func (h *Handler) serveResourceConfigurations(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 {
		routeCollection(w, r, h.createResourceConfig, h.listResourceConfigs)

		return
	}

	routeByID(w, r, rest[0], h.getResourceConfig, h.updateResourceConfig, h.deleteResourceConfig)
}

func (h *Handler) createResourceConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name                     string          `json:"name"`
		Type                     string          `json:"type"`
		Protocol                 string          `json:"protocol"`
		CustomDomainName         string          `json:"customDomainName"`
		GroupDomain              string          `json:"groupDomain"`
		PortRanges               []string        `json:"portRanges"`
		Definition               json.RawMessage `json:"resourceConfigurationDefinition"`
		ResourceGatewayID        string          `json:"resourceGatewayIdentifier"`
		ResourceConfigGroupID    string          `json:"resourceConfigurationGroupIdentifier"`
		AllowAssociationToShared bool            `json:"allowAssociationToShareableServiceNetwork"`
		Tags                     map[string]string
	}

	if !decodeJSON(w, r, &req) {
		return
	}

	c, err := h.lattice.CreateResourceConfiguration(r.Context(), &driver.CreateResourceConfigurationInput{
		Name: req.Name, Type: req.Type, Protocol: req.Protocol, CustomDomainName: req.CustomDomainName,
		GroupDomain: req.GroupDomain, PortRanges: req.PortRanges, Definition: req.Definition,
		ResourceGatewayID: req.ResourceGatewayID, ResourceConfigGroupID: req.ResourceConfigGroupID,
		AllowAssociationToShared: req.AllowAssociationToShared, Tags: req.Tags,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, resourceConfigToWire(c))
}

func (h *Handler) getResourceConfig(w http.ResponseWriter, r *http.Request, id string) {
	c, err := h.lattice.GetResourceConfiguration(r.Context(), id)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, resourceConfigToWire(c))
}

func (h *Handler) updateResourceConfig(w http.ResponseWriter, r *http.Request, id string) {
	var req struct {
		PortRanges               []string        `json:"portRanges"`
		Definition               json.RawMessage `json:"resourceConfigurationDefinition"`
		AllowAssociationToShared *bool           `json:"allowAssociationToShareableServiceNetwork"`
	}

	if !decodeJSON(w, r, &req) {
		return
	}

	c, err := h.lattice.UpdateResourceConfiguration(r.Context(), &driver.UpdateResourceConfigurationInput{
		ID: id, PortRanges: req.PortRanges, Definition: req.Definition,
		AllowAssociationToShared: req.AllowAssociationToShared,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, resourceConfigToWire(c))
}

func (h *Handler) deleteResourceConfig(w http.ResponseWriter, r *http.Request, id string) {
	if err := h.lattice.DeleteResourceConfiguration(r.Context(), id); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, struct{}{})
}

func (h *Handler) listResourceConfigs(w http.ResponseWriter, r *http.Request) {
	cs, err := h.lattice.ListResourceConfigurations(r.Context())
	if err != nil {
		writeErr(w, err)

		return
	}

	items := make([]wireResourceConfig, 0, len(cs))
	for i := range cs {
		items = append(items, resourceConfigToWire(&cs[i]))
	}

	writeJSON(w, map[string]any{"items": items})
}

// --- resource gateways ---

type wireResourceGateway struct {
	Arn                         string   `json:"arn,omitempty"`
	ID                          string   `json:"id,omitempty"`
	Name                        string   `json:"name,omitempty"`
	Status                      string   `json:"status,omitempty"`
	IPAddressType               string   `json:"ipAddressType,omitempty"`
	Ipv4AddressesPerEni         int32    `json:"ipv4AddressesPerEni,omitempty"`
	ResourceConfigDNSResolution string   `json:"resourceConfigDnsResolution,omitempty"`
	SecurityGroupIDs            []string `json:"securityGroupIds,omitempty"`
	SubnetIDs                   []string `json:"subnetIds,omitempty"`
	VpcIdentifier               string   `json:"vpcIdentifier,omitempty"`
	CreatedAt                   string   `json:"createdAt,omitempty"`
	LastUpdatedAt               string   `json:"lastUpdatedAt,omitempty"`
}

func resourceGatewayToWire(g *driver.ResourceGateway) wireResourceGateway {
	return wireResourceGateway{
		Arn: g.ARN, ID: g.ID, Name: g.Name, Status: g.Status, IPAddressType: g.IPAddressType,
		Ipv4AddressesPerEni: g.Ipv4AddressesPerEni, ResourceConfigDNSResolution: g.ResourceConfigDNSResolution,
		SecurityGroupIDs: g.SecurityGroupIDs, SubnetIDs: g.SubnetIDs, VpcIdentifier: g.VpcID,
		CreatedAt: g.CreatedAt, LastUpdatedAt: g.LastUpdatedAt,
	}
}

func (h *Handler) serveResourceGateways(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 {
		routeCollection(w, r, h.createResourceGateway, h.listResourceGateways)

		return
	}

	routeByID(w, r, rest[0], h.getResourceGateway, h.updateResourceGateway, h.deleteResourceGateway)
}

func (h *Handler) createResourceGateway(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name                        string            `json:"name"`
		IPAddressType               string            `json:"ipAddressType"`
		Ipv4AddressesPerEni         int32             `json:"ipv4AddressesPerEni"`
		ResourceConfigDNSResolution string            `json:"resourceConfigDnsResolution"`
		SecurityGroupIDs            []string          `json:"securityGroupIds"`
		SubnetIDs                   []string          `json:"subnetIds"`
		VpcIdentifier               string            `json:"vpcIdentifier"`
		Tags                        map[string]string `json:"tags"`
	}

	if !decodeJSON(w, r, &req) {
		return
	}

	g, err := h.lattice.CreateResourceGateway(r.Context(), &driver.CreateResourceGatewayInput{
		Name: req.Name, IPAddressType: req.IPAddressType, Ipv4AddressesPerEni: req.Ipv4AddressesPerEni,
		ResourceConfigDNSResolution: req.ResourceConfigDNSResolution, SecurityGroupIDs: req.SecurityGroupIDs,
		SubnetIDs: req.SubnetIDs, VpcID: req.VpcIdentifier, Tags: req.Tags,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, resourceGatewayToWire(g))
}

func (h *Handler) getResourceGateway(w http.ResponseWriter, r *http.Request, id string) {
	g, err := h.lattice.GetResourceGateway(r.Context(), id)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, resourceGatewayToWire(g))
}

func (h *Handler) updateResourceGateway(w http.ResponseWriter, r *http.Request, id string) {
	var req struct {
		SecurityGroupIDs []string `json:"securityGroupIds"`
	}

	if !decodeJSON(w, r, &req) {
		return
	}

	g, err := h.lattice.UpdateResourceGateway(r.Context(), id, req.SecurityGroupIDs)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, resourceGatewayToWire(g))
}

func (h *Handler) deleteResourceGateway(w http.ResponseWriter, r *http.Request, id string) {
	if err := h.lattice.DeleteResourceGateway(r.Context(), id); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, struct{}{})
}

func (h *Handler) listResourceGateways(w http.ResponseWriter, r *http.Request) {
	gs, err := h.lattice.ListResourceGateways(r.Context())
	if err != nil {
		writeErr(w, err)

		return
	}

	items := make([]wireResourceGateway, 0, len(gs))
	for i := range gs {
		items = append(items, resourceGatewayToWire(&gs[i]))
	}

	writeJSON(w, map[string]any{"items": items})
}

// --- resource endpoint associations ---

func (h *Handler) serveResourceEndpointAssociations(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 {
		if r.Method != http.MethodGet {
			methodNotAllowed(w)

			return
		}

		as, err := h.lattice.ListResourceEndpointAssociations(r.Context())
		if err != nil {
			writeErr(w, err)

			return
		}

		items := make([]map[string]any, 0, len(as))
		for i := range as {
			items = append(items, map[string]any{"id": as[i].ID, "arn": as[i].ARN})
		}

		writeJSON(w, map[string]any{"items": items})

		return
	}

	if r.Method != http.MethodDelete {
		methodNotAllowed(w)

		return
	}

	if err := h.lattice.DeleteResourceEndpointAssociation(r.Context(), rest[0]); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, struct{}{})
}
