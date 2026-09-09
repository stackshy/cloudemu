// Package grafana provides an in-memory mock implementation of the Amazon
// Managed Grafana control plane: workspaces plus their configuration string,
// authentication summary and resource tags. A workspace is created immediately
// in the ACTIVE state with a stable id (g-[0-9a-f]{10}), arn, endpoint,
// grafanaVersion and created timestamp; its nested vpcConfiguration and
// networkAccessControl blocks are carried verbatim so a round-tripped workspace
// reflects everything the caller sent. Running a Grafana server is out of
// scope: this is a control-plane-only surface.
package grafana

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/grafana/driver"
)

// Compile-time check that Mock implements driver.Grafana.
var _ driver.Grafana = (*Mock)(nil)

// defaultMaxResults caps a page when the caller requests none.
const defaultMaxResults = 25

// defaultGrafanaVersion is the version assigned to a workspace that does not
// request one. It is a computed default, so it is stable across reads and never
// drifts an IaC plan.
const defaultGrafanaVersion = "9.4"

// defaultConfiguration is the configuration string reported for a workspace
// that did not supply one. The Grafana configuration is a JSON string; an empty
// object is the stable default.
const defaultConfiguration = "{}"

// arnMarker scopes a Grafana resource ARN.
const arnMarker = ":grafana:"

// idBytes is the number of random bytes rendered into a workspace id: ten hex
// characters, matching the g-[0-9a-f]{10} pattern.
const idBytes = 5

// Mock is an in-memory implementation of the Amazon Managed Grafana control
// plane.
type Mock struct {
	workspaces *memstore.Store[driver.Workspace]
	opts       *config.Options
}

// New creates a new Grafana mock with the given configuration options.
func New(opts *config.Options) *Mock {
	return &Mock{
		workspaces: memstore.New[driver.Workspace](),
		opts:       opts,
	}
}

func (m *Mock) now() time.Time {
	return m.opts.Clock.Now().UTC()
}

// newID mints a fresh workspace id of the form g-[0-9a-f]{10}. The id is minted
// once at create and stored, so every field derived from it (arn, endpoint) is
// stable across reads.
func newID() string {
	b := make([]byte, idBytes)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand.Read never fails on supported platforms; fall back to a
		// zeroed id rather than panicking in a control-plane emulator.
		return "g-0000000000"
	}

	return fmt.Sprintf("g-%x", b)
}

// workspaceARN mints the stable ARN for a workspace. The Grafana resource part
// carries a leading slash (arn:aws:grafana:{region}:{acct}:/workspaces/{id}),
// so the ARN marker scoping and parsing round-trip exactly.
func (m *Mock) workspaceARN(id string) string {
	return idgen.AWSARN("grafana", m.opts.Region, m.opts.AccountID, "/workspaces/"+id)
}

// workspaceEndpoint mints the stable Grafana console endpoint for a workspace.
func (m *Mock) workspaceEndpoint(id string) string {
	return fmt.Sprintf("%s.grafana-workspace.%s.amazonaws.com", id, m.opts.Region)
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

func copyStrings(in []string) []string {
	if in == nil {
		return nil
	}

	out := make([]string, len(in))
	copy(out, in)

	return out
}

func copyRaw(in json.RawMessage) json.RawMessage {
	if in == nil {
		return nil
	}

	return append(json.RawMessage(nil), in...)
}

// copyWorkspace returns an alias-free copy of a workspace so callers cannot
// mutate stored state through the result.
func copyWorkspace(w *driver.Workspace) driver.Workspace {
	out := *w
	out.Tags = copyTags(w.Tags)
	out.AuthenticationProviders = copyStrings(w.AuthenticationProviders)
	out.DataSources = copyStrings(w.DataSources)
	out.NotificationDestinations = copyStrings(w.NotificationDestinations)
	out.OrganizationalUnits = copyStrings(w.OrganizationalUnits)
	out.VpcConfiguration = copyRaw(w.VpcConfiguration)
	out.NetworkAccessControl = copyRaw(w.NetworkAccessControl)

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
