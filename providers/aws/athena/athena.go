// Package athena provides an in-memory mock implementation of AWS Athena: the
// interactive-query control plane. It models workgroups (with their result and
// engine configuration and usage controls), saved (named) queries, query
// executions, and the read side of the Data Catalog (databases and data
// catalogs) that the query-execution DDL path populates.
//
// There is no real Presto/Trino compute plane behind the emulator, so a started
// query execution settles to SUCCEEDED synchronously and CREATE/DROP DATABASE
// DDL statements mutate the in-memory catalog directly. Statement types are
// classified from the query text. The implicit "primary" workgroup and the
// default "AwsDataCatalog" data catalog are seeded on construction, matching
// real Athena.
package athena

import (
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/athena/driver"
)

// Compile-time check that Mock implements driver.Athena.
var _ driver.Athena = (*Mock)(nil)

// keySep separates the catalog and database segments in the databases store.
const keySep = "/"

// minBytesScannedCutoff is Athena's floor for BytesScannedCutoffPerQuery.
const minBytesScannedCutoff = 10_000_000

// Mock is an in-memory implementation of AWS Athena.
type Mock struct {
	workGroups      *memstore.Store[driver.WorkGroup]
	namedQueries    *memstore.Store[driver.NamedQuery]
	queryExecutions *memstore.Store[driver.QueryExecution]
	databases       *memstore.Store[driver.Database]
	dataCatalogs    *memstore.Store[driver.DataCatalog]

	// mu serializes compound read-modify-write mutations (workgroup update,
	// recursive delete) that span more than one store operation.
	mu sync.Mutex

	// tokenMu guards the client-request-token idempotency index.
	tokenMu sync.Mutex
	tokens  map[string]string // ClientRequestToken -> QueryExecutionId

	// tagsMu guards the resource-tag side map, keyed by resource ARN.
	tagsMu sync.RWMutex
	tags   map[string]map[string]string

	// seq is the monotonic ordering key stamped on each query execution so
	// ListQueryExecutions can return them most-recent-first deterministically.
	seq atomic.Int64

	opts *config.Options
}

// New creates a new Athena mock, seeding the implicit "primary" workgroup and
// the default "AwsDataCatalog" data catalog.
func New(opts *config.Options) *Mock {
	m := &Mock{
		workGroups:      memstore.New[driver.WorkGroup](),
		namedQueries:    memstore.New[driver.NamedQuery](),
		queryExecutions: memstore.New[driver.QueryExecution](),
		databases:       memstore.New[driver.Database](),
		dataCatalogs:    memstore.New[driver.DataCatalog](),
		tokens:          map[string]string{},
		tags:            map[string]map[string]string{},
		opts:            opts,
	}
	m.seed()

	return m
}

// seed installs the implicit primary workgroup and the default data catalog.
func (m *Mock) seed() {
	primary := driver.WorkGroup{
		Name:         driver.DefaultWorkGroup,
		State:        driver.WorkGroupStateEnabled,
		Description:  "",
		CreationTime: m.now(),
		Configuration: driver.WorkGroupConfiguration{
			EnforceWorkGroupConfiguration:   boolPtr(true),
			PublishCloudWatchMetricsEnabled: boolPtr(true),
			RequesterPaysEnabled:            boolPtr(false),
			EngineVersion:                   resolveEngineVersion(driver.EngineVersion{}),
		},
	}
	m.workGroups.SetIfAbsent(primary.Name, copyWorkGroup(primary))

	m.dataCatalogs.SetIfAbsent(driver.DefaultDataCatalog, driver.DataCatalog{
		Name: driver.DefaultDataCatalog,
		Type: "GLUE",
	})
}

func (m *Mock) now() time.Time { return m.opts.Clock.Now().UTC() }

// databaseKey builds the composite key for a database within a data catalog.
func databaseKey(catalog, name string) string { return catalog + keySep + name }

// workGroupARN builds the ARN a workgroup's tags are keyed under, matching the
// ARN the Terraform AWS provider computes for ListTagsForResource.
func (m *Mock) workGroupARN(name string) string {
	return idgen.AWSARN("athena", m.opts.Region, m.opts.AccountID, "workgroup/"+name)
}

// resolveEngineVersion materializes EffectiveEngineVersion from the selected
// version: an explicit non-AUTO selection is echoed as effective; AUTO (or an
// empty selection) resolves to the current default engine version. This mirrors
// real Athena, where omitting EffectiveEngineVersion would leave the workgroup
// perpetually drifting for tools that read it back (Terraform).
func resolveEngineVersion(ev driver.EngineVersion) driver.EngineVersion {
	selected := ev.SelectedEngineVersion
	if selected == "" {
		selected = driver.EngineVersionAuto
	}

	effective := selected
	if strings.EqualFold(selected, driver.EngineVersionAuto) {
		effective = driver.DefaultEffectiveEngineVersion
	}

	return driver.EngineVersion{SelectedEngineVersion: selected, EffectiveEngineVersion: effective}
}

func boolPtr(b bool) *bool { return &b }
