package cosmosdb

import (
	"fmt"
	"strings"
	"sync"
	"time"

	dbdriver "github.com/stackshy/cloudemu/v2/services/database/driver"
)

// containerAttrs holds the Cosmos-only container properties the generic
// database driver has no concept of (default TTL, unique key policy),
// mirroring how offers.go tracks provisioned throughput out-of-band. Keyed by
// the qualified table name (see qualify), so it shares the container's
// database-scoped namespace.
type containerAttrs struct {
	// defaultTTL is ContainerProperties.DefaultTimeToLive. nil means TTL is
	// disabled for the container (the real default): item-level "ttl" values
	// are then inert, matching real Cosmos.
	defaultTTL *int32
	uniqueKeys []uniqueKeyDef
	// indexingPolicy is ContainerProperties.IndexingPolicy, kept verbatim so it
	// round-trips on a container read/list (the generic driver has no indexing
	// concept, same as TTL and unique keys). nil when none was declared.
	indexingPolicy map[string]any
}

// attrsStore tracks containerAttrs plus the per-item TTL bookkeeping needed to
// honor it (expiry is computed at write time from the container's default and
// any item-level "ttl" override, since the wire layer — unlike the generic
// driver — has no absolute-timestamp TTL attribute to reuse: Cosmos TTL is
// seconds-since-last-write, not an absolute epoch).
type attrsStore struct {
	mu    sync.RWMutex
	attrs map[string]containerAttrs
	// expiry maps an item identity (table|partitionValue|id) to its computed
	// expiry time. An item absent here never expires.
	expiry map[string]time.Time
}

func newAttrsStore() *attrsStore {
	return &attrsStore{attrs: make(map[string]containerAttrs), expiry: make(map[string]time.Time)}
}

func (s *attrsStore) set(table string, ttl *int32, uk *uniqueKeyPolicy, indexing map[string]any) {
	var keys []uniqueKeyDef
	if uk != nil {
		keys = uk.UniqueKeys
	}

	s.mu.Lock()
	s.attrs[table] = containerAttrs{defaultTTL: ttl, uniqueKeys: keys, indexingPolicy: indexing}
	s.mu.Unlock()
}

func (s *attrsStore) get(table string) containerAttrs {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.attrs[table]
}

// delete removes a container's attrs and every tracked item expiry under it,
// called when the container (or its owning database) is deleted.
func (s *attrsStore) delete(table string) {
	prefix := table + "|"

	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.attrs, table)

	for k := range s.expiry {
		if strings.HasPrefix(k, prefix) {
			delete(s.expiry, k)
		}
	}
}

// itemIdentity builds the key TTL bookkeeping is stored under: stable across
// create/replace/read of the same document, scoped to its partition so two
// containers (or two partitions) never collide.
func itemIdentity(table, pkAttr string, item map[string]any, id string) string {
	if pkAttr == "" || pkAttr == idAttr {
		return table + "|" + id
	}

	return table + "|" + fmt.Sprintf("%v", item[pkAttr]) + "|" + id
}

// recordWrite computes and stores (or clears) an item's TTL expiry at
// create/replace time, from the container's defaultTTL and the item's own
// "ttl" override, matching real Cosmos precedence: an item ttl of -1 always
// wins (never expires); a positive item ttl overrides the container default;
// otherwise the container default applies (itself -1 meaning no auto-expiry).
// A container with no defaultTTL declared at all ignores item-level ttl
// entirely, since TTL is off for the container.
func (s *attrsStore) recordWrite(table string, cfg *dbdriver.TableConfig, item map[string]any) {
	attrs := s.get(table)
	if attrs.defaultTTL == nil {
		return
	}

	effective := *attrs.defaultTTL
	if v, ok := numericValue(item["ttl"]); ok {
		effective = int32(v)
	}

	id, _ := item[idAttr].(string)
	key := itemIdentity(table, cfg.PartitionKey, item, id)

	s.mu.Lock()
	defer s.mu.Unlock()

	if effective <= 0 {
		// -1 (or an invalid/absent effective value) never expires; clear any
		// expiry a previous write under this identity may have recorded.
		delete(s.expiry, key)
		return
	}

	s.expiry[key] = time.Now().Add(time.Duration(effective) * time.Second)
}

// expired reports whether the item at (table, id, partition value) has
// passed its recorded TTL expiry.
func (s *attrsStore) expired(table string, cfg *dbdriver.TableConfig, item map[string]any) bool {
	id, _ := item[idAttr].(string)
	key := itemIdentity(table, cfg.PartitionKey, item, id)

	s.mu.RLock()
	exp, ok := s.expiry[key]
	s.mu.RUnlock()

	return ok && time.Now().After(exp)
}

// forget removes an item's TTL bookkeeping, called on delete.
func (s *attrsStore) forget(table string, cfg *dbdriver.TableConfig, item map[string]any) {
	id, _ := item[idAttr].(string)
	key := itemIdentity(table, cfg.PartitionKey, item, id)

	s.mu.Lock()
	delete(s.expiry, key)
	s.mu.Unlock()
}
