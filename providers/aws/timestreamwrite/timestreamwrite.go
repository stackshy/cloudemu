// Package timestreamwrite provides an in-memory mock of the Amazon Timestream
// Write control plane: databases and the tables that belong to them, plus
// resource tags. A database (and a table) is created immediately with a stable
// arn, and a table is ACTIVE at once; ingesting records and running queries are
// out of scope. The computed fields clients and IaC read back — the database
// arn and KMS key, the table arn and status, the createdAt/updatedAt timestamps
// and the live table count — are minted once at create (or derived) and stored,
// so repeated reads and a later update never drift.
package timestreamwrite

import (
	"crypto/rand"
	"fmt"
	"strconv"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/timestreamwrite/driver"
)

// Compile-time check that Mock implements driver.Timestream.
var _ driver.Timestream = (*Mock)(nil)

// defaultMaxResults caps a page when the caller requests none. Timestream's list
// operations default to 20 results.
const defaultMaxResults = 20

// kmsKeyBytes is the number of random bytes rendered into the UUID of an
// AWS-managed KMS key minted for a database that did not specify one.
const kmsKeyBytes = 16

// Mock is an in-memory implementation of the Amazon Timestream Write control
// plane.
type Mock struct {
	databases *memstore.Store[driver.Database]
	tables    *memstore.Store[driver.Table]
	opts      *config.Options
}

// New creates a new Timestream Write mock with the given options.
func New(opts *config.Options) *Mock {
	return &Mock{
		databases: memstore.New[driver.Database](),
		tables:    memstore.New[driver.Table](),
		opts:      opts,
	}
}

func (m *Mock) now() time.Time {
	return m.opts.Clock.Now().UTC()
}

// databaseARN mints the stable ARN for a database:
// arn:aws:timestream:<region>:<acct>:database/<name>.
func (m *Mock) databaseARN(name string) string {
	return idgen.AWSARN("timestream", m.opts.Region, m.opts.AccountID, "database/"+name)
}

// tableARN mints the stable ARN for a table:
// arn:aws:timestream:<region>:<acct>:database/<db>/table/<name>.
func (m *Mock) tableARN(databaseName, tableName string) string {
	return idgen.AWSARN("timestream", m.opts.Region, m.opts.AccountID,
		"database/"+databaseName+"/table/"+tableName)
}

// defaultKmsKeyARN mints an AWS-managed KMS key ARN for a database that did not
// specify one, matching real Timestream (which encrypts with a service-managed
// key and reports its ARN). Minted once at create and stored, so it is stable
// across reads.
func (m *Mock) defaultKmsKeyARN() string {
	return idgen.AWSARN("kms", m.opts.Region, m.opts.AccountID, "key/"+newUUID())
}

// tableKey composes the store key for a table from its parent database name and
// its own name; a table name is unique per database.
func tableKey(databaseName, tableName string) string {
	return databaseName + "/" + tableName
}

// newUUID mints a random RFC 4122 version-4 UUID.
func newUUID() string {
	b := make([]byte, kmsKeyBytes)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand.Read never fails on supported platforms; fall back to a
		// zeroed id rather than panicking in a control-plane emulator.
		return "00000000-0000-4000-8000-000000000000"
	}

	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80

	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// tableCount returns the number of tables that belong to the named database.
// Derived live so it always reflects the tables that actually exist.
func (m *Mock) tableCount(databaseName string) int64 {
	var n int64

	tables := m.tables.SortedValues()
	for i := range tables {
		if tables[i].DatabaseName == databaseName {
			n++
		}
	}

	return n
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

func copyRetention(in *driver.RetentionProperties) *driver.RetentionProperties {
	if in == nil {
		return nil
	}

	out := *in

	return &out
}

// copyDatabase returns an alias-free copy of a database with a freshly derived
// TableCount, so callers cannot mutate stored state through the result and the
// count always reflects the current tables.
func (m *Mock) copyDatabase(d *driver.Database) driver.Database {
	out := *d
	out.Tags = copyTags(d.Tags)
	out.TableCount = m.tableCount(d.DatabaseName)

	return out
}

// copyTable returns an alias-free copy of a table.
func copyTable(t *driver.Table) driver.Table {
	out := *t
	out.Tags = copyTags(t.Tags)
	out.RetentionProperties = copyRetention(t.RetentionProperties)
	out.MagneticStoreWriteProperties = copyRaw(t.MagneticStoreWriteProperties)
	out.Schema = copyRaw(t.Schema)

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
