package appflow

import (
	"encoding/json"
	"time"

	"github.com/stackshy/cloudemu/v2/services/appflow/driver"
)

// createFlowBody is the modeled subset of a CreateFlow request. Every other
// field (sourceFlowConfig, destinationFlowConfigList, tasks, triggerConfig,
// metadataCatalogConfig) is carried through Extra so it round-trips verbatim.
type createFlowBody struct {
	FlowName    string            `json:"flowName"`
	Description string            `json:"description"`
	KmsArn      string            `json:"kmsArn"`
	Tags        map[string]string `json:"tags"`
}

// createFlowModeledKeys are the request fields createFlowBody owns plus the
// idempotency token; they are stripped from Extra so they are not echoed back.
//
//nolint:gochecknoglobals // immutable key set, read-only after init.
var createFlowModeledKeys = []string{"flowName", "description", "kmsArn", "tags", "clientToken"}

// updateFlowBody is the modeled subset of an UpdateFlow request. flowName
// identifies the flow; description is mutable.
type updateFlowBody struct {
	FlowName    string `json:"flowName"`
	Description string `json:"description"`
}

//nolint:gochecknoglobals // immutable key set, read-only after init.
var updateFlowModeledKeys = []string{"flowName", "description", "clientToken"}

// connectorProfileBody is the modeled subset of a Create/UpdateConnectorProfile
// request. The connectorProfileConfig block is carried through Extra verbatim.
type connectorProfileBody struct {
	ConnectorProfileName string `json:"connectorProfileName"`
	ConnectorType        string `json:"connectorType"`
	ConnectorLabel       string `json:"connectorLabel"`
	ConnectionMode       string `json:"connectionMode"`
}

//nolint:gochecknoglobals // immutable key set, read-only after init.
var connectorProfileModeledKeys = []string{
	"connectorProfileName", "connectorType", "connectorLabel", "connectionMode",
}

// pageBody carries the body-borne pagination parameters shared by ListFlows and
// DescribeConnectorProfiles.
type pageBody struct {
	MaxResults int32  `json:"maxResults"`
	NextToken  string `json:"nextToken"`
}

// extraFrom returns the raw request fields not owned by a modeled struct.
func extraFrom(raw map[string]json.RawMessage, modeled []string) map[string]json.RawMessage {
	if len(raw) == 0 {
		return nil
	}

	out := make(map[string]json.RawMessage, len(raw))
	for k, v := range raw {
		out[k] = v
	}

	for _, k := range modeled {
		delete(out, k)
	}

	if len(out) == 0 {
		return nil
	}

	return out
}

// epochOrNil renders a time as a Unix-epoch-seconds float the AppFlow SDK
// decodes into a *time.Time, or nil for the zero time.
func epochOrNil(t time.Time) *float64 {
	if t.IsZero() {
		return nil
	}

	secs := float64(t.Unix())

	return &secs
}

// flowToWire renders a full flow as its DescribeFlow restJson1 object. Extra is
// emitted first so the computed fields always win.
func flowToWire(f *driver.Flow) map[string]any {
	out := map[string]any{}
	for k, v := range f.Extra {
		out[k] = v
	}

	out["flowArn"] = f.FlowArn
	out["flowName"] = f.FlowName
	out["flowStatus"] = f.FlowStatus
	out["createdAt"] = epochOrNil(f.CreatedAt)
	out["lastUpdatedAt"] = epochOrNil(f.LastUpdatedAt)
	out["createdBy"] = f.CreatedBy
	out["lastUpdatedBy"] = f.LastUpdatedBy

	if f.Description != "" {
		out["description"] = f.Description
	}

	if f.KmsArn != "" {
		out["kmsArn"] = f.KmsArn
	}

	if f.Tags != nil {
		out["tags"] = f.Tags
	}

	return out
}

// flowDefinitionToWire renders a flow as its ListFlows FlowDefinition summary.
func flowDefinitionToWire(f *driver.Flow) map[string]any {
	out := map[string]any{
		"flowArn":       f.FlowArn,
		"flowName":      f.FlowName,
		"flowStatus":    f.FlowStatus,
		"createdAt":     epochOrNil(f.CreatedAt),
		"lastUpdatedAt": epochOrNil(f.LastUpdatedAt),
		"createdBy":     f.CreatedBy,
		"lastUpdatedBy": f.LastUpdatedBy,
	}

	if f.Description != "" {
		out["description"] = f.Description
	}

	if f.SourceConnectorType != "" {
		out["sourceConnectorType"] = f.SourceConnectorType
	}

	if f.DestinationConnectorType != "" {
		out["destinationConnectorType"] = f.DestinationConnectorType
	}

	if f.TriggerType != "" {
		out["triggerType"] = f.TriggerType
	}

	if f.Tags != nil {
		out["tags"] = f.Tags
	}

	return out
}

// connectorProfileToWire renders a connector profile as its
// DescribeConnectorProfiles restJson1 object.
func connectorProfileToWire(p *driver.ConnectorProfile) map[string]any {
	out := map[string]any{}
	for k, v := range p.Extra {
		out[k] = v
	}

	out["connectorProfileArn"] = p.ConnectorProfileArn
	out["connectorProfileName"] = p.ConnectorProfileName
	out["connectorType"] = p.ConnectorType
	out["credentialsArn"] = p.CredentialsArn
	out["createdAt"] = epochOrNil(p.CreatedAt)
	out["lastUpdatedAt"] = epochOrNil(p.LastUpdatedAt)

	if p.ConnectorLabel != "" {
		out["connectorLabel"] = p.ConnectorLabel
	}

	if p.ConnectionMode != "" {
		out["connectionMode"] = p.ConnectionMode
	}

	return out
}
