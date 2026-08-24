package loganalytics

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
)

// workspaceMeta holds the Azure-only ARM attributes of a workspace that have no
// portable log-group equivalent: the geo location supplied on create, the
// workspace GUID (customerId) Azure assigns, and the pricing SKU. The logging
// driver owns retention, tags, createdAt and identity; this metadata is the
// wire layer's own so the shared driver stays cloud-neutral.
type workspaceMeta struct {
	Location   string
	CustomerID string
	SKU        string
}

// metaStore is a concurrency-safe map of workspace name to its ARM metadata.
type metaStore struct {
	mu sync.RWMutex
	m  map[string]*workspaceMeta
}

func newMetaStore() *metaStore {
	return &metaStore{m: make(map[string]*workspaceMeta)}
}

// upsert records the metadata for a workspace on create/update. The customerId
// GUID is assigned once (on first create) and preserved across updates so a
// client that re-reads the workspace always sees the same workspace ID.
func (s *metaStore) upsert(name, resourceID, location, sku string) *workspaceMeta {
	s.mu.Lock()
	defer s.mu.Unlock()

	meta := s.m[name]
	if meta == nil {
		meta = &workspaceMeta{CustomerID: workspaceGUID(resourceID)}
		s.m[name] = meta
	}

	if location != "" {
		meta.Location = location
	}

	if sku != "" {
		meta.SKU = sku
	} else if meta.SKU == "" {
		meta.SKU = defaultSKUName
	}

	clone := *meta

	return &clone
}

// get returns the metadata for a workspace, synthesizing defaults for one that
// was created out-of-band (e.g. via the portable logging API) and therefore has
// no wire-layer record: a deterministic customerId, the default SKU, and no
// location (which real ARM tolerates on read).
func (s *metaStore) get(name, resourceID string) *workspaceMeta {
	s.mu.RLock()
	meta := s.m[name]
	s.mu.RUnlock()

	if meta != nil {
		clone := *meta

		return &clone
	}

	return &workspaceMeta{CustomerID: workspaceGUID(resourceID), SKU: defaultSKUName}
}

func (s *metaStore) delete(name string) {
	s.mu.Lock()
	delete(s.m, name)
	s.mu.Unlock()
}

// workspaceGUID derives a stable RFC-4122-shaped GUID from the workspace's ARM
// resource ID. Real Log Analytics assigns a random workspace ID; deriving it
// from the (unique) resource ID makes it stable across reads without any extra
// stored state, which is what a client keying on customerId needs.
func workspaceGUID(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	h := hex.EncodeToString(sum[:])

	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}
