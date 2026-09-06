package appsync

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/appsync/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// appsyncSnapshot is the full serialized state of the AppSync mock. The apis
// store holds an unexported *apiData (whose fields are unexported and invisible
// to json.Marshal), so it is promoted to an exported apiSnapshot keyed by
// apiId. The per-API mutex and the wired opts are intentionally not serialized.
type appsyncSnapshot struct {
	APIs map[string]*apiSnapshot `json:"apis,omitempty"`
}

// apiSnapshot is the exported form of apiData.
type apiSnapshot struct {
	API      driver.GraphqlAPI            `json:"api"`
	DataSrcs map[string]driver.DataSource `json:"dataSources,omitempty"`
	APIKeys  map[string]driver.APIKey     `json:"apiKeys,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// AppSync is control-plane only and holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	if m.apis.Len() == 0 {
		return json.Marshal(appsyncSnapshot{})
	}

	out := make(map[string]*apiSnapshot, m.apis.Len())

	for apiID, ad := range m.apis.All() {
		ad.mu.RLock()
		out[apiID] = &apiSnapshot{
			API:      ad.api,
			DataSrcs: ad.dataSrcs,
			APIKeys:  ad.apiKeys,
		}
		ad.mu.RUnlock()
	}

	b, err := json.Marshal(appsyncSnapshot{APIs: out})
	if err != nil {
		return nil, fmt.Errorf("appsync: snapshot: %w", err)
	}

	return b, nil
}

// Restore rebuilds the mock's state under the original identities: every apiId,
// data-source name, and API-key id (and the ARNs/URIs derived from them) is
// preserved, so a restore is transparent to clients.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap appsyncSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("appsync: parse snapshot: %w", err)
	}

	for apiID, as := range snap.APIs {
		ad := &apiData{
			api:      as.API,
			dataSrcs: as.DataSrcs,
			apiKeys:  as.APIKeys,
		}
		if ad.dataSrcs == nil {
			ad.dataSrcs = map[string]driver.DataSource{}
		}

		if ad.apiKeys == nil {
			ad.apiKeys = map[string]driver.APIKey{}
		}

		m.apis.Set(apiID, ad)
	}

	return nil
}
