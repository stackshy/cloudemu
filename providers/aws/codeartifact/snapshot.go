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
// stores hold exported types, so they serialize directly. The wired opts and
// event publisher are not serialized. Asset bytes are included only when the
// snapshot asks for assets; without them package versions keep their assets'
// names, sizes and hashes but the content is gone.
type codeartifactSnapshot struct {
	Domains        map[string]driver.Domain         `json:"domains,omitempty"`
	Repositories   map[string]driver.Repository     `json:"repositories,omitempty"`
	DomainPolicies map[string]driver.ResourcePolicy `json:"domainPolicies,omitempty"`
	RepoPolicies   map[string]driver.ResourcePolicy `json:"repoPolicies,omitempty"`
	Packages       map[string]*packageRecord        `json:"packages,omitempty"`
	Groups         map[string]*groupRecord          `json:"groups,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets selects
// whether asset bytes are part of it.
func (m *Mock) Snapshot(_ context.Context, includeAssets bool) (json.RawMessage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	snap := codeartifactSnapshot{}

	if m.domains.Len() > 0 {
		snap.Domains = m.domains.All()
	}

	if m.repos.Len() > 0 {
		snap.Repositories = m.repos.All()
	}

	if m.domainPolicies.Len() > 0 {
		snap.DomainPolicies = m.domainPolicies.All()
	}

	if m.repoPolicies.Len() > 0 {
		snap.RepoPolicies = m.repoPolicies.All()
	}

	if m.packages.Len() > 0 {
		snap.Packages = m.packages.All()

		if !includeAssets {
			snap.Packages = stripAssetBytes(snap.Packages)
		}
	}

	if m.groups.Len() > 0 {
		snap.Groups = m.groups.All()
	}

	b, err := json.Marshal(snap)
	if err != nil {
		return nil, fmt.Errorf("codeartifact: snapshot: %w", err)
	}

	return b, nil
}

// stripAssetBytes returns copies of the packages whose assets have no content.
func stripAssetBytes(in map[string]*packageRecord) map[string]*packageRecord {
	out := make(map[string]*packageRecord, len(in))

	for k, p := range in {
		cp := *p
		cp.Versions = make(map[string]*versionRecord, len(p.Versions))

		for name, v := range p.Versions {
			cv := *v
			cv.Assets = make(map[string]*assetRecord, len(v.Assets))

			for an, a := range v.Assets {
				ca := *a
				ca.Content, ca.Stored = nil, false
				cv.Assets[an] = &ca
			}

			cp.Versions[name] = &cv
		}

		out[k] = &cp
	}

	return out
}

// Restore rebuilds the mock's state under the original identities: every domain
// name, repository key, package, group and policy (and the ARN and computed
// fields derived from them) is preserved, so a restore is transparent to
// clients. A snapshot written before packages and groups existed restores with
// those stores empty.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap codeartifactSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("codeartifact: parse snapshot: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for name := range snap.Domains {
		m.domains.Set(name, snap.Domains[name])
	}

	for key := range snap.Repositories {
		m.repos.Set(key, snap.Repositories[key])
	}

	for key := range snap.DomainPolicies {
		m.domainPolicies.Set(key, snap.DomainPolicies[key])
	}

	for key := range snap.RepoPolicies {
		m.repoPolicies.Set(key, snap.RepoPolicies[key])
	}

	for key, p := range snap.Packages {
		if p.Versions == nil {
			p.Versions = map[string]*versionRecord{}
		}

		m.packages.Set(key, p)
	}

	for key, g := range snap.Groups {
		m.groups.Set(key, g)
	}

	return nil
}
