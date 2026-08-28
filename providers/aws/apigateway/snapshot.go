package apigateway

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/apigateway/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// apigatewaySnapshot is the full serialized state of the API Gateway mock. The
// apis store holds an unexported *apiData whose tree lives in unexported fields
// (invisible to json.Marshal), so each API is promoted to an exported form keyed
// by REST API id. The per-API lock and the wired opts are not serialized.
type apigatewaySnapshot struct {
	APIs map[string]*apiSnapshot `json:"apis,omitempty"`
}

// apiSnapshot is the exported form of apiData: the REST API plus its resource
// tree, deployments and stages, all under their original identities.
type apiSnapshot struct {
	API         driver.RestAPI                `json:"api"`
	Resources   map[string]*driver.Resource   `json:"resources,omitempty"`
	Deployments map[string]*driver.Deployment `json:"deployments,omitempty"`
	Stages      map[string]*driver.Stage      `json:"stages,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// API Gateway holds only control-plane definitions, no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	snap := apigatewaySnapshot{}

	if m.apis.Len() > 0 {
		snap.APIs = make(map[string]*apiSnapshot, m.apis.Len())

		for id, ad := range m.apis.All() {
			snap.APIs[id] = snapshotAPI(ad)
		}
	}

	return json.Marshal(snap)
}

// snapshotAPI deep-copies one API's tree under its lock so the marshal that
// follows never races a concurrent mutation.
func snapshotAPI(ad *apiData) *apiSnapshot {
	ad.mu.RLock()
	defer ad.mu.RUnlock()

	as := &apiSnapshot{
		API:         copyAPI(&ad.api),
		Resources:   make(map[string]*driver.Resource, len(ad.resources)),
		Deployments: make(map[string]*driver.Deployment, len(ad.deployments)),
		Stages:      make(map[string]*driver.Stage, len(ad.stages)),
	}

	for rid, r := range ad.resources {
		cp := copyResource(r)
		as.Resources[rid] = &cp
	}

	for did, d := range ad.deployments {
		cp := *d
		as.Deployments[did] = &cp
	}

	for name, s := range ad.stages {
		cp := copyStage(s)
		as.Stages[name] = &cp
	}

	return as
}

// Restore rebuilds the mock's state under the original identities: every REST
// API id, resource id, deployment id and stage name is preserved.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap apigatewaySnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("apigateway: parse snapshot: %w", err)
	}

	for id, as := range snap.APIs {
		m.apis.Set(id, restoreAPI(as))
	}

	return nil
}

// restoreAPI rebuilds an apiData from its exported snapshot form.
func restoreAPI(as *apiSnapshot) *apiData {
	ad := &apiData{
		api:         as.API,
		resources:   make(map[string]*driver.Resource, len(as.Resources)),
		deployments: make(map[string]*driver.Deployment, len(as.Deployments)),
		stages:      make(map[string]*driver.Stage, len(as.Stages)),
	}

	for rid, r := range as.Resources {
		ad.resources[rid] = r
	}

	for did, d := range as.Deployments {
		ad.deployments[did] = d
	}

	for name, s := range as.Stages {
		ad.stages[name] = s
	}

	return ad
}
