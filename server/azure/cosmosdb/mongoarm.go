package cosmosdb

// ARM Mongo-API control plane (Microsoft.DocumentDB/databaseAccounts/
// mongodbDatabases and .../collections, plus their throughputSettings). Real
// armcosmos MongoDBResourcesClient clients configured with a custom endpoint
// drive this the same way they drive management.azure.com, so Bicep / Terraform
// (azurerm_cosmosdb_mongo_database / _mongo_collection) / `az cosmosdb mongodb
// database create` can manage the Mongo data model.
//
// MongoARMHandler shares the data-plane Handler's state exactly as the SQL ARM
// handler does: Mongo databases live in the same databases set (a real Cosmos
// account is single-API, so SQL and Mongo databases never coexist on one
// account and the shared namespace is not a collision), Mongo collections are
// the same driver tables (keyed by qualify, partitioned by the collection's
// shard key), and throughput lives in the same offers map. The API-agnostic
// database and throughput planes are shared from armcommon.go; only the
// collection shape (shard key + indexes + analytical TTL) is handled here.
//
// Create/update/delete/migrate are long-running operations in real Azure; the
// emulator completes them synchronously (200 with the resource body, or 204 for
// delete) so the SDK's Begin* pollers terminate on the first response, matching
// the SQL control plane.

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"sync"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	dbdriver "github.com/stackshy/cloudemu/v2/services/database/driver"
)

const (
	mongoDatabasesSeg = "mongodbDatabases"
	collectionsSeg    = "collections"

	typeMongoDatabase             = "Microsoft.DocumentDB/databaseAccounts/mongodbDatabases"
	typeMongoCollection           = "Microsoft.DocumentDB/databaseAccounts/mongodbDatabases/collections"
	typeMongoDatabaseThroughput   = "Microsoft.DocumentDB/databaseAccounts/mongodbDatabases/throughputSettings"
	typeMongoCollectionThroughput = "Microsoft.DocumentDB/databaseAccounts/mongodbDatabases/collections/throughputSettings"

	// shardKindHash is the shard-key kind real Cosmos reports for a Mongo shard
	// key (Mongo sharding is hash-based); reconstructed on a collection read.
	shardKindHash = "Hash"
)

// mongoAPISpec is the Mongo-API type/segment set for the shared database and
// throughput planes.
func mongoAPISpec() *armAPISpec {
	return &armAPISpec{
		databasesSegment:       mongoDatabasesSeg,
		childSegment:           collectionsSeg,
		databaseType:           typeMongoDatabase,
		databaseThroughputType: typeMongoDatabaseThroughput,
		childThroughputType:    typeMongoCollectionThroughput,
	}
}

// Mongo collection attrs store -----------------------------------------------

// mongoCollAttrs holds the Mongo collection properties the generic database
// driver has no concept of, so a collection read/list round-trips them.
type mongoCollAttrs struct {
	shardKey             map[string]string
	indexes              []armMongoIndex
	analyticalStorageTTL *int32
}

// mongoAttrStore tracks mongoCollAttrs keyed by the collection's qualified
// table name (see qualify), mirroring how offers.go and container_attrs.go
// track out-of-driver container state.
type mongoAttrStore struct {
	mu    sync.RWMutex
	attrs map[string]mongoCollAttrs
}

func newMongoAttrStore() *mongoAttrStore {
	return &mongoAttrStore{attrs: make(map[string]mongoCollAttrs)}
}

func (s *mongoAttrStore) set(table string, a mongoCollAttrs) {
	s.mu.Lock()
	s.attrs[table] = a
	s.mu.Unlock()
}

func (s *mongoAttrStore) get(table string) (mongoCollAttrs, bool) {
	s.mu.RLock()
	a, ok := s.attrs[table]
	s.mu.RUnlock()

	return a, ok
}

func (s *mongoAttrStore) delete(table string) {
	s.mu.Lock()
	delete(s.attrs, table)
	s.mu.Unlock()
}

// MongoARMHandler ------------------------------------------------------------

// MongoARMHandler serves the Microsoft.DocumentDB Mongo sub-resource control
// plane (mongodbDatabases, collections, throughputSettings) against the shared
// data-plane Handler.
type MongoARMHandler struct {
	h    *Handler
	spec *armAPISpec
}

// NewMongoARM returns an ARM Mongo control-plane handler backed by the same
// data-plane Handler, so control-plane and data-plane state stay unified.
func NewMongoARM(h *Handler) *MongoARMHandler {
	return &MongoARMHandler{h: h, spec: mongoAPISpec()}
}

// Matches claims only the mongodbDatabases sub-tree of a database account. It
// never claims the account-level path (served by cosmosaccount) nor the /dbs
// data plane, and it is disjoint from the SQL ARM handler's sqlDatabases tree.
func (*MongoARMHandler) Matches(r *http.Request) bool {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		return false
	}

	return strings.EqualFold(rp.Provider, armProvider) &&
		strings.EqualFold(rp.ResourceType, armAccountType) &&
		rp.ResourceName != "" &&
		strings.EqualFold(rp.SubResource, mongoDatabasesSeg)
}

// ServeHTTP parses the ARM path, verifies the parent account exists, then
// dispatches: the database/throughput kinds go to the shared plane, the
// collection kinds to the Mongo-specific callbacks below.
func (m *MongoARMHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	serveARMSubtree(m.h, w, r, mongoDatabasesSeg, collectionsSeg, "unsupported Cosmos Mongo resource path",
		func(rp *azurearm.ResourcePath, t *sqlTarget) {
			armDispatch(m.h, w, r, rp, t, m.spec, "unsupported Cosmos Mongo resource path", childOps{
				list:   m.listCollections,
				single: m.collectionResource,
			})
		})
}

// Collections (Mongo-specific) -----------------------------------------------

func (m *MongoARMHandler) collectionResource(
	w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, db, coll string,
) {
	armServeChild(m.h, w, r, rp, db, coll, singleChildOps{
		put:     m.createOrUpdateCollection,
		render:  m.renderCollectionAny,
		cleanup: m.h.mongoAttrs.delete,
	})
}

// renderCollectionAny adapts renderCollection to the shared render callback.
func (m *MongoARMHandler) renderCollectionAny(ctx context.Context, rp *azurearm.ResourcePath, db, coll string) (any, error) {
	return m.renderCollection(ctx, rp, db, coll)
}

func (m *MongoARMHandler) createOrUpdateCollection(
	w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, db, coll string,
) {
	if !m.h.databaseExists(rp.ResourceName, db) {
		azurearm.WriteError(w, http.StatusNotFound, "ParentResourceNotFound", "mongodb database "+db+" not found")
		return
	}

	var body armMongoCollectionCreateParams
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	res := body.resourceOrNil()
	if res == nil || res.ID == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidParameter", "properties.resource.id is required")
		return
	}

	table := qualify(rp.ResourceName, db, coll)
	pkAttr := shardKeyAttribute(res.ShardKey)

	cfg := dbdriver.TableConfig{Name: table, PartitionKey: pkAttr}
	if pkAttr != idAttr {
		cfg.SortKey = idAttr
	}

	// Create-or-update: an existing collection (AlreadyExists) is not an error —
	// re-apply its attrs and throughput. The shard key is immutable, so the
	// existing table config is kept.
	if err := m.h.db.CreateTable(r.Context(), cfg); err != nil && !cerrors.IsAlreadyExists(err) {
		azurearm.WriteCErr(w, err)
		return
	}

	m.h.mongoAttrs.set(table, mongoCollAttrs{
		shardKey:             res.ShardKey,
		indexes:              res.Indexes,
		analyticalStorageTTL: res.AnalyticalStorageTTL,
	})

	if st, ok := offerFromOptions(body.Properties.Options); ok {
		m.h.setOffer(table, st)
	}

	out, err := m.renderCollection(r.Context(), rp, db, coll)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

func (m *MongoARMHandler) listCollections(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, db string) {
	armListChildren(m.h, w, r, rp, db, "mongodb database "+db+" not found", m.renderCollectionAny)
}

func (m *MongoARMHandler) renderCollection(
	ctx context.Context, rp *azurearm.ResourcePath, db, coll string,
) (armMongoCollectionGetResults, error) {
	table := qualify(rp.ResourceName, db, coll)

	cfg, err := m.h.db.DescribeTable(ctx, table)
	if err != nil {
		return armMongoCollectionGetResults{}, err
	}

	attrs, _ := m.h.mongoAttrs.get(table)

	// A collection created through the ARM plane carries its shard key in the
	// attrs store; one materialized only through the driver falls back to the
	// table's partition key (a single-field hash shard key).
	shardKey := attrs.shardKey
	if shardKey == nil && cfg.PartitionKey != "" && cfg.PartitionKey != idAttr {
		shardKey = map[string]string{cfg.PartitionKey: shardKindHash}
	}

	id := armChildResourceID(rp, db, coll, m.spec)

	return armMongoCollectionGetResults{
		ID:   id,
		Name: coll,
		Type: typeMongoCollection,
		Properties: &armMongoCollectionGetProps{
			Resource: &armMongoCollectionGetResource{
				ID:                   coll,
				ShardKey:             shardKey,
				Indexes:              attrs.indexes,
				AnalyticalStorageTTL: attrs.analyticalStorageTTL,
				RID:                  containerRID(table),
				TS:                   m.h.clock.Now().Unix(),
				ETag:                 azurearm.ETag(id),
			},
		},
	}, nil
}

// Helpers --------------------------------------------------------------------

func (b *armMongoCollectionCreateParams) resourceOrNil() *armMongoCollectionResource {
	if b.Properties == nil {
		return nil
	}

	return b.Properties.Resource
}

// shardKeyAttribute maps a Mongo shard key onto the driver table's partition-key
// attribute. A Cosmos Mongo shard key is a single field; when several are given
// the lexicographically-first is used deterministically, and an absent shard key
// yields an unsharded collection partitioned by its id.
func shardKeyAttribute(shardKey map[string]string) string {
	if len(shardKey) == 0 {
		return idAttr
	}

	fields := make([]string, 0, len(shardKey))
	for f := range shardKey {
		fields = append(fields, f)
	}

	sort.Strings(fields)

	return fields[0]
}
