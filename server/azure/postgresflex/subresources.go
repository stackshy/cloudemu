package postgresflex

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

// ---- ARM JSON shapes for child resources ----

type armDatabase struct {
	ID         string          `json:"id,omitempty"`
	Name       string          `json:"name,omitempty"`
	Type       string          `json:"type,omitempty"`
	Properties *armDatabaseCfg `json:"properties,omitempty"`
}

type armDatabaseCfg struct {
	Charset   string `json:"charset,omitempty"`
	Collation string `json:"collation,omitempty"`
}

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

type armConfiguration struct {
	ID         string        `json:"id,omitempty"`
	Name       string        `json:"name,omitempty"`
	Type       string        `json:"type,omitempty"`
	Properties *armConfigCfg `json:"properties,omitempty"`
}

type armConfigCfg struct {
	Value         string `json:"value,omitempty"`
	Source        string `json:"source,omitempty"`
	DataType      string `json:"dataType,omitempty"`
	DefaultValue  string `json:"defaultValue,omitempty"`
	AllowedValues string `json:"allowedValues,omitempty"`
}

func childResourceID(rp *azurearm.ResourcePath, subType, name string) string {
	base := azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, resourceFlexibleServers, rp.ResourceName)
	return base + "/" + subType + "/" + name
}

// ---- capability accessors ----

func (h *Handler) databases() (rdsdriver.Databases, bool) {
	d, ok := h.db.(rdsdriver.Databases)
	return d, ok
}

func (h *Handler) firewallRules() (rdsdriver.FirewallRules, bool) {
	f, ok := h.db.(rdsdriver.FirewallRules)
	return f, ok
}

func (h *Handler) configurations() (rdsdriver.Configurations, bool) {
	c, ok := h.db.(rdsdriver.Configurations)
	return c, ok
}

func writeUnsupported(w http.ResponseWriter, what string) {
	azurearm.WriteError(w, http.StatusBadRequest, "OperationNotSupported", what+" is not supported by this driver")
}

// ---- Databases ----

//nolint:dupl // mirrors the sibling sub-resource handler by design.
func (h *Handler) serveDatabase(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	db, ok := h.databases()
	if !ok {
		writeUnsupported(w, "databases")
		return
	}

	if rp.SubResourceName == "" {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w)
			return
		}

		h.listDatabases(w, r, rp, db)

		return
	}

	switch r.Method {
	case http.MethodPut:
		h.putDatabase(w, r, rp, db)
	case http.MethodGet:
		h.getDatabase(w, r, rp, db)
	case http.MethodDelete:
		h.deleteDatabase(w, r, rp, db)
	default:
		writeMethodNotAllowed(w)
	}
}

func (*Handler) putDatabase(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, db rdsdriver.Databases) {
	var body armDatabase
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	cfg := rdsdriver.DatabaseConfig{Server: rp.ResourceName, Name: rp.SubResourceName}
	if body.Properties != nil {
		cfg.Charset = body.Properties.Charset
		cfg.Collation = body.Properties.Collation
	}

	out, err := db.CreateDatabase(r.Context(), cfg)
	if err != nil {
		existing, getErr := db.GetDatabase(r.Context(), rp.ResourceName, rp.SubResourceName)
		if getErr != nil {
			azurearm.WriteCErr(w, err)
			return
		}

		out = existing
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMDatabase(out, rp))
}

func (*Handler) getDatabase(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, db rdsdriver.Databases) {
	out, err := db.GetDatabase(r.Context(), rp.ResourceName, rp.SubResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMDatabase(out, rp))
}

func (*Handler) deleteDatabase(
	w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, db rdsdriver.Databases,
) {
	if err := db.DeleteDatabase(r.Context(), rp.ResourceName, rp.SubResourceName); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
}

//nolint:dupl // mirrors the sibling list handler by design.
func (*Handler) listDatabases(
	w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, db rdsdriver.Databases,
) {
	items, err := db.ListDatabases(r.Context(), rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := make([]armDatabase, 0, len(items))
	for i := range items {
		out = append(out, toARMDatabase(&items[i], rp))
	}

	azurearm.WriteJSON(w, http.StatusOK, armList[armDatabase]{Value: out})
}

func toARMDatabase(db *rdsdriver.Database, rp *azurearm.ResourcePath) armDatabase {
	return armDatabase{
		ID:         childResourceID(rp, subDatabases, db.Name),
		Name:       db.Name,
		Type:       providerName + "/" + resourceFlexibleServers + "/" + subDatabases,
		Properties: &armDatabaseCfg{Charset: db.Charset, Collation: db.Collation},
	}
}

// ---- Firewall rules ----

//nolint:dupl // mirrors the sibling sub-resource handler by design.
func (h *Handler) serveFirewallRule(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	fw, ok := h.firewallRules()
	if !ok {
		writeUnsupported(w, "firewallRules")
		return
	}

	if rp.SubResourceName == "" {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w)
			return
		}

		h.listFirewallRules(w, r, rp, fw)

		return
	}

	switch r.Method {
	case http.MethodPut:
		h.putFirewallRule(w, r, rp, fw)
	case http.MethodGet:
		h.getFirewallRule(w, r, rp, fw)
	case http.MethodDelete:
		h.deleteFirewallRule(w, r, rp, fw)
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

func (*Handler) getFirewallRule(
	w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, fw rdsdriver.FirewallRules,
) {
	out, err := fw.GetFirewallRule(r.Context(), rp.ResourceName, rp.SubResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMFirewallRule(out, rp))
}

func (*Handler) deleteFirewallRule(
	w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, fw rdsdriver.FirewallRules,
) {
	if err := fw.DeleteFirewallRule(r.Context(), rp.ResourceName, rp.SubResourceName); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
}

//nolint:dupl // mirrors the sibling list handler by design.
func (*Handler) listFirewallRules(
	w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, fw rdsdriver.FirewallRules,
) {
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
		ID:   childResourceID(rp, subFirewallRules, fw.Name),
		Name: fw.Name,
		Type: providerName + "/" + resourceFlexibleServers + "/" + subFirewallRules,
		Properties: &armFirewallRuleCfg{
			StartIPAddress: fw.StartIPAddress,
			EndIPAddress:   fw.EndIPAddress,
		},
	}
}

// ---- Configurations ----

func (h *Handler) serveConfiguration(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	cf, ok := h.configurations()
	if !ok {
		writeUnsupported(w, "configurations")
		return
	}

	if rp.SubResourceName == "" {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w)
			return
		}

		h.listConfigurations(w, r, rp, cf)

		return
	}

	switch r.Method {
	case http.MethodPut, http.MethodPatch:
		h.putConfiguration(w, r, rp, cf)
	case http.MethodGet:
		h.getConfiguration(w, r, rp, cf)
	default:
		writeMethodNotAllowed(w)
	}
}

func (*Handler) putConfiguration(
	w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, cf rdsdriver.Configurations,
) {
	var body armConfiguration
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	cfg := rdsdriver.ConfigurationConfig{Server: rp.ResourceName, Name: rp.SubResourceName}
	if body.Properties != nil {
		cfg.Value = body.Properties.Value
	}

	out, err := cf.SetConfiguration(r.Context(), cfg)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMConfiguration(out, rp))
}

func (*Handler) getConfiguration(
	w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, cf rdsdriver.Configurations,
) {
	out, err := cf.GetConfiguration(r.Context(), rp.ResourceName, rp.SubResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMConfiguration(out, rp))
}

//nolint:dupl // mirrors the sibling list handler by design.
func (*Handler) listConfigurations(
	w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, cf rdsdriver.Configurations,
) {
	items, err := cf.ListConfigurations(r.Context(), rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := make([]armConfiguration, 0, len(items))
	for i := range items {
		out = append(out, toARMConfiguration(&items[i], rp))
	}

	azurearm.WriteJSON(w, http.StatusOK, armList[armConfiguration]{Value: out})
}

func toARMConfiguration(c *rdsdriver.Configuration, rp *azurearm.ResourcePath) armConfiguration {
	return armConfiguration{
		ID:   childResourceID(rp, subConfigurations, c.Name),
		Name: c.Name,
		Type: providerName + "/" + resourceFlexibleServers + "/" + subConfigurations,
		Properties: &armConfigCfg{
			Value:         c.Value,
			Source:        c.Source,
			DataType:      c.DataType,
			DefaultValue:  c.DefaultValue,
			AllowedValues: c.AllowedValues,
		},
	}
}
