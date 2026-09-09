// Package appflow provides an in-memory mock implementation of the AWS AppFlow
// control plane: flows (managed data-transfer configurations) and connector
// profiles, plus resource tagging. A flow is created immediately with a stable
// flowArn, an Active status, and create/update timestamps; its rich
// configuration blocks (sourceFlowConfig, destinationFlowConfigList, tasks,
// triggerConfig, metadataCatalogConfig) are carried verbatim so a round-tripped
// flow reflects everything the caller sent. Actually running a flow and
// transferring data are out of scope: StartFlow/StopFlow only move a flow's
// status.
package appflow

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/appflow/driver"
)

// Compile-time check that Mock implements driver.AppFlow.
var _ driver.AppFlow = (*Mock)(nil)

// defaultMaxResults caps a page when the caller requests none.
const defaultMaxResults = 100

// Resource kinds parsed from an AppFlow ARN's resource segment.
const (
	kindFlow             = "flow"
	kindConnectorProfile = "connectorprofile"
)

// Mock is an in-memory implementation of the AWS AppFlow control plane.
type Mock struct {
	flows    *memstore.Store[driver.Flow]
	profiles *memstore.Store[driver.ConnectorProfile]
	opts     *config.Options
}

// New creates a new AppFlow mock with the given configuration options.
func New(opts *config.Options) *Mock {
	return &Mock{
		flows:    memstore.New[driver.Flow](),
		profiles: memstore.New[driver.ConnectorProfile](),
		opts:     opts,
	}
}

func (m *Mock) now() time.Time {
	return m.opts.Clock.Now().UTC()
}

func (m *Mock) flowARN(flowName string) string {
	return idgen.AWSARN("appflow", m.opts.Region, m.opts.AccountID, "flow/"+flowName)
}

func (m *Mock) connectorProfileARN(name string) string {
	return idgen.AWSARN("appflow", m.opts.Region, m.opts.AccountID, "connectorprofile/"+name)
}

// credentialsARN mints the Secrets Manager ARN AppFlow reports for a connector
// profile's stored credentials. It is derived from the profile name, so it is
// stable across reads.
func (m *Mock) credentialsARN(name string) string {
	return idgen.AWSARN("secretsmanager", m.opts.Region, m.opts.AccountID, "secret:appflow!"+name)
}

// createdBy is the caller identity AppFlow records on create; a stable, derived
// value so repeated reads never drift.
func (m *Mock) createdBy() string {
	return fmt.Sprintf("arn:aws:iam::%s:root", m.opts.AccountID)
}

// resourceNameFromARN extracts the flow or connector-profile name from an
// AppFlow resource ARN of the form
// arn:aws:appflow:{region}:{acct}:{flow|connectorprofile}/{name}.
func resourceNameFromARN(arn string) (kind, name string) {
	const marker = ":appflow:"
	if !strings.Contains(arn, marker) {
		return "", ""
	}

	tail := arn[strings.LastIndex(arn, ":")+1:]

	slash := strings.IndexByte(tail, '/')
	if slash < 0 {
		return "", ""
	}

	return tail[:slash], tail[slash+1:]
}

// summarizeConnectorTypes peeks into the verbatim config blocks to derive the
// source/destination connector types and trigger type used by ListFlows'
// FlowDefinition summary. Malformed blocks yield empty strings rather than an
// error: the summary is best-effort and the full blocks still round-trip.
func summarizeConnectorTypes(extra map[string]json.RawMessage) (srcType, dstType, triggerType string) {
	if raw, ok := extra["sourceFlowConfig"]; ok {
		var sfc struct {
			ConnectorType string `json:"connectorType"`
		}

		if json.Unmarshal(raw, &sfc) == nil {
			srcType = sfc.ConnectorType
		}
	}

	if raw, ok := extra["destinationFlowConfigList"]; ok {
		var dfc []struct {
			ConnectorType string `json:"connectorType"`
		}

		if json.Unmarshal(raw, &dfc) == nil && len(dfc) > 0 {
			dstType = dfc[0].ConnectorType
		}
	}

	if raw, ok := extra["triggerConfig"]; ok {
		var tc struct {
			TriggerType string `json:"triggerType"`
		}

		if json.Unmarshal(raw, &tc) == nil {
			triggerType = tc.TriggerType
		}
	}

	return srcType, dstType, triggerType
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

// copyFlow returns an alias-free copy of a flow so callers cannot mutate stored
// state through the result.
func copyFlow(f *driver.Flow) driver.Flow {
	out := *f
	out.Tags = copyTags(f.Tags)
	out.Extra = copyExtra(f.Extra)

	return out
}

// copyProfile returns an alias-free copy of a connector profile.
func copyProfile(p *driver.ConnectorProfile) driver.ConnectorProfile {
	out := *p
	out.Tags = copyTags(p.Tags)
	out.Extra = copyExtra(p.Extra)

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
