package azuresql

import (
	"net/http"

	rdsdriver "github.com/stackshy/cloudemu/relationaldb/driver"
	"github.com/stackshy/cloudemu/server/wire/azurearm"
)

// ---- Server (logical) ops ----

func (h *Handler) createOrUpdateServer(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body armServer
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	cfg := rdsdriver.ClusterConfig{
		ID:             rp.ResourceName,
		Engine:         "SQLServer",
		MasterUsername: stringFrom(body.Properties, func(p *armServerProps) string { return p.AdministratorLogin }),
		MasterUserPassword: stringFrom(body.Properties, func(p *armServerProps) string {
			return p.AdministratorLoginPassword
		}),
		EngineVersion: stringFrom(body.Properties, func(p *armServerProps) string { return p.Version }),
		Tags:          body.Tags,
	}

	cluster, err := h.db.CreateCluster(r.Context(), cfg)
	if err != nil {
		// Idempotent PUT: if the server already exists, treat as a get.
		existing, getErr := h.db.DescribeClusters(r.Context(), []string{rp.ResourceName})
		if getErr != nil || len(existing) != 1 {
			azurearm.WriteCErr(w, err)
			return
		}

		cluster = &existing[0]
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMServer(cluster, rp.Subscription, rp.ResourceGroup))
}

func (h *Handler) updateServer(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body armServer
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	input := rdsdriver.ModifyInstanceInput{
		EngineVersion: stringFrom(body.Properties, func(p *armServerProps) string { return p.Version }),
		Tags:          body.Tags,
	}

	cluster, err := h.db.ModifyCluster(r.Context(), rp.ResourceName, input)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMServer(cluster, rp.Subscription, rp.ResourceGroup))
}

func (h *Handler) getServer(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	clusters, err := h.db.DescribeClusters(r.Context(), []string{rp.ResourceName})
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMServer(&clusters[0], rp.Subscription, rp.ResourceGroup))
}

func (h *Handler) deleteServer(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if err := h.db.DeleteCluster(r.Context(), rp.ResourceName); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (h *Handler) listServers(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	clusters, err := h.db.DescribeClusters(r.Context(), nil)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := make([]armServer, 0, len(clusters))
	for i := range clusters {
		out = append(out, toARMServer(&clusters[i], rp.Subscription, rp.ResourceGroup))
	}

	azurearm.WriteJSON(w, http.StatusOK, armList[armServer]{Value: out})
}

// ---- Database ops ----

//nolint:gocyclo // sequential field defaulting + restore path keeps the body linear.
func (h *Handler) createOrUpdateDatabase(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body armDatabase
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	server := rp.ResourceName
	dbName := rp.SubResourceName

	// Restore path: createMode=Restore + sourceDatabaseId.
	if body.Properties != nil && body.Properties.CreateMode == "Restore" {
		input := rdsdriver.RestoreInstanceInput{
			NewInstanceID: server + "/" + dbName,
			SnapshotID:    body.Properties.SourceDatabaseID,
		}

		if body.SKU != nil {
			input.InstanceClass = body.SKU.Name
		}

		inst, err := h.db.RestoreInstanceFromSnapshot(r.Context(), input)
		if err != nil {
			azurearm.WriteCErr(w, err)
			return
		}

		azurearm.WriteJSON(w, http.StatusOK, toARMDatabase(inst, rp.Subscription, rp.ResourceGroup))

		return
	}

	cfg := rdsdriver.InstanceConfig{
		ID:               dbName,
		ClusterID:        server,
		Engine:           "SQLServer",
		AvailabilityZone: body.Location,
		Tags:             body.Tags,
	}

	if body.SKU != nil {
		cfg.InstanceClass = body.SKU.Name
	}

	if body.Properties != nil && body.Properties.MaxSizeBytes > 0 {
		cfg.AllocatedStorage = int(body.Properties.MaxSizeBytes / (1 << 30))
	}

	inst, err := h.db.CreateInstance(r.Context(), cfg)
	if err != nil {
		// Idempotent PUT: if the database already exists, fall back to a get.
		existing, getErr := h.db.DescribeInstances(r.Context(), []string{server + "/" + dbName})
		if getErr != nil || len(existing) != 1 {
			azurearm.WriteCErr(w, err)
			return
		}

		inst = &existing[0]
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMDatabase(inst, rp.Subscription, rp.ResourceGroup))
}

func (h *Handler) updateDatabase(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body armDatabase
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	input := rdsdriver.ModifyInstanceInput{
		Tags: body.Tags,
	}

	if body.SKU != nil {
		input.InstanceClass = body.SKU.Name
	}

	if body.Properties != nil && body.Properties.MaxSizeBytes > 0 {
		input.AllocatedStorage = int(body.Properties.MaxSizeBytes / (1 << 30))
	}

	inst, err := h.db.ModifyInstance(r.Context(), rp.ResourceName+"/"+rp.SubResourceName, input)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMDatabase(inst, rp.Subscription, rp.ResourceGroup))
}

func (h *Handler) getDatabase(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	insts, err := h.db.DescribeInstances(r.Context(), []string{rp.ResourceName + "/" + rp.SubResourceName})
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMDatabase(&insts[0], rp.Subscription, rp.ResourceGroup))
}

func (h *Handler) deleteDatabase(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if err := h.db.DeleteInstance(r.Context(), rp.ResourceName+"/"+rp.SubResourceName); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (h *Handler) listDatabases(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	all, err := h.db.DescribeInstances(r.Context(), nil)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := make([]armDatabase, 0)

	for i := range all {
		if all[i].ClusterID != rp.ResourceName {
			continue
		}

		out = append(out, toARMDatabase(&all[i], rp.Subscription, rp.ResourceGroup))
	}

	azurearm.WriteJSON(w, http.StatusOK, armList[armDatabase]{Value: out})
}

// stringFrom returns f(p) when p is non-nil, else "". A small helper that
// keeps body decoders compact when most fields are pointer-deref accesses.
func stringFrom[T any](p *T, f func(*T) string) string {
	if p == nil {
		return ""
	}

	return f(p)
}
