package sql

import (
	"context"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

// ---- Server (logical) ops ----

func (h *Handler) createOrUpdateServer(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body armServer
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	reqVersion := stringFrom(body.Properties, func(p *armServerProps) string { return p.Version })

	cfg := rdsdriver.ClusterConfig{
		ID:             rp.ResourceName,
		Engine:         "SQLServer",
		Location:       body.Location,
		MasterUsername: stringFrom(body.Properties, func(p *armServerProps) string { return p.AdministratorLogin }),
		MasterUserPassword: stringFrom(body.Properties, func(p *armServerProps) string {
			return p.AdministratorLoginPassword
		}),
		EngineVersion: reqVersion,
		Tags:          body.Tags,
		Scope:         scope.Scope{Subscription: rp.Subscription, ResourceGroup: rp.ResourceGroup},
	}

	// Azure SQL synthesizes version "12.0" when a server create omits it.
	if cfg.EngineVersion == "" {
		cfg.EngineVersion = defaultServerVersion
	}

	// ARM PUT of a new resource returns 201 Created; an in-place update of an
	// existing one returns 200.
	status := http.StatusCreated

	cluster, err := h.db.CreateCluster(r.Context(), cfg)
	if err != nil {
		if !cerrors.IsAlreadyExists(err) {
			azurearm.WriteCErr(w, err)
			return
		}

		// Upsert: PUT on an existing server applies the body (admin/version/tags)
		// rather than returning the stale record. Use the raw request version so
		// a PUT that omits version preserves the existing one (ModifyCluster
		// guards empty), not the create-time "12.0" default.
		cluster, err = h.db.ModifyCluster(r.Context(), rp.ResourceName, rdsdriver.ModifyInstanceInput{
			EngineVersion: reqVersion,
			Tags:          body.Tags,
		})
		if err != nil {
			azurearm.WriteCErr(w, err)
			return
		}

		status = http.StatusOK
	}

	azurearm.WriteJSON(w, status, toARMServer(cluster, rp.Subscription, rp.ResourceGroup))
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

	filter := scope.Scope{Subscription: rp.Subscription, ResourceGroup: rp.ResourceGroup}

	out := make([]armServer, 0, len(clusters))

	for i := range clusters {
		if !clusters[i].Scope.Matches(filter) {
			continue
		}

		// Render the id from the server's own group, not the request path's
		// (empty on a subscription-scoped list) — so the id carries its true
		// resourceGroups/{rg} segment.
		rg := clusters[i].Scope.ResourceGroup
		if rg == "" {
			rg = rp.ResourceGroup
		}

		out = append(out, toARMServer(&clusters[i], rp.Subscription, rg))
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

// databaseStatuser is the optional provider capability that reports a
// database's transient ARM status (Creating / Scaling) while an async settle
// window is active. Providers that don't implement it (settle disabled or not
// adopted) leave the status empty, so read responses report the terminal
// Online. Kept an assertion (not a required interface method) so the wire
// layer stays decoupled from the settle overlay.
type databaseStatuser interface {
	DatabaseTransientStatus(server, name string) string
}

// databaseStatus returns the transient status the provider reports for a
// database, or "" when the provider doesn't expose one.
func (h *Handler) databaseStatus(server, name string) string {
	s, ok := h.db.(databaseStatuser)
	if !ok {
		return ""
	}

	return s.DatabaseTransientStatus(server, name)
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
		cfg.CreateMode = body.Properties.CreateMode
		cfg.SourceDatabaseID = body.Properties.SourceDatabaseID

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
		azurearm.WriteJSON(w, http.StatusOK, toARMDatabase(out, rp, h.databaseStatus(out.Server, out.Name)))
		return
	}

	if cerrors.IsNotFound(err) {
		// CreateDatabase raises NotFound for three distinct causes: a missing
		// parent server, a Copy/PointInTimeRestore sourceDatabaseId that doesn't
		// resolve, or (once the server exists) an elasticPoolId that doesn't
		// resolve to a pool on it. Disambiguate by server existence + copy mode.
		h.writeDatabaseNotFound(r.Context(), w, rp.ResourceName, &cfg, err)
		return
	}

	if !cerrors.IsAlreadyExists(err) {
		azurearm.WriteCErr(w, err)
		return
	}

	out, err = replaceDatabase(r.Context(), db, &body, &cfg)
	if err == nil {
		azurearm.WriteJSON(w, http.StatusOK, toARMDatabase(out, rp, h.databaseStatus(out.Server, out.Name)))
		return
	}

	if cerrors.IsNotFound(err) {
		// replaceDatabase's only NotFound is its elastic-pool pre-check (the
		// copy path never runs on the update half), so pass no cfg.
		h.writeDatabaseNotFound(r.Context(), w, rp.ResourceName, nil, err)
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
func (h *Handler) writeDatabaseNotFound(
	ctx context.Context, w http.ResponseWriter, server string, cfg *rdsdriver.DatabaseConfig, err error,
) {
	clusters, dErr := h.db.DescribeClusters(ctx, []string{server})
	if dErr != nil || len(clusters) == 0 {
		azurearm.WriteParentNotFound(w, err)
		return
	}

	// Server exists: a Copy/PointInTimeRestore whose sourceDatabaseId is set
	// resolved the source before the pool check, so the NotFound is the source.
	if cfg != nil && cfg.SourceDatabaseID != "" {
		azurearm.WriteError(w, http.StatusNotFound, "SourceDatabaseNotFound", err.Error())
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

// replaceDatabase merges the request body over the stored database and applies
// the result in place, so a PUT/PATCH against an existing database changes
// sku/tier/HA while leaving omitted fields intact.
//
// It updates the record in place via the DatabaseUpdater capability rather than
// deleting and re-creating it. A delete+recreate upsert dropped the database's
// transparentDataEncryption record and re-materialized it as Enabled on the
// re-create, silently re-enabling TDE on a database the user had disabled it on.
// The in-place update leaves the TDE sub-resource untouched.
//
// The merged elasticPoolId is validated before the update: a request that
// references a nonexistent pool must fail with no side effect.
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

	updater, ok := db.(rdsdriver.DatabaseUpdater)
	if !ok {
		return nil, cerrors.New(cerrors.InvalidArgument, "database update is not supported by this provider")
	}

	return updater.UpdateDatabase(ctx, rdsdriver.DatabaseConfig{
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

func (h *Handler) getDatabase(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, db rdsdriver.Databases) {
	out, err := db.GetDatabase(r.Context(), rp.ResourceName, rp.SubResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMDatabase(out, rp, h.databaseStatus(out.Server, out.Name)))
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

func (h *Handler) listDatabases(
	w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, db rdsdriver.Databases,
) {
	items, err := db.ListDatabases(r.Context(), rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := make([]armDatabase, 0, len(items))
	for i := range items {
		out = append(out, toARMDatabase(&items[i], rp, h.databaseStatus(items[i].Server, items[i].Name)))
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
