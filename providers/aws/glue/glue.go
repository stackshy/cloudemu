// Package glue provides an in-memory mock implementation of AWS Glue: the Data
// Catalog (databases, tables + versions, partitions, user-defined functions,
// connections, catalogs), crawlers, classifiers, ETL jobs and their runs,
// triggers, workflows, blueprints, security configurations, the schema registry
// (registries, schemas, versions), dev endpoints, and resource tags.
//
// Resource lifecycles are modeled with real state. A started job run completes
// SUCCEEDED synchronously because there is no real Spark compute plane behind
// the emulator; likewise crawler/workflow/blueprint runs settle immediately.
// Read-only analytics, ML-transform, data-quality, integration, glossary, and
// column-statistics operations return plausible synthesized results — see the
// synth.go file and docs/services.md.
package glue

import (
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/glue/driver"
)

// Compile-time check that Mock implements driver.Glue.
var _ driver.Glue = (*Mock)(nil)

const (
	// maxTags is Glue's cap on tags per resource.
	maxTags = 50
	// maxBatchGet is Glue's cap on names in a BatchGet* request.
	maxBatchGet = 25
	// keySep separates the composite-key segments in the memstores.
	keySep = "/"
)

// Mock is an in-memory implementation of AWS Glue.
type Mock struct {
	databases     *memstore.Store[*databaseData]
	tables        *memstore.Store[*tableData]
	partitions    *memstore.Store[*partitionData]
	udfs          *memstore.Store[*udfData]
	connections   *memstore.Store[*connectionData]
	catalogs      *memstore.Store[*catalogData]
	crawlers      *memstore.Store[*crawlerData]
	classifiers   *memstore.Store[*classifierData]
	jobs          *memstore.Store[*jobData]
	jobRuns       *memstore.Store[*jobRunData]
	triggers      *memstore.Store[*triggerData]
	workflows     *memstore.Store[*workflowData]
	workflowRuns  *memstore.Store[*workflowRunData]
	blueprints    *memstore.Store[*blueprintData]
	blueprintRuns *memstore.Store[*blueprintRunData]
	secConfigs    *memstore.Store[*secConfigData]
	registries    *memstore.Store[*registryData]
	schemas       *memstore.Store[*schemaData]
	devEndpoints  *memstore.Store[*devEndpointData]

	tagsMu sync.RWMutex
	tags   map[string]map[string]string // resourceARN -> tags

	policyMu sync.RWMutex
	policies map[string]string // resourceARN -> policy JSON

	encMu       sync.RWMutex
	encSettings map[string]map[string]any // catalogID -> encryption settings

	opts *config.Options
}

// New creates a new Glue mock with the given configuration options.
func New(opts *config.Options) *Mock {
	return &Mock{
		databases:     memstore.New[*databaseData](),
		tables:        memstore.New[*tableData](),
		partitions:    memstore.New[*partitionData](),
		udfs:          memstore.New[*udfData](),
		connections:   memstore.New[*connectionData](),
		catalogs:      memstore.New[*catalogData](),
		crawlers:      memstore.New[*crawlerData](),
		classifiers:   memstore.New[*classifierData](),
		jobs:          memstore.New[*jobData](),
		jobRuns:       memstore.New[*jobRunData](),
		triggers:      memstore.New[*triggerData](),
		workflows:     memstore.New[*workflowData](),
		workflowRuns:  memstore.New[*workflowRunData](),
		blueprints:    memstore.New[*blueprintData](),
		blueprintRuns: memstore.New[*blueprintRunData](),
		secConfigs:    memstore.New[*secConfigData](),
		registries:    memstore.New[*registryData](),
		schemas:       memstore.New[*schemaData](),
		devEndpoints:  memstore.New[*devEndpointData](),
		tags:          map[string]map[string]string{},
		policies:      map[string]string{},
		encSettings:   map[string]map[string]any{},
		opts:          opts,
	}
}

func (m *Mock) now() time.Time { return m.opts.Clock.Now().UTC() }

// catalogOrDefault returns the given catalog ID or the account ID when empty,
// matching real Glue where an omitted CatalogId defaults to the caller account.
func (m *Mock) catalogOrDefault(catalogID string) string {
	if catalogID == "" {
		return m.opts.AccountID
	}

	return catalogID
}

// arn builds a Glue ARN for a resource path (e.g. "database/db1",
// "table/db1/t1", "registry/r1").
func (m *Mock) arn(resource string) string {
	return idgen.AWSARN("glue", m.opts.Region, m.opts.AccountID, resource)
}

// nameKey joins composite key segments with the store separator.
func nameKey(parts ...string) string { return strings.Join(parts, keySep) }

// partitionKey encodes a partition's identifying values into a single stable
// key segment. Values can't contain the separator without escaping, so encode
// each value's length to keep the key unambiguous.
func partitionKey(catalogID, db, table string, values []string) string {
	var b strings.Builder

	b.WriteString(nameKey(catalogID, db, table))

	for _, v := range values {
		b.WriteString(keySep)
		b.WriteString(strconv.Itoa(len(v)))
		b.WriteString(":")
		b.WriteString(v)
	}

	return b.String()
}

// validName reports whether s is a non-empty Glue resource name within length
// limits. Glue names are 1–255 chars.
func validName(s string) bool {
	const maxLen = 255

	return s != "" && len(s) <= maxLen
}

// copyTags returns a deep copy of a tag map (nil-safe).
func copyTags(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}

	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}

	return out
}

// copyStrings returns a deep copy of a string slice (nil-safe).
func copyStrings(in []string) []string {
	if in == nil {
		return nil
	}

	return append([]string(nil), in...)
}
