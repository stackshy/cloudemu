package sql

import (
	"context"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
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
		Location:       body.Location,
		MasterUsername: stringFrom(body.Properties, func(p *armServerProps) string { return p.AdministratorLogin }),
		MasterUserPassword: stringFrom(body.Properties, func(p *armServerProps) string {
			return p.AdministratorLoginPassword
		}),
		EngineVersion: stringFrom(body.Properties, func(p *armServerProps) string { return p.Version }),
		Tags:          body.Tags,
	}

	cluster, err := h.db.CreateCluster(r.Context(), cfg)
	if err != nil {
		if !cerrors.IsAlreadyExists(err) {
			azurearm.WriteCErr(w, err)
			return
		}

		// Upsert: PUT on an existing server applies the body (admin/version/tags)
		// rather than returning the stale record.
		cluster, err = h.db.ModifyCluster(r.Context(), rp.ResourceName, rdsdriver.ModifyInstanceInput{
			EngineVersion: cfg.EngineVersion,
			Tags:          body.Tags,
		})
		if err != nil {
			azurearm.WriteCErr(w, err)
			return
		}
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

	if len(clusters) == 0 {
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "server "+rp.ResourceName+" not found")
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMServer(&clusters[0], rp.Subscription, rp.ResourceGroup))
}

func (h *Handler) deleteServer(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	// ARM DELETE is idempotent: deleting an absent server is a success, not a
	// 404 (Servers-Delete documents 204 = "does not exist").
	if err := h.db.DeleteCluster(r.Context(), rp.ResourceName); err != nil && !cerrors.IsNotFound(err) {
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

// ---- Database ops (Databases capability) ----

// databases returns the optional Databases capability. Logical Azure SQL
// databases are backed by this capability (not the RDS instance path) so a
// database created over the wire is the same record Resource Graph enumerates
// via ListDatabases.
func (h *Handler) databases() (rdsdriver.Databases, bool) {
	d, ok := h.db.(rdsdriver.Databases)
	return d, ok
}

func dbCfgFromBody(body *armDatabase, rp *azurearm.ResourcePath) rdsdriver.DatabaseConfig {
	cfg := rdsdriver.DatabaseConfig{
		Server:   rp.ResourceName,
		Name:     rp.SubResourceName,
		Location: body.Location,
		Tags:     body.Tags,
	}
	if body.SKU != nil {
		cfg.SKUName = body.SKU.Name
		cfg.SKUTier = body.SKU.Tier
		cfg.SKUCapacity = body.SKU.Capacity
	}

	if body.Properties != nil {
		cfg.Collation = body.Properties.Collation
		cfg.ElasticPoolID = body.Properties.ElasticPoolID

		if body.Properties.ZoneRedundant != nil {
			cfg.ZoneRedundant = *body.Properties.ZoneRedundant
		}
	}

	return cfg
}

// putDatabase serves both PUT (CreateOrUpdate) and PATCH (Update): create when
// absent, otherwise apply the body's sku/tier/zoneRedundant to the existing
// record. The Databases capability has no update verb, so an upsert merges the
// body over the stored fields and re-creates.
func (h *Handler) putDatabase(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, db rdsdriver.Databases) {
	var body armDatabase
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	cfg := dbCfgFromBody(&body, rp)

	out, err := db.CreateDatabase(r.Context(), cfg)
	if err == nil {
		azurearm.WriteJSON(w, http.StatusOK, toARMDatabase(out, rp))
		return
	}

	if cerrors.IsNotFound(err) {
		// CreateDatabase raises NotFound for two distinct causes: a missing
		// parent server, or (once the server exists) an elasticPoolId that
		// doesn't resolve to a pool on it. Disambiguate by server existence.
		h.writeDatabaseNotFound(r.Context(), w, rp.ResourceName, err)
		return
	}

	if !cerrors.IsAlreadyExists(err) {
		azurearm.WriteCErr(w, err)
		return
	}

	out, err = replaceDatabase(r.Context(), db, &body, &cfg)
	if err == nil {
		azurearm.WriteJSON(w, http.StatusOK, toARMDatabase(out, rp))
		return
	}

	if cerrors.IsNotFound(err) {
		// Same disambiguation as above: replaceDatabase's own elastic-pool
		// pre-check also raises NotFound.
		h.writeDatabaseNotFound(r.Context(), w, rp.ResourceName, err)
		return
	}

	azurearm.WriteCErr(w, err)
}

// writeDatabaseNotFound distinguishes the two NotFound causes a database
// create/replace can raise: a missing parent server (real Azure answers 404
// ParentResourceNotFound for a child under an absent parent) vs an existing
// server whose referenced elastic pool doesn't exist (real Azure answers 400
// TargetElasticPoolDoesNotExist — see
// https://learn.microsoft.com/en-us/rest/api/sql/databases/create-or-update).
func (h *Handler) writeDatabaseNotFound(ctx context.Context, w http.ResponseWriter, server string, err error) {
	clusters, dErr := h.db.DescribeClusters(ctx, []string{server})
	if dErr != nil || len(clusters) == 0 {
		azurearm.WriteParentNotFound(w, err)
		return
	}

	azurearm.WriteError(w, http.StatusBadRequest, "TargetElasticPoolDoesNotExist", err.Error())
}

// mergeDatabaseFields overlays the non-empty fields of cfg (and body's
// pointer-only properties) onto existing, leaving fields the request omitted
// untouched. Split out of replaceDatabase to keep that function's
// cyclomatic complexity down — this is pure field merging, no I/O.
func mergeDatabaseFields(existing *rdsdriver.Database, body *armDatabase, cfg *rdsdriver.DatabaseConfig) rdsdriver.Database {
	merged := *existing

	if cfg.SKUName != "" {
		merged.SKUName = cfg.SKUName
	}

	if cfg.SKUTier != "" {
		merged.SKUTier = cfg.SKUTier
	}

	if cfg.SKUCapacity != 0 {
		merged.SKUCapacity = cfg.SKUCapacity
	}

	if cfg.Collation != "" {
		merged.Collation = cfg.Collation
	}

	if cfg.Location != "" {
		merged.Location = cfg.Location
	}

	if cfg.Tags != nil {
		merged.Tags = cfg.Tags
	}

	if cfg.ElasticPoolID != "" {
		merged.ElasticPoolID = cfg.ElasticPoolID
	}

	if body.Properties != nil && body.Properties.ZoneRedundant != nil {
		merged.ZoneRedundant = *body.Properties.ZoneRedundant
	}

	return merged
}

// replaceDatabase merges the request body over the stored database and
// re-creates it, so a PUT/PATCH against an existing database changes sku/tier/
// HA while leaving omitted fields intact.
//
// The merged elasticPoolId is validated before DeleteDatabase runs: a request
// that references a nonexistent pool must fail the update with no side
// effect, not delete the database out from under a bad request (CreateDatabase
// re-validates the pool too, but only after the delete below would already
// have happened).
func replaceDatabase(
	ctx context.Context, db rdsdriver.Databases, body *armDatabase, cfg *rdsdriver.DatabaseConfig,
) (*rdsdriver.Database, error) {
	existing, err := db.GetDatabase(ctx, cfg.Server, cfg.Name)
	if err != nil {
		return nil, err
	}

	merged := mergeDatabaseFields(existing, body, cfg)

	if err := requireElasticPool(ctx, db, cfg.Server, merged.ElasticPoolID); err != nil {
		return nil, err
	}

	if err := db.DeleteDatabase(ctx, cfg.Server, cfg.Name); err != nil {
		return nil, err
	}

	return db.CreateDatabase(ctx, rdsdriver.DatabaseConfig{
		Server:        merged.Server,
		Name:          merged.Name,
		Charset:       merged.Charset,
		Collation:     merged.Collation,
		Location:      merged.Location,
		Tags:          merged.Tags,
		SKUName:       merged.SKUName,
		SKUTier:       merged.SKUTier,
		SKUCapacity:   merged.SKUCapacity,
		ZoneRedundant: merged.ZoneRedundant,
		ElasticPoolID: merged.ElasticPoolID,
	})
}

// requireElasticPool returns NotFound when a non-empty elastic-pool reference
// doesn't resolve to an existing pool on the server. Empty id is a no-op (a
// standalone database). Mirrors the provider-level check CreateDatabase
// applies, so replaceDatabase can fail before mutating anything instead of
// only after CreateDatabase re-validates on the re-create half of the upsert.
func requireElasticPool(ctx context.Context, db rdsdriver.Databases, server, poolID string) error {
	if poolID == "" {
		return nil
	}

	pools, ok := db.(rdsdriver.ElasticPools)
	if !ok {
		return nil
	}

	_, err := pools.GetElasticPool(ctx, server, rdsdriver.ElasticPoolName(poolID))

	return err
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
	// ARM DELETE is idempotent: deleting an absent database is a success, not a
	// 404 (Databases-Delete documents 204 = "does not exist").
	if err := db.DeleteDatabase(r.Context(), rp.ResourceName, rp.SubResourceName); err != nil && !cerrors.IsNotFound(err) {
		azurearm.WriteCErr(w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
}

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

// stringFrom returns f(p) when p is non-nil, else "". A small helper that
// keeps body decoders compact when most fields are pointer-deref accesses.
func stringFrom[T any](p *T, f func(*T) string) string {
	if p == nil {
		return ""
	}

	return f(p)
}
