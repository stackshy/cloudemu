package sql

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

// Documented defaults for the database retention singletons a new database
// carries before anything configures them.
const (
	defaultSTRRetentionDays = 7
	defaultSTRDiffInterval  = 12
	stateDisabled           = "Disabled"
	zeroDuration            = "PT0S"
	singletonDefault        = "default"
	singletonDefaultTitle   = "Default"
	propState               = "state"
	propRetentionDays       = "retentionDays"
)

// serveDatabaseChild answers .../databases/{d}/{child}[/{name}]. TDE is
// modeled; the always-present singletons below are not, so a read returns
// their documented default and a write is 501. Nothing here reaches the
// database itself.
func (h *Handler) serveDatabaseChild(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if azurearm.TooDeep(w, r, rp, databaseChildMaxDepth) {
		return
	}

	child := rp.SubResourceAction

	switch {
	case child == subTDE:
		h.serveTDE(w, r, rp)
	case strings.EqualFold(child, subSTR), strings.EqualFold(child, subLTR):
		h.serveRetention(w, r, rp)
	case strings.EqualFold(child, "replicationLinks"):
		// cloudemu has no geo-replication, so a database has no links.
		kind := azurearm.DeferredCollection
		if rp.Rest != "" {
			kind = azurearm.DeferredItem
		}

		azurearm.ServeDeferred(w, r, rp, kind, nil)
	default:
		h.serveDatabaseSingleton(w, r, rp)
	}
}

// serveDatabaseSingleton answers the always-present database singletons
// cloudemu does not model: reads return the documented default.
func (h *Handler) serveDatabaseSingleton(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	name, props, ok := databaseSingleton(rp.SubResourceAction)
	if !ok {
		azurearm.WriteUnknownType(w, r, rp)
		return
	}

	switch {
	case rp.Rest == "":
		azurearm.ServeDeferred(w, r, rp, azurearm.DeferredCollection, nil)
	case !strings.EqualFold(rp.Rest, name):
		azurearm.ServeDeferred(w, r, rp, azurearm.DeferredItem, nil)
	case r.Method == http.MethodGet, restatesDisabled(r, props):
		// A write that restates the Disabled default changes nothing, so it
		// is answered with the default (azurerm_mssql_database update sends
		// one for securityAlertPolicies). Any other write is not modeled yet.
		h.getDatabaseSingleton(w, r, rp, name, props)
	default:
		azurearm.WriteChildNotImplemented(w, rp)
	}
}

// restatesDisabled reports whether r is a PUT or PATCH whose properties.state
// is Disabled, for a singleton whose default state is Disabled.
func restatesDisabled(r *http.Request, props map[string]any) bool {
	if (r.Method != http.MethodPut && r.Method != http.MethodPatch) || props[propState] != stateDisabled {
		return false
	}

	var body struct {
		Properties struct {
			State string `json:"state"`
		} `json:"properties"`
	}

	if err := json.NewDecoder(io.LimitReader(r.Body, azurearm.MaxBodyBytes)).Decode(&body); err != nil {
		return false
	}

	return strings.EqualFold(body.Properties.State, stateDisabled)
}

// getDatabaseSingleton returns the default singleton of an existing database.
func (h *Handler) getDatabaseSingleton(
	w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, name string, props map[string]any,
) {
	db, ok := h.databases()
	if !ok {
		writeUnsupported(w, "databases")
		return
	}

	if _, err := db.GetDatabase(r.Context(), rp.ResourceName, rp.SubResourceName); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	writeSingleton(w, r, rp, name, props)
}

// writeSingleton writes a singleton child envelope whose id is the request path.
func writeSingleton(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, name string, props map[string]any) {
	azurearm.WriteJSON(w, http.StatusOK, singletonEnvelope(strings.TrimSuffix(r.URL.Path, "/"), name, rp, props))
}

// singletonEnvelope is the ARM envelope of a database or server singleton.
func singletonEnvelope(id, name string, rp *azurearm.ResourcePath, props any) map[string]any {
	return map[string]any{"id": id, "name": name, "type": providerName + "/" + rp.NestedType(), "properties": props}
}

// databaseSingleton returns the fixed name and default properties of an
// always-present database child cloudemu does not model.
func databaseSingleton(childType string) (name string, props map[string]any, ok bool) {
	switch strings.ToLower(childType) {
	case "securityalertpolicies":
		return singletonDefaultTitle, map[string]any{
			propState: stateDisabled, "emailAccountAdmins": false,
			"emailAddresses": []string{}, "disabledAlerts": []string{}, propRetentionDays: 0,
		}, true
	case "geobackuppolicies":
		return singletonDefaultTitle, map[string]any{propState: stateDisabled}, true
	case "auditingsettings":
		return singletonDefault, map[string]any{propState: stateDisabled}, true
	case "ledgerdigestuploads":
		return tdeName, map[string]any{propState: stateDisabled}, true
	default:
		return "", nil, false
	}
}

// subRestorableDropped lists the server's dropped databases;
// subConnectionPolicies is the server's connection-type singleton.
const (
	subRestorableDropped  = "restorableDroppedDatabases"
	subConnectionPolicies = "connectionPolicies"
)

// serverSingletonProps returns the default properties of an always-present
// server child (all named "default") cloudemu does not model.
func serverSingletonProps(childType string) (map[string]any, bool) {
	switch strings.ToLower(childType) {
	case "sqlvulnerabilityassessments":
		return map[string]any{propState: stateDisabled}, true
	default:
		return nil, false
	}
}

// serveServerSingleton answers servers/{s}/{child}[/default] for the
// singletons above: a read of an existing server returns the default and a
// write is 501.
func (h *Handler) serveServerSingleton(
	w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, props map[string]any,
) {
	switch {
	case rp.SubResourceName == "":
		azurearm.ServeDeferred(w, r, rp, azurearm.DeferredCollection, nil)
		return
	case !strings.EqualFold(rp.SubResourceName, singletonDefault):
		azurearm.ServeDeferred(w, r, rp, azurearm.DeferredItem, nil)
		return
	case r.Method != http.MethodGet && !restatesDisabled(r, props):
		// Restating the Disabled default (azurerm_mssql_server create sends
		// one for sqlVulnerabilityAssessments) changes nothing; other writes
		// are not modeled yet.
		azurearm.WriteChildNotImplemented(w, rp)
		return
	}

	clusters, err := h.db.DescribeClusters(r.Context(), []string{rp.ResourceName})
	if err != nil || len(clusters) == 0 {
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "server "+rp.ResourceName+" not found")
		return
	}

	writeSingleton(w, r, rp, singletonDefault, props)
}
