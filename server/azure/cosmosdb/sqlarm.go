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
// not two disjoint models. The API-agnostic database and throughput planes live
// in armcommon.go and are shared with the Mongo-API control plane (mongoarm.go);
// only the SQL container shape (partition key + TTL/unique keys) is here.
//
// Create/update/delete/migrate are long-running operations in real Azure; the
// emulator completes them synchronously (200 with the resource body, or 204 for
// delete) so the SDK's Begin* pollers terminate on the first response, matching
// how the account control plane already answers its LROs.

import (
	"context"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	dbdriver "github.com/stackshy/cloudemu/v2/services/database/driver"
)

const (
	armProvider     = "Microsoft.DocumentDB"
	armAccountType  = "databaseAccounts"
	sqlDatabasesSeg = "sqlDatabases"
	containersSeg   = "containers"

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

// sqlAPISpec is the SQL-API type/segment set for the shared database and
// throughput planes.
func sqlAPISpec() *armAPISpec {
	return &armAPISpec{
		databasesSegment:       sqlDatabasesSeg,
		childSegment:           containersSeg,
		databaseType:           typeSQLDatabase,
		databaseThroughputType: typeSQLDatabaseThroughput,
		childThroughputType:    typeSQLContainerThrough,
	}
}

// ARMHandler serves the Microsoft.DocumentDB SQL sub-resource control plane
// (sqlDatabases, containers, throughputSettings) against the shared data-plane
// Handler.
type ARMHandler struct {
	h    *Handler
	spec *armAPISpec
}

// NewARM returns an ARM SQL control-plane handler backed by the same data-plane
// Handler, so control-plane and data-plane state stay unified.
func NewARM(h *Handler) *ARMHandler {
	return &ARMHandler{h: h, spec: sqlAPISpec()}
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

// sqlKind enumerates the addressable resource shapes under a database sub-tree.
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

// sqlTarget is a parsed database-subtree request (the "container" field holds
// the SQL container or Mongo collection name, per the API).
type sqlTarget struct {
	kind      sqlKind
	db        string
	container string
	migrate   string // "migratetoautoscale" | "migratetomanualthroughput"
}

// ServeHTTP parses the ARM path, verifies the parent account exists, then
// dispatches: the database/throughput kinds go to the shared plane, the
// container kinds to the SQL-specific callbacks below.
func (a *ARMHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	serveARMSubtree(a.h, w, r, sqlDatabasesSeg, containersSeg, "unsupported Cosmos SQL resource path",
		func(rp *azurearm.ResourcePath, t *sqlTarget) {
			armDispatch(a.h, w, r, rp, t, a.spec, "unsupported Cosmos SQL resource path", childOps{
				list:   a.listContainers,
				single: a.containerResource,
			})
		})
}

// Path parsing (shared) -------------------------------------------------------

// armTail returns the path segments after .../databaseAccounts/{account}/,
// starting at the API's databases segment. ParsePath stops at that segment, so
// the deeper child/throughput segments are recovered from the raw path here.
func armTail(urlPath, account string) []string {
	parts := strings.Split(strings.Trim(urlPath, "/"), "/")

	for i := 0; i+1 < len(parts); i++ {
		if strings.EqualFold(parts[i], armAccountType) && parts[i+1] == account {
			return parts[i+2:]
		}
	}

	return nil
}

// parseDBSubtree classifies the segments under an API's databases tree. seg[0]
// is always the databases segment (guaranteed by Matches). dbSeg/childSeg name
// the API's databases and child collection segments ("sqlDatabases"/"containers"
// or "mongodbDatabases"/"collections").
func parseDBSubtree(seg []string, dbSeg, childSeg string) (sqlTarget, bool) {
	if len(seg) == 0 || !strings.EqualFold(seg[0], dbSeg) {
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

	switch {
	case strings.EqualFold(rest[0], "throughputSettings"):
		return parseThroughputTail(db, "", rest, kindDBThroughput, kindDBMigrate)
	case strings.EqualFold(rest[0], childSeg):
		return parseChildTail(db, rest[1:])
	default:
		return sqlTarget{}, false
	}
}

// parseChildTail classifies the segments after ".../{childSegment}".
func parseChildTail(db string, seg []string) (sqlTarget, bool) {
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

// Containers (SQL-specific) ---------------------------------------------------

func (a *ARMHandler) containerResource(
	w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, db, container string,
) {
	armServeChild(a.h, w, r, rp, db, container, singleChildOps{
		put:     a.createOrUpdateContainer,
		render:  a.renderContainerAny,
		cleanup: a.h.attrs.delete,
	})
}

// renderContainerAny adapts renderContainer to the shared render callback.
func (a *ARMHandler) renderContainerAny(ctx context.Context, rp *azurearm.ResourcePath, db, container string) (any, error) {
	return a.renderContainer(ctx, rp, db, container)
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
	armListChildren(a.h, w, r, rp, db, "sql database "+db+" not found", a.renderContainerAny)
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
	id := armChildResourceID(rp, db, container, a.spec)

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

// Throughput/offer helpers (shared) ------------------------------------------

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

// Small helpers ---------------------------------------------------------------

func (b *armSQLContainerCreateParams) resourceOrNil() *armSQLContainerResource {
	if b.Properties == nil {
		return nil
	}

	return b.Properties.Resource
}

func int32Ptr(v int32) *int32 { return &v }
