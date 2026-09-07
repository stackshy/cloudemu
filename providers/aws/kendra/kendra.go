// Package kendra provides an in-memory mock of the Amazon Kendra control plane:
// indexes and the data source connectors that belong to them, plus resource
// tags. An index (and a data source) is created immediately in the ACTIVE state
// with a stable id, status and createdAt/updatedAt; running a search engine and
// indexing documents are out of scope. Real Kendra index creation takes ~30
// minutes, so returning ACTIVE synchronously is what lets an IaC waiter complete
// instead of hanging.
package kendra

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/kendra/driver"
)

// Compile-time check that Mock implements driver.Kendra.
var _ driver.Kendra = (*Mock)(nil)

// defaultMaxResults caps a page when the caller requests none. Kendra documents
// a default of 10 for its list operations.
const defaultMaxResults = 10

// indexIDBytes is the number of random bytes rendered into a 36-character UUID
// index id, matching Kendra's fixed 36-character index id contract.
const indexIDBytes = 16

// dataSourceIDBytes is the number of random bytes rendered as hex into a data
// source id (a 32-character lowercase-hex string, within the documented
// [a-zA-Z0-9][a-zA-Z0-9_-]* pattern and 1–100 length).
const dataSourceIDBytes = 16

// Mock is an in-memory implementation of the Amazon Kendra control plane.
type Mock struct {
	indexes     *memstore.Store[driver.Index]
	dataSources *memstore.Store[driver.DataSource]
	opts        *config.Options
}

// New creates a new Kendra mock with the given options.
func New(opts *config.Options) *Mock {
	return &Mock{
		indexes:     memstore.New[driver.Index](),
		dataSources: memstore.New[driver.DataSource](),
		opts:        opts,
	}
}

func (m *Mock) now() time.Time {
	return m.opts.Clock.Now().UTC()
}

// newIndexID mints a fresh 36-character UUID index id. Minted once at create and
// stored, so the arn Terraform derives from it is stable across reads.
func newIndexID() string {
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

// paginate returns the offset window and next token for a slice of length n,
// honoring an opaque numeric offset token.
func paginate(n int, page driver.Page) (start, end int, next string) {
	start = decodeToken(page.NextToken)
	if start > n {
		start = n
	}

	limit := int(page.MaxResults)
	if limit <= 0 {
		limit = defaultMaxResults
	}

	end = start + limit
	if end >= n {
		return start, n, ""
	}

	return start, end, encodeToken(end)
}

func encodeToken(offset int) string {
	return strconv.Itoa(offset)
}

func decodeToken(token string) int {
	if token == "" {
		return 0
	}

	n, err := strconv.Atoi(token)
	if err != nil || n < 0 {
		return 0
	}

	return n
}
