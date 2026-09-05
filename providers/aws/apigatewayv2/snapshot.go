package apigatewayv2

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/apigatewayv2/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// apigatewayV2Snapshot is the full serialized state of the mock. The apis store
// holds an unexported *apiData whose tree lives in unexported fields (invisible
// to json.Marshal), so each API is promoted to an exported form keyed by API id.
// The per-API lock, wired opts and region are not serialized.
type apigatewayV2Snapshot struct {
	APIs map[string]*apiSnapshot `json:"apis,omitempty"`
}

// apiSnapshot is the exported form of apiData: the API plus its routes,
// integrations and stages, all under their original identities.
type apiSnapshot struct {
	API          driver.API                     `json:"api"`
	Routes       map[string]*driver.Route       `json:"routes,omitempty"`
	Integrations map[string]*driver.Integration `json:"integrations,omitempty"`
	Stages       map[string]*driver.Stage       `json:"stages,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// apigatewayv2 holds only control-plane definitions, no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	snap := apigatewayV2Snapshot{}

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
		API:          copyAPI(&ad.api),
		Routes:       make(map[string]*driver.Route, len(ad.routes)),
		Integrations: make(map[string]*driver.Integration, len(ad.integrations)),
		Stages:       make(map[string]*driver.Stage, len(ad.stages)),
	}

	for id, r := range ad.routes {
		cp := copyRoute(r)
		as.Routes[id] = &cp
	}

	for id, ig := range ad.integrations {
		cp := copyIntegration(ig)
		as.Integrations[id] = &cp
	}

	for name, s := range ad.stages {
		cp := copyStage(s)
		as.Stages[name] = &cp
	}

	return as
}

// Restore rebuilds the mock's state under the original identities: every API id,
// route id, integration id and stage name is preserved.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap apigatewayV2Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("apigatewayv2: parse snapshot: %w", err)
	}

	for id, as := range snap.APIs {
		m.apis.Set(id, restoreAPI(as))
	}

	return nil
}

// restoreAPI rebuilds an apiData from its exported snapshot form.
func restoreAPI(as *apiSnapshot) *apiData {
	ad := &apiData{
		api:          as.API,
		routes:       make(map[string]*driver.Route, len(as.Routes)),
		integrations: make(map[string]*driver.Integration, len(as.Integrations)),
		stages:       make(map[string]*driver.Stage, len(as.Stages)),
	}

	for id, r := range as.Routes {
		ad.routes[id] = r
	}

	for id, ig := range as.Integrations {
		ad.integrations[id] = ig
	}

	for name, s := range as.Stages {
		ad.stages[name] = s
	}

	return ad
}
