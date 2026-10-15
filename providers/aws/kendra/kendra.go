// Package kendra provides an in-memory mock of Amazon Kendra: indexes, data
// sources and their child resources (FAQs, thesauri, experiences, block lists,
// access controls, featured results, principal mappings, query suggestions), plus
// a small data plane. Documents added with BatchPutDocument are held in the index
// and searched by Query and Retrieve with a term-matching engine (no semantic
// ranking). An index and a data source are created immediately in the ACTIVE state
// with a stable id, status and createdAt/updatedAt (CREATING first under async
// settling); real Kendra index creation takes ~30 minutes, so returning ACTIVE
// synchronously is what lets an IaC waiter complete instead of hanging.
package kendra

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/internal/settle"
	"github.com/stackshy/cloudemu/v2/services/kendra/driver"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// Compile-time checks that Mock implements driver.Kendra and every optional
// capability the wire handler serves.
var (
	_ driver.Kendra            = (*Mock)(nil)
	_ driver.Documents         = (*Mock)(nil)
	_ driver.SyncJobs          = (*Mock)(nil)
	_ driver.Faqs              = (*Mock)(nil)
	_ driver.Thesauri          = (*Mock)(nil)
	_ driver.BlockLists        = (*Mock)(nil)
	_ driver.Experiences       = (*Mock)(nil)
	_ driver.AccessControls    = (*Mock)(nil)
	_ driver.FeaturedResults   = (*Mock)(nil)
	_ driver.PrincipalMappings = (*Mock)(nil)
	_ driver.QuerySuggestions  = (*Mock)(nil)
)

// indexIDBytes is the number of random bytes rendered into a 36-character UUID
// index id, matching Kendra's fixed 36-character index id contract.
const indexIDBytes = 16

// dataSourceIDBytes is the number of random bytes rendered as hex into a data
// source id (a 32-character lowercase-hex string, within the documented
// [a-zA-Z0-9][a-zA-Z0-9_-]* pattern and 1-100 length).
const dataSourceIDBytes = 16

// Mock is an in-memory implementation of the Amazon Kendra control plane and the
// document, search and index-child capabilities built on it.
type Mock struct {
	// mu serializes every mutation (create, update, delete, ingest) so a child is
	// never inserted under a parent that a concurrent cascade delete is removing,
	// and the client-token lookup and the insert of a create are one step.
	mu sync.Mutex

	syncSeq uint64 // guarded by mu

	indexes     *memstore.Store[driver.Index]
	dataSources *memstore.Store[driver.DataSource]

	documents      *memstore.Store[storedDocument]
	syncJobs       *memstore.Store[syncJobRecord]
	faqs           *memstore.Store[driver.Faq]
	thesauri       *memstore.Store[driver.Thesaurus]
	blockLists     *memstore.Store[driver.BlockList]
	experiences    *memstore.Store[driver.Experience]
	accessControls *memstore.Store[driver.AccessControlConfiguration]
	featured       *memstore.Store[driver.FeaturedResultsSet]
	mappings       *memstore.Store[principalMapping]
	suggestions    *memstore.Store[driver.SuggestionsConfig]
	queryLog       queryLog

	// settling overlays a transient CREATING/UPDATING status on resources when
	// the server runs with async settling; it is inactive (final status at once)
	// by default so IaC waiters complete.
	settling *settle.Set

	// tokenKey signs pagination tokens so they are opaque and unforgeable.
	tokenKey []byte

	monitoring mondriver.Monitoring
	opts       *config.Options
}

// New creates a new Kendra mock with the given options.
func New(opts *config.Options) *Mock {
	return &Mock{
		indexes:        memstore.New[driver.Index](),
		dataSources:    memstore.New[driver.DataSource](),
		documents:      memstore.New[storedDocument](),
		syncJobs:       memstore.New[syncJobRecord](),
		faqs:           memstore.New[driver.Faq](),
		thesauri:       memstore.New[driver.Thesaurus](),
		blockLists:     memstore.New[driver.BlockList](),
		experiences:    memstore.New[driver.Experience](),
		accessControls: memstore.New[driver.AccessControlConfiguration](),
		featured:       memstore.New[driver.FeaturedResultsSet](),
		mappings:       memstore.New[principalMapping](),
		suggestions:    memstore.New[driver.SuggestionsConfig](),
		settling:       settle.NewSet(),
		tokenKey:       newTokenKey(),
		opts:           opts,
	}
}

// settleWindow is how long a resource reports a transient status under async
// settling.
const settleWindow = settle.DefaultClusterSettle

// settleStatus overlays the transient status of a resource (keyed by its store
// key) onto its stored final status.
func (m *Mock) settleStatus(key, final string) string {
	return m.settling.State(key, m.opts.Clock.Now(), final)
}

// beginSettle starts a transient status window for a resource; it is a no-op
// unless async settling is enabled.
func (m *Mock) beginSettle(key, transient string) {
	m.settling.Begin(key, transient, m.opts.Clock.Now(), m.opts.SettleDuration(settleWindow))
}

func (m *Mock) now() time.Time {
	return m.opts.Clock.Now().UTC()
}

// newUUID mints a fresh 36-character UUID, used for index ids and the ids of
// the index child resources. Minted once at create and
// stored, so the arn Terraform derives from it is stable across reads.
func newUUID() string {
	b := make([]byte, indexIDBytes)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand.Read never fails on supported platforms; fall back to a
		// zeroed id rather than panicking in a control-plane emulator.
		return "00000000-0000-4000-8000-000000000000"
	}

	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80

	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// newDataSourceID mints a fresh 32-character lowercase-hex data source id.
func newDataSourceID() string {
	b := make([]byte, dataSourceIDBytes)
	if _, err := rand.Read(b); err != nil {
		return "00000000000000000000000000000000"
	}

	return hex.EncodeToString(b)
}

// dataSourceKey composes the store key for a data source from its parent index
// id and its own id; a data source id is unique per index.
func dataSourceKey(indexID, id string) string {
	return indexID + "/" + id
}

func copyTags(in []driver.Tag) []driver.Tag {
	if in == nil {
		return nil
	}

	out := make([]driver.Tag, len(in))
	copy(out, in)

	return out
}

func copyRaw(in []byte) []byte {
	if in == nil {
		return nil
	}

	return append([]byte(nil), in...)
}

// copyIndex returns an alias-free copy of an index so callers cannot mutate
// stored state through the result.
func copyIndex(i *driver.Index) driver.Index {
	out := *i
	out.Tags = copyTags(i.Tags)
	out.ServerSideEncryptionConfiguration = copyRaw(i.ServerSideEncryptionConfiguration)
	out.CapacityUnits = copyRaw(i.CapacityUnits)
	out.DocumentMetadataConfigurations = copyRaw(i.DocumentMetadataConfigurations)
	out.UserGroupResolutionConfiguration = copyRaw(i.UserGroupResolutionConfiguration)
	out.UserTokenConfigurations = copyRaw(i.UserTokenConfigurations)

	return out
}

// copyDataSource returns an alias-free copy of a data source.
func copyDataSource(d *driver.DataSource) driver.DataSource {
	out := *d
	out.Tags = copyTags(d.Tags)
	out.Configuration = copyRaw(d.Configuration)
	out.VpcConfiguration = copyRaw(d.VpcConfiguration)
	out.CustomDocumentEnrichmentConfiguration = copyRaw(d.CustomDocumentEnrichmentConfiguration)

	return out
}

// deleteWithPrefix removes every entry of a store whose key starts with prefix and
// calls drop with each removed key (to drop its settle window).
func deleteWithPrefix[V any](s *memstore.Store[V], prefix string, drop func(key string)) {
	for _, k := range s.Keys() {
		if strings.HasPrefix(k, prefix) {
			s.Delete(k)
			drop(k)
		}
	}
}
