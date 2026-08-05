package vpclattice

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/vpclattice/driver"
)

// --- SN ↔ VPC ---

type wireSNVpcAssoc struct {
	Arn                string   `json:"arn,omitempty"`
	ID                 string   `json:"id,omitempty"`
	CreatedBy          string   `json:"createdBy,omitempty"`
	ServiceNetworkArn  string   `json:"serviceNetworkArn,omitempty"`
	ServiceNetworkID   string   `json:"serviceNetworkId,omitempty"`
	ServiceNetworkName string   `json:"serviceNetworkName,omitempty"`
	VpcID              string   `json:"vpcId,omitempty"`
	SecurityGroupIDs   []string `json:"securityGroupIds,omitempty"`
	PrivateDNSEnabled  bool     `json:"privateDnsEnabled"`
	Status             string   `json:"status,omitempty"`
	CreatedAt          string   `json:"createdAt,omitempty"`
	LastUpdatedAt      string   `json:"lastUpdatedAt,omitempty"`
}

func snVpcToWire(a *driver.SNVpcAssociation) wireSNVpcAssoc {
	return wireSNVpcAssoc{
		Arn: a.ARN, ID: a.ID, CreatedBy: a.CreatedBy,
		ServiceNetworkArn: a.ServiceNetworkARN, ServiceNetworkID: a.ServiceNetworkID,
		ServiceNetworkName: a.ServiceNetworkName, VpcID: a.VpcID,
		SecurityGroupIDs: a.SecurityGroupIDs, PrivateDNSEnabled: a.PrivateDNSEnabled,
		Status: a.Status, CreatedAt: a.CreatedAt, LastUpdatedAt: a.LastUpdatedAt,
	}
}

func (h *Handler) serveSNVpcAssociations(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 {
		routeCollection(w, r, h.createSNVpcAssoc, h.listSNVpcAssocs)

		return
	}

	routeByID(w, r, rest[0], h.getSNVpcAssoc, h.updateSNVpcAssoc, h.deleteSNVpcAssoc)
}

func (h *Handler) createSNVpcAssoc(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ServiceNetworkIdentifier string            `json:"serviceNetworkIdentifier"`
		VpcIdentifier            string            `json:"vpcIdentifier"`
		SecurityGroupIDs         []string          `json:"securityGroupIds"`
		PrivateDNSEnabled        bool              `json:"privateDnsEnabled"`
		Tags                     map[string]string `json:"tags"`
	}

	if !decodeJSON(w, r, &req) {
		return
	}

	a, err := h.lattice.CreateSNVpcAssociation(r.Context(), &driver.CreateSNVpcAssociationInput{
		ServiceNetworkID: req.ServiceNetworkIdentifier, VpcID: req.VpcIdentifier,
		SecurityGroupIDs: req.SecurityGroupIDs, PrivateDNSEnabled: req.PrivateDNSEnabled, Tags: req.Tags,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, snVpcToWire(a))
}

func (h *Handler) getSNVpcAssoc(w http.ResponseWriter, r *http.Request, id string) {
	a, err := h.lattice.GetSNVpcAssociation(r.Context(), id)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, snVpcToWire(a))
}

func (h *Handler) updateSNVpcAssoc(w http.ResponseWriter, r *http.Request, id string) {
	var req struct {
		SecurityGroupIDs []string `json:"securityGroupIds"`
	}

	if !decodeJSON(w, r, &req) {
		return
	}

	a, err := h.lattice.UpdateSNVpcAssociation(r.Context(), id, req.SecurityGroupIDs)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, snVpcToWire(a))
}

func (h *Handler) deleteSNVpcAssoc(w http.ResponseWriter, r *http.Request, id string) {
	if err := h.lattice.DeleteSNVpcAssociation(r.Context(), id); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, struct{}{})
}

func (h *Handler) listSNVpcAssocs(w http.ResponseWriter, r *http.Request) {
	as, err := h.lattice.ListSNVpcAssociations(r.Context())
	if err != nil {
		writeErr(w, err)

		return
	}

	items := make([]wireSNVpcAssoc, 0, len(as))
	for i := range as {
		items = append(items, snVpcToWire(&as[i]))
	}

	writeJSON(w, map[string]any{"items": items})
}

func (h *Handler) serveSNVpcEndpointAssociations(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) != 0 || r.Method != http.MethodGet {
		methodNotAllowed(w)

		return
	}

	as, err := h.lattice.ListSNVpcEndpointAssociations(r.Context(), r.URL.Query().Get("serviceNetworkIdentifier"))
	if err != nil {
		writeErr(w, err)

		return
	}

	items := make([]wireSNVpcAssoc, 0, len(as))
	for i := range as {
		items = append(items, snVpcToWire(&as[i]))
	}

	writeJSON(w, map[string]any{"items": items})
}

// --- SN ↔ Service ---

type wireSNSvcAssoc struct {
	Arn                string        `json:"arn,omitempty"`
	ID                 string        `json:"id,omitempty"`
	CreatedBy          string        `json:"createdBy,omitempty"`
	CustomDomainName   string        `json:"customDomainName,omitempty"`
	DNSEntry           *wireDNSEntry `json:"dnsEntry,omitempty"`
	ServiceArn         string        `json:"serviceArn,omitempty"`
	ServiceID          string        `json:"serviceId,omitempty"`
	ServiceName        string        `json:"serviceName,omitempty"`
	ServiceNetworkArn  string        `json:"serviceNetworkArn,omitempty"`
	ServiceNetworkID   string        `json:"serviceNetworkId,omitempty"`
	ServiceNetworkName string        `json:"serviceNetworkName,omitempty"`
	Status             string        `json:"status,omitempty"`
	CreatedAt          string        `json:"createdAt,omitempty"`
}

func snSvcToWire(a *driver.SNServiceAssociation) wireSNSvcAssoc {
	w := wireSNSvcAssoc{
		Arn: a.ARN, ID: a.ID, CreatedBy: a.CreatedBy, CustomDomainName: a.CustomDomainName,
		ServiceArn: a.ServiceARN, ServiceID: a.ServiceID, ServiceName: a.ServiceName,
		ServiceNetworkArn: a.ServiceNetworkARN, ServiceNetworkID: a.ServiceNetworkID,
		ServiceNetworkName: a.ServiceNetworkName, Status: a.Status, CreatedAt: a.CreatedAt,
	}
	if a.DNSName != "" {
		w.DNSEntry = &wireDNSEntry{DomainName: a.DNSName, HostedZoneID: a.HostedZoneID}
	}

	return w
}

func (h *Handler) serveSNServiceAssociations(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 {
		routeCollection(w, r, h.createSNSvcAssoc, h.listSNSvcAssocs)

		return
	}

	routeByID(w, r, rest[0], h.getSNSvcAssoc, nil, h.deleteSNSvcAssoc)
}

func (h *Handler) createSNSvcAssoc(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ServiceIdentifier        string            `json:"serviceIdentifier"`
		ServiceNetworkIdentifier string            `json:"serviceNetworkIdentifier"`
		Tags                     map[string]string `json:"tags"`
	}

	if !decodeJSON(w, r, &req) {
		return
	}

	a, err := h.lattice.CreateSNServiceAssociation(r.Context(), req.ServiceNetworkIdentifier, req.ServiceIdentifier, req.Tags)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, snSvcToWire(a))
}

func (h *Handler) getSNSvcAssoc(w http.ResponseWriter, r *http.Request, id string) {
	a, err := h.lattice.GetSNServiceAssociation(r.Context(), id)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, snSvcToWire(a))
}

func (h *Handler) deleteSNSvcAssoc(w http.ResponseWriter, r *http.Request, id string) {
	if err := h.lattice.DeleteSNServiceAssociation(r.Context(), id); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, struct{}{})
}

func (h *Handler) listSNSvcAssocs(w http.ResponseWriter, r *http.Request) {
	as, err := h.lattice.ListSNServiceAssociations(r.Context())
	if err != nil {
		writeErr(w, err)

		return
	}

	items := make([]wireSNSvcAssoc, 0, len(as))
	for i := range as {
		items = append(items, snSvcToWire(&as[i]))
	}

	writeJSON(w, map[string]any{"items": items})
}

// --- SN ↔ Resource ---

type wireSNResAssoc struct {
	Arn                       string `json:"arn,omitempty"`
	ID                        string `json:"id,omitempty"`
	CreatedBy                 string `json:"createdBy,omitempty"`
	ResourceConfigurationArn  string `json:"resourceConfigurationArn,omitempty"`
	ResourceConfigurationID   string `json:"resourceConfigurationId,omitempty"`
	ResourceConfigurationName string `json:"resourceConfigurationName,omitempty"`
	ServiceNetworkArn         string `json:"serviceNetworkArn,omitempty"`
	ServiceNetworkID          string `json:"serviceNetworkId,omitempty"`
	ServiceNetworkName        string `json:"serviceNetworkName,omitempty"`
	PrivateDNSEnabled         bool   `json:"privateDnsEnabled"`
	Status                    string `json:"status,omitempty"`
	CreatedAt                 string `json:"createdAt,omitempty"`
	LastUpdatedAt             string `json:"lastUpdatedAt,omitempty"`
}

func snResToWire(a *driver.SNResourceAssociation) wireSNResAssoc {
	return wireSNResAssoc{
		Arn: a.ARN, ID: a.ID, CreatedBy: a.CreatedBy,
		ResourceConfigurationArn: a.ResourceConfigurationARN, ResourceConfigurationID: a.ResourceConfigurationID,
		ResourceConfigurationName: a.ResourceConfigurationName, ServiceNetworkArn: a.ServiceNetworkARN,
		ServiceNetworkID: a.ServiceNetworkID, ServiceNetworkName: a.ServiceNetworkName,
		PrivateDNSEnabled: a.PrivateDNSEnabled, Status: a.Status,
		CreatedAt: a.CreatedAt, LastUpdatedAt: a.LastUpdatedAt,
	}
}

func (h *Handler) serveSNResourceAssociations(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 {
		routeCollection(w, r, h.createSNResAssoc, h.listSNResAssocs)

		return
	}

	routeByID(w, r, rest[0], h.getSNResAssoc, nil, h.deleteSNResAssoc)
}

func (h *Handler) createSNResAssoc(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResourceConfigurationIdentifier string            `json:"resourceConfigurationIdentifier"`
		ServiceNetworkIdentifier        string            `json:"serviceNetworkIdentifier"`
		PrivateDNSEnabled               bool              `json:"privateDnsEnabled"`
		Tags                            map[string]string `json:"tags"`
	}

	if !decodeJSON(w, r, &req) {
		return
	}

	a, err := h.lattice.CreateSNResourceAssociation(r.Context(),
		req.ServiceNetworkIdentifier, req.ResourceConfigurationIdentifier, req.PrivateDNSEnabled, req.Tags)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, snResToWire(a))
}

func (h *Handler) getSNResAssoc(w http.ResponseWriter, r *http.Request, id string) {
	a, err := h.lattice.GetSNResourceAssociation(r.Context(), id)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, snResToWire(a))
}

func (h *Handler) deleteSNResAssoc(w http.ResponseWriter, r *http.Request, id string) {
	if err := h.lattice.DeleteSNResourceAssociation(r.Context(), id); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, struct{}{})
}

func (h *Handler) listSNResAssocs(w http.ResponseWriter, r *http.Request) {
	as, err := h.lattice.ListSNResourceAssociations(r.Context())
	if err != nil {
		writeErr(w, err)

		return
	}

	items := make([]wireSNResAssoc, 0, len(as))
	for i := range as {
		items = append(items, snResToWire(&as[i]))
	}

	writeJSON(w, map[string]any{"items": items})
}
