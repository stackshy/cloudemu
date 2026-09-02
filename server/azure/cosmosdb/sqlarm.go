package cosmosdb

// ARM SQL-API control plane (Microsoft.DocumentDB/databaseAccounts/sqlDatabases
// and .../containers, plus their throughputSettings). Real armcosmos
// SQLResourcesClient clients configured with a custom endpoint drive this the
// same way they drive management.azure.com, so Bicep / Terraform /
// `az cosmosdb sql database create` can manage the Cosmos data model that was
// previously reachable only through the documents.azure.com data plane.
//
// ARMHandler shares the data-plane Handler's state: databases live in the same
// databases set, containers are the same driver tables (keyed by qualify), and
// throughput lives in the same offers map. A database or container created here
// is therefore immediately visible to the data plane and vice versa — there are
// not two disjoint models.
//
// Create/update/delete/migrate are long-running operations in real Azure; the
// emulator completes them synchronously (200 with the resource body, or 204 for
// delete) so the SDK's Begin* pollers terminate on the first response, matching
// how the account control plane already answers its LROs.

import (
	"context"
	"net/http"
	"sort"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	dbdriver "github.com/stackshy/cloudemu/v2/services/database/driver"
)

const (
	armProvider     = "Microsoft.DocumentDB"
	armAccountType  = "databaseAccounts"
	sqlDatabasesSeg = "sqlDatabases"

	typeSQLDatabase           = "Microsoft.DocumentDB/databaseAccounts/sqlDatabases"
	typeSQLContainer          = "Microsoft.DocumentDB/databaseAccounts/sqlDatabases/containers"
	typeSQLDatabaseThroughput = "Microsoft.DocumentDB/databaseAccounts/sqlDatabases/throughputSettings"
	typeSQLContainerThrough   = "Microsoft.DocumentDB/databaseAccounts/sqlDatabases/containers/throughputSettings"

	throughputName = "default"

	// The two throughput-migration action verbs, lowercased for matching.
	actionMigrateToAutoscale = "migratetoautoscale"
	actionMigrateToManual    = "migratetomanualthroughput"

	// Segment counts under ".../throughputSettings": [throughputSettings,
	// default] is the plain resource; a third segment is the migrate action.
	throughputPlainDepth   = 2
	throughputMigrateDepth = 3

	// Provisioned-throughput floors real Cosmos enforces: 400 RU/s manual,
	// 1000 RU/s autoscale max. Used when migrating between the two modes.
	minManualThroughput    = 400
	minAutoscaleThroughput = 1000
)

// ARMHandler serves the Microsoft.DocumentDB SQL sub-resource control plane
// (sqlDatabases, containers, throughputSettings) against the shared data-plane
// Handler.
type ARMHandler struct {
	h *Handler
}

// NewARM returns an ARM SQL control-plane handler backed by the same data-plane
// Handler, so control-plane and data-plane state stay unified.
func NewARM(h *Handler) *ARMHandler {
	return &ARMHandler{h: h}
}

// Matches claims only the sqlDatabases sub-tree of a database account
// (.../databaseAccounts/{acct}/sqlDatabases/...). It never claims the
// account-level path (create/get/delete/listKeys), which the cosmosaccount
// handler serves, nor the /dbs data plane. Registered before cosmosaccount so it
// wins the first-match over the shared databaseAccounts path.
func (*ARMHandler) Matches(r *http.Request) bool {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		return false
	}

	return strings.EqualFold(rp.Provider, armProvider) &&
		strings.EqualFold(rp.ResourceType, armAccountType) &&
		rp.ResourceName != "" &&
		strings.EqualFold(rp.SubResource, sqlDatabasesSeg)
}

// sqlKind enumerates the addressable resource shapes under the sqlDatabases tree.
type sqlKind int

const (
	kindDBList sqlKind = iota
	kindDB
	kindDBThroughput
	kindDBMigrate
	kindContainerList
	kindContainer
	kindContainerThroughput
	kindContainerMigrate
)

// sqlTarget is a parsed sqlDatabases-subtree request.
type sqlTarget struct {
	kind      sqlKind
	db        string
	container string
	migrate   string // "migratetoautoscale" | "migratetomanualthroughput"
}

// ServeHTTP parses the ARM path, verifies the parent account exists, then
// dispatches to the addressed resource.
func (a *ARMHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "malformed ARM path")
		return
	}

	tail := sqlTail(r.URL.Path, rp.ResourceName)

	target, ok := parseSQLTarget(tail)
	if !ok {
		azurearm.WriteError(w, http.StatusNotFound, "NotFound", "unsupported Cosmos SQL resource path")
		return
	}

	// Every sqlDatabases operation is scoped to a database account; a missing
	// account is ARM's ParentResourceNotFound.
	if !a.h.isAccount(rp.ResourceName) {
		azurearm.WriteError(w, http.StatusNotFound, "ParentResourceNotFound",
			"database account "+rp.ResourceName+" not found")

		return
	}

	a.dispatch(w, r, &rp, &target)
}

// dispatch routes a parsed target to its handler.
func (a *ARMHandler) dispatch(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, t *sqlTarget) {
	switch t.kind {
	case kindDBList:
		a.listDatabases(w, r, rp)
	case kindDB:
		a.databaseResource(w, r, rp, t.db)
	case kindContainerList:
		a.listContainers(w, r, rp, t.db)
	case kindContainer:
		a.containerResource(w, r, rp, t.db, t.container)
	case kindDBThroughput, kindContainerThroughput:
		a.throughputResource(w, r, rp, t)
	case kindDBMigrate, kindContainerMigrate:
		a.migrateThroughput(w, r, rp, t)
	default:
		azurearm.WriteError(w, http.StatusNotFound, "NotFound", "unsupported Cosmos SQL resource path")
	}
}

// sqlTail returns the path segments after .../databaseAccounts/{account}/,
// starting at "sqlDatabases". ParsePath stops at the sqlDatabases segment, so
// the deeper container/throughput segments are recovered from the raw path here.
func sqlTail(urlPath, account string) []string {
	parts := strings.Split(strings.Trim(urlPath, "/"), "/")

	for i := 0; i+1 < len(parts); i++ {
		if strings.EqualFold(parts[i], armAccountType) && parts[i+1] == account {
			return parts[i+2:]
		}
	}

	return nil
}

// parseSQLTarget classifies the sqlDatabases-subtree segments. seg[0] is always
// "sqlDatabases" (guaranteed by Matches).
func parseSQLTarget(seg []string) (sqlTarget, bool) {
	if len(seg) == 0 || !strings.EqualFold(seg[0], sqlDatabasesSeg) {
		return sqlTarget{}, false
	}

	if len(seg) == 1 {
		return sqlTarget{kind: kindDBList}, true
	}

	db := seg[1]
	rest := seg[2:]

	if len(rest) == 0 {
		return sqlTarget{kind: kindDB, db: db}, true
	}

	switch strings.ToLower(rest[0]) {
	case "throughputsettings":
		return parseThroughputTail(db, "", rest, kindDBThroughput, kindDBMigrate)
	case "containers":
		return parseContainerTail(db, rest[1:])
	default:
		return sqlTarget{}, false
	}
}

// parseContainerTail classifies the segments after ".../containers".
func parseContainerTail(db string, seg []string) (sqlTarget, bool) {
	if len(seg) == 0 {
		return sqlTarget{kind: kindContainerList, db: db}, true
	}

	container := seg[0]
	rest := seg[1:]

	if len(rest) == 0 {
		return sqlTarget{kind: kindContainer, db: db, container: container}, true
	}

	if strings.EqualFold(rest[0], "throughputSettings") {
		return parseThroughputTail(db, container, rest, kindContainerThroughput, kindContainerMigrate)
	}

	return sqlTarget{}, false
}

// parseThroughputTail classifies ".../throughputSettings/default[/migrate...]".
func parseThroughputTail(db, container string, seg []string, plain, migrate sqlKind) (sqlTarget, bool) {
	// seg[0] == "throughputSettings"; the sole named child is "default".
	if len(seg) < 2 || !strings.EqualFold(seg[1], throughputName) {
		return sqlTarget{}, false
	}

	base := sqlTarget{db: db, container: container}

	switch len(seg) {
	case throughputPlainDepth:
		base.kind = plain
		return base, true
	case throughputMigrateDepth:
		action := strings.ToLower(seg[2])
		if action != actionMigrateToAutoscale && action != actionMigrateToManual {
			return sqlTarget{}, false
		}

		base.kind = migrate
		base.migrate = action

		return base, true
	default:
		return sqlTarget{}, false
	}
}

// Databases ------------------------------------------------------------------

func (a *ARMHandler) databaseResource(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, db string) {
	switch r.Method {
	case http.MethodPut:
		a.createOrUpdateDatabase(w, r, rp, db)
	case http.MethodGet:
		if !a.h.databaseExists(rp.ResourceName, db) {
			azurearm.WriteError(w, http.StatusNotFound, "NotFound", "sql database "+db+" not found")
			return
		}

		azurearm.WriteJSON(w, http.StatusOK, a.renderDatabase(rp, db))
	case http.MethodDelete:
		// Delete is idempotent: a cascade over a missing database is a no-op that
		// still answers 204 so the SDK's BeginDelete poller terminates.
		_ = a.h.cascadeDeleteDatabase(r.Context(), rp.ResourceName, db)

		w.WriteHeader(http.StatusNoContent)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func (a *ARMHandler) createOrUpdateDatabase(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, db string) {
	var body armSQLDatabaseCreateParams
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	if body.Properties == nil || body.Properties.Resource == nil || body.Properties.Resource.ID == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidParameter", "properties.resource.id is required")
		return
	}

	// registerDatabase is idempotent for the ARM create-or-update contract: a
	// re-PUT of an existing database is not an error, it re-applies throughput.
	a.h.registerDatabase(rp.ResourceName, db)

	// Shared (database-level) throughput, keyed by the database's dbNS — exactly
	// the key the data plane's /offers lookup derives, so it round-trips there.
	if st, ok := offerFromOptions(body.Properties.Options); ok {
		a.h.setOffer(dbNS(rp.ResourceName, db), st)
	}

	azurearm.WriteJSON(w, http.StatusOK, a.renderDatabase(rp, db))
}

func (a *ARMHandler) listDatabases(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodGet {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	names := a.databasesInAccount(rp.ResourceName)

	out := armSQLDatabaseList{Value: make([]armSQLDatabaseGetResults, 0, len(names))}
	for _, name := range names {
		out.Value = append(out.Value, a.renderDatabase(rp, name))
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

// databasesInAccount returns the sorted database names owned by account.
func (a *ARMHandler) databasesInAccount(account string) []string {
	a.h.dbMu.RLock()
	defer a.h.dbMu.RUnlock()

	names := make([]string, 0)

	for key := range a.h.databases {
		if id, ok := accountDBName(key, account); ok {
			names = append(names, id)
		}
	}

	sort.Strings(names)

	return names
}

func (a *ARMHandler) renderDatabase(rp *azurearm.ResourcePath, db string) armSQLDatabaseGetResults {
	id := dbResourceID(rp, db)

	return armSQLDatabaseGetResults{
		ID:   id,
		Name: db,
		Type: typeSQLDatabase,
		Properties: &armSQLDatabaseGetProps{
			Resource: &armSQLDatabaseGetResource{
				ID:    db,
				RID:   "rid-" + dbNS(rp.ResourceName, db),
				TS:    a.h.clock.Now().Unix(),
				ETag:  azurearm.ETag(id),
				Colls: "colls/",
				Users: "users/",
			},
		},
	}
}

// Containers -----------------------------------------------------------------

func (a *ARMHandler) containerResource(
	w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, db, container string,
) {
	switch r.Method {
	case http.MethodPut:
		a.createOrUpdateContainer(w, r, rp, db, container)
	case http.MethodGet:
		res, err := a.renderContainer(r.Context(), rp, db, container)
		if err != nil {
			azurearm.WriteCErr(w, err)
			return
		}

		azurearm.WriteJSON(w, http.StatusOK, res)
	case http.MethodDelete:
		table := qualify(rp.ResourceName, db, container)
		// Idempotent: absence is a no-op that still answers 204.
		_ = a.h.db.DeleteTable(r.Context(), table)
		a.h.deleteOffer(table)
		a.h.attrs.delete(table)
		w.WriteHeader(http.StatusNoContent)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func (a *ARMHandler) createOrUpdateContainer(
	w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, db, container string,
) {
	if !a.h.databaseExists(rp.ResourceName, db) {
		azurearm.WriteError(w, http.StatusNotFound, "ParentResourceNotFound", "sql database "+db+" not found")
		return
	}

	var body armSQLContainerCreateParams
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	res := body.resourceOrNil()
	if res == nil || res.ID == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidParameter", "properties.resource.id is required")
		return
	}

	table := qualify(rp.ResourceName, db, container)
	pkAttr := partitionKeyAttribute(res.PartitionKey)

	cfg := dbdriver.TableConfig{Name: table, PartitionKey: pkAttr}
	if pkAttr != idAttr {
		cfg.SortKey = idAttr
	}

	// Create-or-update: an existing container (AlreadyExists) is not an error —
	// re-apply its attrs and throughput. The partition key is immutable, so the
	// existing table config is kept.
	if err := a.h.db.CreateTable(r.Context(), cfg); err != nil && !cerrors.IsAlreadyExists(err) {
		azurearm.WriteCErr(w, err)
		return
	}

	a.h.attrs.set(table, res.DefaultTTL, res.UniqueKeyPolicy, res.IndexingPolicy)

	if st, ok := offerFromOptions(body.Properties.Options); ok {
		a.h.setOffer(table, st)
	}

	out, err := a.renderContainer(r.Context(), rp, db, container)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

func (a *ARMHandler) listContainers(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, db string) {
	if r.Method != http.MethodGet {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	if !a.h.databaseExists(rp.ResourceName, db) {
		azurearm.WriteError(w, http.StatusNotFound, "ParentResourceNotFound", "sql database "+db+" not found")
		return
	}

	tables, err := a.h.db.ListTables(r.Context())
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	prefix := dbNS(rp.ResourceName, db) + "/"

	out := armSQLContainerList{Value: make([]armSQLContainerGetResults, 0)}

	for _, t := range tables {
		if !strings.HasPrefix(t, prefix) {
			continue
		}

		coll := strings.TrimPrefix(t, prefix)
		if res, cerr := a.renderContainer(r.Context(), rp, db, coll); cerr == nil {
			out.Value = append(out.Value, res)
		}
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

func (a *ARMHandler) renderContainer(
	ctx context.Context, rp *azurearm.ResourcePath, db, container string,
) (armSQLContainerGetResults, error) {
	table := qualify(rp.ResourceName, db, container)

	cfg, err := a.h.db.DescribeTable(ctx, table)
	if err != nil {
		return armSQLContainerGetResults{}, err
	}

	pk := defaultPartitionKey()
	if cfg.PartitionKey != "" {
		pk = &partitionKeyDef{Paths: []string{"/" + cfg.PartitionKey}, Kind: "Hash"}
	}

	attrs := a.h.attrs.get(table)
	id := containerResourceID(rp, db, container)

	return armSQLContainerGetResults{
		ID:   id,
		Name: container,
		Type: typeSQLContainer,
		Properties: &armSQLContainerGetProps{
			Resource: &armSQLContainerGetResource{
				ID:              container,
				PartitionKey:    pk,
				DefaultTTL:      attrs.defaultTTL,
				UniqueKeyPolicy: uniqueKeyPolicyFromDef(attrs.uniqueKeys),
				IndexingPolicy:  attrs.indexingPolicy,
				RID:             containerRID(table),
				TS:              a.h.clock.Now().Unix(),
				ETag:            azurearm.ETag(id),
			},
		},
	}, nil
}

// Throughput -----------------------------------------------------------------

func (a *ARMHandler) throughputResource(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, t *sqlTarget) {
	key, resType, exists := a.throughputTarget(rp, t)
	if !exists {
		azurearm.WriteError(w, http.StatusNotFound, "NotFound", "parent resource not found")
		return
	}

	switch r.Method {
	case http.MethodGet:
		st, ok := a.h.getOffer(key)
		if !ok {
			azurearm.WriteError(w, http.StatusNotFound, "NotFound",
				"resource does not have dedicated throughput (shared or serverless)")

			return
		}

		azurearm.WriteJSON(w, http.StatusOK, renderThroughput(st, throughputID(rp, t), resType))
	case http.MethodPut:
		a.updateThroughput(w, r, rp, t, key, resType)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func (a *ARMHandler) updateThroughput(
	w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, t *sqlTarget, key, resType string,
) {
	var body armThroughputUpdateParams
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	var res *armThroughputResource
	if body.Properties != nil {
		res = body.Properties.Resource
	}

	if res == nil {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidParameter", "properties.resource is required")
		return
	}

	st, ok := offerFromThroughput(res.Throughput, res.AutoscaleSettings)
	if !ok {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidParameter",
			"either throughput or autoscaleSettings.maxThroughput is required")

		return
	}

	a.h.setOffer(key, st)
	azurearm.WriteJSON(w, http.StatusOK, renderThroughput(st, throughputID(rp, t), resType))
}

func (a *ARMHandler) migrateThroughput(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, t *sqlTarget) {
	if r.Method != http.MethodPost {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	key, resType, exists := a.throughputTarget(rp, t)
	if !exists {
		azurearm.WriteError(w, http.StatusNotFound, "NotFound", "parent resource not found")
		return
	}

	st, ok := a.h.getOffer(key)
	if !ok {
		azurearm.WriteError(w, http.StatusNotFound, "NotFound",
			"resource does not have dedicated throughput to migrate")

		return
	}

	migrated := migrateOffer(st, t.migrate == actionMigrateToAutoscale)
	a.h.setOffer(key, migrated)

	azurearm.WriteJSON(w, http.StatusOK, renderThroughput(migrated, throughputID(rp, t), resType))
}

// throughputTarget resolves a throughput request to its offer key and response
// resource type, and reports whether the parent database/container exists.
func (a *ARMHandler) throughputTarget(rp *azurearm.ResourcePath, t *sqlTarget) (key, resType string, exists bool) {
	if t.container != "" {
		table := qualify(rp.ResourceName, t.db, t.container)
		if _, err := a.h.db.DescribeTable(context.Background(), table); err != nil {
			return "", "", false
		}

		return table, typeSQLContainerThrough, true
	}

	if !a.h.databaseExists(rp.ResourceName, t.db) {
		return "", "", false
	}

	return dbNS(rp.ResourceName, t.db), typeSQLDatabaseThroughput, true
}

// migrateOffer converts an offer between manual and autoscale, clamping to the
// mode's floor. Migrating to the mode it already occupies is a no-op.
func migrateOffer(st offerState, toAutoscale bool) offerState {
	if toAutoscale {
		if st.autoscale {
			return st
		}

		maxRU := st.manualThroughput
		if maxRU < minAutoscaleThroughput {
			maxRU = minAutoscaleThroughput
		}

		return offerState{autoscale: true, autoscaleMax: maxRU}
	}

	if !st.autoscale {
		return st
	}

	manual := st.autoscaleMax
	if manual < minManualThroughput {
		manual = minManualThroughput
	}

	return offerState{manualThroughput: manual}
}

// offerFromOptions reads throughput from a create's options block.
func offerFromOptions(o *armCreateUpdateOptions) (offerState, bool) {
	if o == nil {
		return offerState{}, false
	}

	return offerFromThroughput(o.Throughput, o.AutoscaleSettings)
}

// offerFromThroughput builds an offerState from a manual value or an autoscale
// ceiling. Autoscale wins when both are somehow present (real Cosmos rejects
// that combination; preferring autoscale keeps a single, well-defined outcome).
func offerFromThroughput(manual *int32, auto *armAutoscaleSettings) (offerState, bool) {
	if auto != nil && auto.MaxThroughput != nil && *auto.MaxThroughput > 0 {
		return offerState{autoscale: true, autoscaleMax: *auto.MaxThroughput}, true
	}

	if manual != nil && *manual > 0 {
		return offerState{manualThroughput: *manual}, true
	}

	return offerState{}, false
}

// renderThroughput builds the ThroughputSettings response for an offer.
func renderThroughput(st offerState, id, resType string) armThroughputGetResults {
	res := &armThroughputGetResource{MinimumThroughput: "400"}

	if st.autoscale {
		res.AutoscaleSettings = &armAutoscaleSettingsResource{MaxThroughput: int32Ptr(st.autoscaleMax)}
		res.MinimumThroughput = "1000"
	} else {
		res.Throughput = int32Ptr(st.manualThroughput)
	}

	return armThroughputGetResults{
		ID:         id,
		Name:       throughputName,
		Type:       resType,
		Properties: &armThroughputGetProps{Resource: res},
	}
}

// Resource-ID + small helpers ------------------------------------------------

func (b *armSQLContainerCreateParams) resourceOrNil() *armSQLContainerResource {
	if b.Properties == nil {
		return nil
	}

	return b.Properties.Resource
}

func dbResourceID(rp *azurearm.ResourcePath, db string) string {
	return azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, armProvider, armAccountType, rp.ResourceName) +
		"/sqlDatabases/" + db
}

func containerResourceID(rp *azurearm.ResourcePath, db, container string) string {
	return dbResourceID(rp, db) + "/containers/" + container
}

// throughputID is the ARM ID of a throughputSettings/default child.
func throughputID(rp *azurearm.ResourcePath, t *sqlTarget) string {
	base := dbResourceID(rp, t.db)
	if t.container != "" {
		base += "/containers/" + t.container
	}

	return base + "/throughputSettings/" + throughputName
}

func int32Ptr(v int32) *int32 { return &v }
