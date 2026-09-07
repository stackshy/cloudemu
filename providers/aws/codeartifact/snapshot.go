package codeartifact

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/codeartifact/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// codeartifactSnapshot is the full serialized state of the CodeArtifact mock. The
// stores hold exported driver types, so they serialize directly, keyed by domain
// name and by "<domain>/<repository>". The wired opts are not serialized.
type codeartifactSnapshot struct {
	Domains      map[string]driver.Domain     `json:"domains,omitempty"`
	Repositories map[string]driver.Repository `json:"repositories,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// CodeArtifact is control-plane only and holds no bulk assets.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	snap := codeartifactSnapshot{}

	if m.domains.Len() > 0 {
		snap.Domains = m.domains.All()
	}

	if m.repos.Len() > 0 {
		snap.Repositories = m.repos.All()
	}

	b, err := json.Marshal(snap)
	if err != nil {
		return nil, fmt.Errorf("codeartifact: snapshot: %w", err)
	}

	return b, nil
}

// Restore rebuilds the mock's state under the original identities: every domain
// name and repository key (and the ARN and computed fields derived from them) is
// preserved, so a restore is transparent to clients.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap codeartifactSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("codeartifact: parse snapshot: %w", err)
	}

	for name := range snap.Domains {
		m.domains.Set(name, snap.Domains[name])
	}

	for key := range snap.Repositories {
		m.repos.Set(key, snap.Repositories[key])
	}

	return nil
}
