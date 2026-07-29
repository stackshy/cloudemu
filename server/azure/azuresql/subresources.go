package azuresql

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

// ---- capability accessors ----

func (h *Handler) firewallRules() (rdsdriver.FirewallRules, bool) {
	c, ok := h.db.(rdsdriver.FirewallRules)
	return c, ok
}

func (h *Handler) vnetRules() (rdsdriver.VNetRules, bool) {
	c, ok := h.db.(rdsdriver.VNetRules)
	return c, ok
}

func (h *Handler) elasticPools() (rdsdriver.ElasticPools, bool) {
	c, ok := h.db.(rdsdriver.ElasticPools)
	return c, ok
}

func (h *Handler) failoverGroups() (rdsdriver.FailoverGroups, bool) {
	c, ok := h.db.(rdsdriver.FailoverGroups)
	return c, ok
}

func (h *Handler) aadAdmins() (rdsdriver.AADAdmins, bool) {
	c, ok := h.db.(rdsdriver.AADAdmins)
	return c, ok
}

func writeUnsupported(w http.ResponseWriter, what string) {
	azurearm.WriteError(w, http.StatusBadRequest, "OperationNotSupported", what+" is not supported by this driver")
}

func childID(rp *azurearm.ResourcePath, subType, name string) string {
	return armServerID(rp.Subscription, rp.ResourceGroup, rp.ResourceName) + "/" + subType + "/" + name
}

// ---- Firewall rules ----

type armFirewallRule struct {
	ID         string              `json:"id,omitempty"`
	Name       string              `json:"name,omitempty"`
	Type       string              `json:"type,omitempty"`
	Properties *armFirewallRuleCfg `json:"properties,omitempty"`
}

type armFirewallRuleCfg struct {
	StartIPAddress string `json:"startIpAddress,omitempty"`
	EndIPAddress   string `json:"endIpAddress,omitempty"`
}

//nolint:dupl // mirrors the sibling sub-resource handler by design.
func (h *Handler) serveFirewallRule(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	fw, ok := h.firewallRules()
	if !ok {
		writeUnsupported(w, "firewallRules")
		return
	}

	if rp.SubResourceName == "" {
		h.getOrListFirewall(w, r, rp, fw, true)
		return
	}

	switch r.Method {
	case http.MethodPut:
		h.putFirewallRule(w, r, rp, fw)
	case http.MethodGet:
		h.getOrListFirewall(w, r, rp, fw, false)
	case http.MethodDelete:
		if err := fw.DeleteFirewallRule(r.Context(), rp.ResourceName, rp.SubResourceName); err != nil {
			azurearm.WriteCErr(w, err)
			return
		}

		w.WriteHeader(http.StatusOK)
	default:
		writeMethodNotAllowed(w)
	}
}

func (*Handler) putFirewallRule(
	w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, fw rdsdriver.FirewallRules,
) {
	var body armFirewallRule
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	cfg := rdsdriver.FirewallRuleConfig{Server: rp.ResourceName, Name: rp.SubResourceName}
	if body.Properties != nil {
		cfg.StartIPAddress = body.Properties.StartIPAddress
		cfg.EndIPAddress = body.Properties.EndIPAddress
	}

	out, err := fw.CreateFirewallRule(r.Context(), cfg)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMFirewallRule(out, rp))
}

//nolint:dupl // mirrors the sibling get/list handler by design.
func (*Handler) getOrListFirewall(
	w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, fw rdsdriver.FirewallRules, list bool,
) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}

	if !list {
		out, err := fw.GetFirewallRule(r.Context(), rp.ResourceName, rp.SubResourceName)
		if err != nil {
			azurearm.WriteCErr(w, err)
			return
		}

		azurearm.WriteJSON(w, http.StatusOK, toARMFirewallRule(out, rp))

		return
	}

	items, err := fw.ListFirewallRules(r.Context(), rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := make([]armFirewallRule, 0, len(items))
	for i := range items {
		out = append(out, toARMFirewallRule(&items[i], rp))
	}

	azurearm.WriteJSON(w, http.StatusOK, armList[armFirewallRule]{Value: out})
}

func toARMFirewallRule(fw *rdsdriver.FirewallRule, rp *azurearm.ResourcePath) armFirewallRule {
	return armFirewallRule{
		ID:         childID(rp, subFirewallRules, fw.Name),
		Name:       fw.Name,
		Type:       providerName + "/" + resourceServers + "/" + subFirewallRules,
		Properties: &armFirewallRuleCfg{StartIPAddress: fw.StartIPAddress, EndIPAddress: fw.EndIPAddress},
	}
}

// ---- Virtual network rules ----

type armVNetRule struct {
	ID         string          `json:"id,omitempty"`
	Name       string          `json:"name,omitempty"`
	Type       string          `json:"type,omitempty"`
	Properties *armVNetRuleCfg `json:"properties,omitempty"`
}

type armVNetRuleCfg struct {
	VirtualNetworkSubnetID           string `json:"virtualNetworkSubnetId,omitempty"`
	IgnoreMissingVnetServiceEndpoint bool   `json:"ignoreMissingVnetServiceEndpoint,omitempty"`
	State                            string `json:"state,omitempty"`
}

//nolint:dupl // mirrors the sibling sub-resource handler by design.
func (h *Handler) serveVNetRule(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	vr, ok := h.vnetRules()
	if !ok {
		writeUnsupported(w, "virtualNetworkRules")
		return
	}

	if rp.SubResourceName == "" {
		h.getOrListVNet(w, r, rp, vr, true)
		return
	}

	switch r.Method {
	case http.MethodPut:
		h.putVNetRule(w, r, rp, vr)
	case http.MethodGet:
		h.getOrListVNet(w, r, rp, vr, false)
	case http.MethodDelete:
		if err := vr.DeleteVNetRule(r.Context(), rp.ResourceName, rp.SubResourceName); err != nil {
			azurearm.WriteCErr(w, err)
			return
		}

		w.WriteHeader(http.StatusOK)
	default:
		writeMethodNotAllowed(w)
	}
}

func (*Handler) putVNetRule(
	w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, vr rdsdriver.VNetRules,
) {
	var body armVNetRule
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	cfg := rdsdriver.VNetRuleConfig{Server: rp.ResourceName, Name: rp.SubResourceName}
	if body.Properties != nil {
		cfg.SubnetID = body.Properties.VirtualNetworkSubnetID
		cfg.IgnoreMissingEndpoint = body.Properties.IgnoreMissingVnetServiceEndpoint
	}

	out, err := vr.CreateVNetRule(r.Context(), cfg)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMVNetRule(out, rp))
}

//nolint:dupl // mirrors the sibling get/list handler by design.
func (*Handler) getOrListVNet(
	w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, vr rdsdriver.VNetRules, list bool,
) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}

	if !list {
		out, err := vr.GetVNetRule(r.Context(), rp.ResourceName, rp.SubResourceName)
		if err != nil {
			azurearm.WriteCErr(w, err)
			return
		}

		azurearm.WriteJSON(w, http.StatusOK, toARMVNetRule(out, rp))

		return
	}

	items, err := vr.ListVNetRules(r.Context(), rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := make([]armVNetRule, 0, len(items))
	for i := range items {
		out = append(out, toARMVNetRule(&items[i], rp))
	}

	azurearm.WriteJSON(w, http.StatusOK, armList[armVNetRule]{Value: out})
}

func toARMVNetRule(vr *rdsdriver.VNetRule, rp *azurearm.ResourcePath) armVNetRule {
	return armVNetRule{
		ID:   childID(rp, subVNetRules, vr.Name),
		Name: vr.Name,
		Type: providerName + "/" + resourceServers + "/" + subVNetRules,
		Properties: &armVNetRuleCfg{
			VirtualNetworkSubnetID:           vr.SubnetID,
			IgnoreMissingVnetServiceEndpoint: vr.IgnoreMissingEndpoint,
			State:                            vr.State,
		},
	}
}

// ---- Elastic pools ----

type armElasticPool struct {
	ID         string             `json:"id,omitempty"`
	Name       string             `json:"name,omitempty"`
	Type       string             `json:"type,omitempty"`
	Location   string             `json:"location,omitempty"`
	SKU        *armSKU            `json:"sku,omitempty"`
	Properties *armElasticPoolCfg `json:"properties,omitempty"`
}

type armElasticPoolCfg struct {
	MaxSizeBytes       int64                 `json:"maxSizeBytes,omitempty"`
	State              string                `json:"state,omitempty"`
	PerDatabaseSetting *armPerDatabaseSeters `json:"perDatabaseSettings,omitempty"`
}

type armPerDatabaseSeters struct {
	MinCapacity float64 `json:"minCapacity,omitempty"`
	MaxCapacity float64 `json:"maxCapacity,omitempty"`
}

func (h *Handler) serveElasticPool(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	ep, ok := h.elasticPools()
	if !ok {
		writeUnsupported(w, "elasticPools")
		return
	}

	if rp.SubResourceName == "" {
		h.getOrListPool(w, r, rp, ep, true)
		return
	}

	switch r.Method {
	case http.MethodPut, http.MethodPatch:
		h.putElasticPool(w, r, rp, ep)
	case http.MethodGet:
		h.getOrListPool(w, r, rp, ep, false)
	case http.MethodDelete:
		if err := ep.DeleteElasticPool(r.Context(), rp.ResourceName, rp.SubResourceName); err != nil {
			azurearm.WriteCErr(w, err)
			return
		}

		w.WriteHeader(http.StatusOK)
	default:
		writeMethodNotAllowed(w)
	}
}

func (*Handler) putElasticPool(
	w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, ep rdsdriver.ElasticPools,
) {
	var body armElasticPool
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	cfg := rdsdriver.ElasticPoolConfig{Server: rp.ResourceName, Name: rp.SubResourceName, Location: body.Location}
	if body.SKU != nil {
		cfg.SKUName = body.SKU.Name
		cfg.SKUTier = body.SKU.Tier
	}

	if body.Properties != nil {
		cfg.MaxSizeBytes = body.Properties.MaxSizeBytes
		if body.Properties.PerDatabaseSetting != nil {
			cfg.MinCapacity = body.Properties.PerDatabaseSetting.MinCapacity
			cfg.MaxCapacity = body.Properties.PerDatabaseSetting.MaxCapacity
		}
	}

	out, err := ep.CreateElasticPool(r.Context(), cfg)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMElasticPool(out, rp))
}

//nolint:dupl // mirrors the sibling get/list handler by design.
func (*Handler) getOrListPool(
	w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, ep rdsdriver.ElasticPools, list bool,
) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}

	if !list {
		out, err := ep.GetElasticPool(r.Context(), rp.ResourceName, rp.SubResourceName)
		if err != nil {
			azurearm.WriteCErr(w, err)
			return
		}

		azurearm.WriteJSON(w, http.StatusOK, toARMElasticPool(out, rp))

		return
	}

	items, err := ep.ListElasticPools(r.Context(), rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := make([]armElasticPool, 0, len(items))
	for i := range items {
		out = append(out, toARMElasticPool(&items[i], rp))
	}

	azurearm.WriteJSON(w, http.StatusOK, armList[armElasticPool]{Value: out})
}

func toARMElasticPool(ep *rdsdriver.ElasticPool, rp *azurearm.ResourcePath) armElasticPool {
	return armElasticPool{
		ID:       childID(rp, subElasticPools, ep.Name),
		Name:     ep.Name,
		Type:     providerName + "/" + resourceServers + "/" + subElasticPools,
		Location: ep.Location,
		SKU:      &armSKU{Name: ep.SKUName, Tier: ep.SKUTier},
		Properties: &armElasticPoolCfg{
			MaxSizeBytes:       ep.MaxSizeBytes,
			State:              ep.State,
			PerDatabaseSetting: &armPerDatabaseSeters{MinCapacity: ep.MinCapacity, MaxCapacity: ep.MaxCapacity},
		},
	}
}

// ---- Failover groups ----

type armFailoverGroup struct {
	ID         string               `json:"id,omitempty"`
	Name       string               `json:"name,omitempty"`
	Type       string               `json:"type,omitempty"`
	Properties *armFailoverGroupCfg `json:"properties,omitempty"`
}

type armFailoverGroupCfg struct {
	ReadWriteEndpoint *armRWEndpoint `json:"readWriteEndpoint,omitempty"`
	PartnerServers    []armPartner   `json:"partnerServers,omitempty"`
	Databases         []string       `json:"databases,omitempty"`
	ReplicationRole   string         `json:"replicationRole,omitempty"`
	ReplicationState  string         `json:"replicationState,omitempty"`
}

type armRWEndpoint struct {
	FailoverPolicy                         string `json:"failoverPolicy,omitempty"`
	FailoverWithDataLossGracePeriodMinutes int32  `json:"failoverWithDataLossGracePeriodMinutes,omitempty"`
}

type armPartner struct {
	ID string `json:"id,omitempty"`
}

func (h *Handler) serveFailoverGroup(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	fg, ok := h.failoverGroups()
	if !ok {
		writeUnsupported(w, "failoverGroups")
		return
	}

	if rp.SubResourceName == "" {
		h.getOrListFG(w, r, rp, fg, true)
		return
	}

	switch r.Method {
	case http.MethodPut, http.MethodPatch:
		h.putFailoverGroup(w, r, rp, fg)
	case http.MethodGet:
		h.getOrListFG(w, r, rp, fg, false)
	case http.MethodPost: // .../failoverGroups/{name}/failover (and force/tryPlanned variants)
		h.doFailover(w, r, rp, fg)
	case http.MethodDelete:
		if err := fg.DeleteFailoverGroup(r.Context(), rp.ResourceName, rp.SubResourceName); err != nil {
			azurearm.WriteCErr(w, err)
			return
		}

		w.WriteHeader(http.StatusOK)
	default:
		writeMethodNotAllowed(w)
	}
}

func (*Handler) putFailoverGroup(
	w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, fg rdsdriver.FailoverGroups,
) {
	var body armFailoverGroup
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	cfg := rdsdriver.FailoverGroupConfig{Server: rp.ResourceName, Name: rp.SubResourceName}
	if body.Properties != nil {
		cfg.Databases = body.Properties.Databases

		if body.Properties.ReadWriteEndpoint != nil {
			cfg.FailoverPolicy = body.Properties.ReadWriteEndpoint.FailoverPolicy
			cfg.GracePeriodMinutes = body.Properties.ReadWriteEndpoint.FailoverWithDataLossGracePeriodMinutes
		}

		for _, p := range body.Properties.PartnerServers {
			cfg.PartnerServers = append(cfg.PartnerServers, p.ID)
		}
	}

	out, err := fg.CreateFailoverGroup(r.Context(), cfg)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMFailoverGroup(out, rp))
}

func (*Handler) doFailover(
	w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, fg rdsdriver.FailoverGroups,
) {
	out, err := fg.FailoverFailoverGroup(r.Context(), rp.ResourceName, rp.SubResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMFailoverGroup(out, rp))
}

//nolint:dupl // mirrors the sibling get/list handler by design.
func (*Handler) getOrListFG(
	w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, fg rdsdriver.FailoverGroups, list bool,
) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}

	if !list {
		out, err := fg.GetFailoverGroup(r.Context(), rp.ResourceName, rp.SubResourceName)
		if err != nil {
			azurearm.WriteCErr(w, err)
			return
		}

		azurearm.WriteJSON(w, http.StatusOK, toARMFailoverGroup(out, rp))

		return
	}

	items, err := fg.ListFailoverGroups(r.Context(), rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := make([]armFailoverGroup, 0, len(items))
	for i := range items {
		out = append(out, toARMFailoverGroup(&items[i], rp))
	}

	azurearm.WriteJSON(w, http.StatusOK, armList[armFailoverGroup]{Value: out})
}

func toARMFailoverGroup(fg *rdsdriver.FailoverGroup, rp *azurearm.ResourcePath) armFailoverGroup {
	partners := make([]armPartner, 0, len(fg.PartnerServers))
	for _, id := range fg.PartnerServers {
		partners = append(partners, armPartner{ID: id})
	}

	return armFailoverGroup{
		ID:   childID(rp, subFailoverGroups, fg.Name),
		Name: fg.Name,
		Type: providerName + "/" + resourceServers + "/" + subFailoverGroups,
		Properties: &armFailoverGroupCfg{
			ReadWriteEndpoint: &armRWEndpoint{
				FailoverPolicy:                         fg.FailoverPolicy,
				FailoverWithDataLossGracePeriodMinutes: fg.GracePeriodMinutes,
			},
			PartnerServers:   partners,
			Databases:        fg.Databases,
			ReplicationRole:  fg.ReplicationRole,
			ReplicationState: "CATCH_UP",
		},
	}
}

// ---- Azure AD administrator ----

type armAADAdmin struct {
	ID         string          `json:"id,omitempty"`
	Name       string          `json:"name,omitempty"`
	Type       string          `json:"type,omitempty"`
	Properties *armAADAdminCfg `json:"properties,omitempty"`
}

type armAADAdminCfg struct {
	AdministratorType string `json:"administratorType,omitempty"`
	Login             string `json:"login,omitempty"`
	Sid               string `json:"sid,omitempty"`
	TenantID          string `json:"tenantId,omitempty"`
}

func (h *Handler) serveAADAdmin(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	aad, ok := h.aadAdmins()
	if !ok {
		writeUnsupported(w, "administrators")
		return
	}

	if rp.SubResourceName == "" {
		h.listAADAdmins(w, r, rp, aad)
		return
	}

	switch r.Method {
	case http.MethodPut:
		h.putAADAdmin(w, r, rp, aad)
	case http.MethodGet:
		out, err := aad.GetAADAdmin(r.Context(), rp.ResourceName, rp.SubResourceName)
		if err != nil {
			azurearm.WriteCErr(w, err)
			return
		}

		azurearm.WriteJSON(w, http.StatusOK, toARMAADAdmin(out, rp))
	case http.MethodDelete:
		if err := aad.DeleteAADAdmin(r.Context(), rp.ResourceName, rp.SubResourceName); err != nil {
			azurearm.WriteCErr(w, err)
			return
		}

		w.WriteHeader(http.StatusOK)
	default:
		writeMethodNotAllowed(w)
	}
}

func (*Handler) putAADAdmin(
	w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, aad rdsdriver.AADAdmins,
) {
	var body armAADAdmin
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	cfg := rdsdriver.AADAdminConfig{Server: rp.ResourceName}
	if body.Properties != nil {
		cfg.Login = body.Properties.Login
		cfg.SID = body.Properties.Sid
		cfg.TenantID = body.Properties.TenantID
	}

	out, err := aad.SetAADAdmin(r.Context(), cfg)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMAADAdmin(out, rp))
}

func (*Handler) listAADAdmins(
	w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, aad rdsdriver.AADAdmins,
) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}

	items, err := aad.ListAADAdmins(r.Context(), rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := make([]armAADAdmin, 0, len(items))
	for i := range items {
		out = append(out, toARMAADAdmin(&items[i], rp))
	}

	azurearm.WriteJSON(w, http.StatusOK, armList[armAADAdmin]{Value: out})
}

func toARMAADAdmin(a *rdsdriver.AADAdmin, rp *azurearm.ResourcePath) armAADAdmin {
	return armAADAdmin{
		ID:   childID(rp, subAdministrators, a.Name),
		Name: a.Name,
		Type: providerName + "/" + resourceServers + "/" + subAdministrators,
		Properties: &armAADAdminCfg{
			AdministratorType: aadAdminType,
			Login:             a.Login,
			Sid:               a.SID,
			TenantID:          a.TenantID,
		},
	}
}

const aadAdminType = "ActiveDirectory"
