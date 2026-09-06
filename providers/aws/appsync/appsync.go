// Package appsync provides an in-memory mock implementation of the AWS AppSync
// control plane: GraphQL APIs, their data sources, and their API keys, plus
// resource tagging. APIs are created immediately with a stable apiId, the two
// endpoint URIs (GRAPHQL + REALTIME), and an ARN; data sources and API keys are
// nested under their owning API. Real GraphQL schema, resolvers, and query
// execution are out of scope for this control-plane surface.
package appsync

import (
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/appsync/driver"
)

// Compile-time check that Mock implements driver.AppSync.
var _ driver.AppSync = (*Mock)(nil)

// defaultMaxResults caps a page when the caller requests none.
const defaultMaxResults = 100

// apiData is the full server-side state of one GraphQL API plus its own lock.
// The nested data-source and API-key maps are guarded by mu.
type apiData struct {
	api      driver.GraphqlAPI
	dataSrcs map[string]driver.DataSource
	apiKeys  map[string]driver.APIKey
	mu       sync.RWMutex
}

// Mock is an in-memory implementation of the AWS AppSync control plane.
type Mock struct {
	apis *memstore.Store[*apiData]
	opts *config.Options
}

// New creates a new AppSync mock with the given configuration options.
func New(opts *config.Options) *Mock {
	return &Mock{
		apis: memstore.New[*apiData](),
		opts: opts,
	}
}

func (m *Mock) now() time.Time {
	return m.opts.Clock.Now().UTC()
}

// newAPIID mints a fresh, stable API id. Generated once at create and never
// regenerated, so the id (and the ARN and URIs derived from it) never drifts.
func newAPIID() string {
	return idgen.GenerateID("")
}

func (m *Mock) apiARN(apiID string) string {
	return idgen.AWSARN("appsync", m.opts.Region, m.opts.AccountID, "apis/"+apiID)
}

func (m *Mock) dataSourceARN(apiID, name string) string {
	return idgen.AWSARN("appsync", m.opts.Region, m.opts.AccountID, "apis/"+apiID+"/datasources/"+name)
}

// urisFor derives the always-populated two-key endpoint map for an API.
func (m *Mock) urisFor(apiID string) map[string]string {
	return map[string]string{
		driver.URIKeyGraphQL:  "https://" + apiID + ".appsync-api." + m.opts.Region + ".amazonaws.com/graphql",
		driver.URIKeyRealtime: "wss://" + apiID + ".appsync-realtime-api." + m.opts.Region + ".amazonaws.com/graphql",
	}
}

// getAPI resolves an API by id, returning a NotFoundException when absent.
func (m *Mock) getAPI(apiID string) (*apiData, error) {
	ad, ok := m.apis.Get(apiID)
	if !ok {
		return nil, notFound("GraphQL API %s not found", apiID)
	}

	return ad, nil
}

// apiIDFromARN extracts the owning API id from an AppSync resource ARN of the
// form arn:aws:appsync:{region}:{acct}:apis/{apiId}[/...].
func apiIDFromARN(arn string) string {
	const marker = ":apis/"

	idx := strings.Index(arn, marker)
	if idx < 0 {
		return ""
	}

	rest := arn[idx+len(marker):]
	if slash := strings.IndexByte(rest, '/'); slash >= 0 {
		return rest[:slash]
	}

	return rest
}

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

func copyExtra(in map[string]json.RawMessage) map[string]json.RawMessage {
	if in == nil {
		return nil
	}

	out := make(map[string]json.RawMessage, len(in))
	for k, v := range in {
		out[k] = append(json.RawMessage(nil), v...)
	}

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
