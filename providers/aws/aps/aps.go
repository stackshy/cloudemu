// Package aps provides an in-memory mock of the Amazon Managed Service for
// Prometheus (APS) control plane: workspaces plus their rule-groups namespaces,
// alert-manager definition, logging configuration and resource tagging. A
// workspace is created immediately in the ACTIVE state with a stable arn,
// workspaceId (ws-<uuid>), prometheusEndpoint and createdAt; its child resources
// are likewise created ACTIVE and their definition blobs are carried verbatim so
// they round-trip byte-for-byte. Ingesting and querying Prometheus metrics is
// out of scope: this is a control-plane-only surface.
package aps

import (
	"strconv"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/aps/driver"
)

// Compile-time check that Mock implements driver.APS.
var _ driver.APS = (*Mock)(nil)

// defaultMaxResults caps a page when the caller requests none.
const defaultMaxResults = 100

// arnMarker scopes an APS resource ARN.
const arnMarker = ":aps:"

// Resource segments of the two taggable APS ARN kinds.
const (
	kindWorkspace           = "workspace"
	kindRuleGroupsNamespace = "rulegroupsnamespace"
)

// arnFieldCount is the number of colon-separated fields in an APS resource ARN
// (arn:aws:aps:{region}:{acct}:{resource}).
const arnFieldCount = 6

// rgNamespaceSegs is the segment count of a rule-groups-namespace ARN tail
// (rulegroupsnamespace/{workspaceId}/{name}).
const rgNamespaceSegs = 3

// workspaceSegs is the segment count of a workspace ARN tail (workspace/{id}).
const workspaceSegs = 2

// workspaceIDPrefix is the AWS-assigned prefix on a workspace id (ws-<uuid>).
const workspaceIDPrefix = "ws-"

// Mock is an in-memory implementation of the Amazon APS control plane.
type Mock struct {
	workspaces *memstore.Store[driver.Workspace]
	opts       *config.Options
}

// New creates a new APS mock with the given configuration options.
func New(opts *config.Options) *Mock {
	return &Mock{
		workspaces: memstore.New[driver.Workspace](),
		opts:       opts,
	}
}

func (m *Mock) now() time.Time {
	return m.opts.Clock.Now().UTC()
}

// newWorkspaceID mints a fresh ws-<uuid> workspace id.
func newWorkspaceID() string {
	return workspaceIDPrefix + idgen.UUID()
}

// workspaceARN mints the stable ARN reported for a workspace.
func (m *Mock) workspaceARN(id string) string {
	return idgen.AWSARN("aps", m.opts.Region, m.opts.AccountID, kindWorkspace+"/"+id)
}

// ruleGroupsNamespaceARN mints the stable ARN reported for a rule-groups
// namespace under a workspace.
func (m *Mock) ruleGroupsNamespaceARN(workspaceID, name string) string {
	return idgen.AWSARN("aps", m.opts.Region, m.opts.AccountID,
		kindRuleGroupsNamespace+"/"+workspaceID+"/"+name)
}

// prometheusEndpoint mints the stable Prometheus endpoint for a workspace.
func (m *Mock) prometheusEndpoint(id string) string {
	return "https://aps-workspaces." + m.opts.Region + ".amazonaws.com/workspaces/" + id + "/"
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

// copyWorkspace returns an alias-free copy of a workspace so callers cannot
// mutate stored state through the result. The child maps and pointers are deep
// copied.
func copyWorkspace(w *driver.Workspace) driver.Workspace {
	out := *w
	out.Tags = copyTags(w.Tags)

	if w.RuleGroups != nil {
		rg := make(map[string]driver.RuleGroupsNamespace, len(w.RuleGroups))

		for k, v := range w.RuleGroups {
			v.Tags = copyTags(v.Tags)
			rg[k] = v
		}

		out.RuleGroups = rg
	}

	if w.AlertManager != nil {
		am := *w.AlertManager
		out.AlertManager = &am
	}

	if w.Logging != nil {
		lg := *w.Logging
		out.Logging = &lg
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

// encodeToken encodes a numeric list offset as an opaque pagination token.
func encodeToken(offset int) string {
	return strconv.Itoa(offset)
}

// decodeToken decodes an opaque pagination token to a numeric offset. An empty
// or malformed token decodes to 0 (start from the beginning).
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
