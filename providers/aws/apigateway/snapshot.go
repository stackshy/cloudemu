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
	APIs    map[string]*apiSnapshot              `json:"apis,omitempty"`
	Certs   map[string]*driver.ClientCertificate `json:"clientCertificates,omitempty"`
	Account *driver.Account                      `json:"account,omitempty"`
}

// apiSnapshot is the exported form of apiData: the REST API plus its resource
// tree, deployments (with the tree each one captured) and stages, all under
// their original identities.
type apiSnapshot struct {
	API             driver.RestAPI                         `json:"api"`
	Resources       map[string]*driver.Resource            `json:"resources,omitempty"`
	Deployments     map[string]*driver.Deployment          `json:"deployments,omitempty"`
	DeploymentTrees map[string]map[string]*driver.Resource `json:"deploymentTrees,omitempty"`
	Stages          map[string]*driver.Stage               `json:"stages,omitempty"`
	DocParts        map[string]*driver.DocumentationPart   `json:"documentationParts,omitempty"`
	DocVersions     map[string]*docVersion                 `json:"documentationVersions,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused. API Gateway holds
// only control-plane definitions, no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	snap := apigatewaySnapshot{}

	if m.apis.Len() > 0 {
		snap.APIs = make(map[string]*apiSnapshot, m.apis.Len())

		for id, ad := range m.apis.All() {
			snap.APIs[id] = snapshotAPI(ad)
		}
	}

	m.regionMu.RLock()

	acct := copyAccount(&m.account)
	snap.Account = &acct

	if len(m.certs) > 0 {
		snap.Certs = make(map[string]*driver.ClientCertificate, len(m.certs))

		for id, cc := range m.certs {
			cp := copyCert(cc)
			snap.Certs[id] = &cp
		}
	}

	m.regionMu.RUnlock()

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

		DeploymentTrees: make(map[string]map[string]*driver.Resource, len(ad.trees)),
	}

	for did, tree := range ad.trees {
		as.DeploymentTrees[did] = copyTree(tree)
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

	as.DocParts = make(map[string]*driver.DocumentationPart, len(ad.docParts))

	for id, p := range ad.docParts {
		cp := *p
		as.DocParts[id] = &cp
	}

	as.DocVersions = make(map[string]*docVersion, len(ad.docVersions))

	for v, dv := range ad.docVersions {
		cp := docVersion{Version: dv.Version, Parts: make(map[string]driver.DocumentationPart, len(dv.Parts))}
		for id, p := range dv.Parts {
			cp.Parts[id] = p
		}

		as.DocVersions[v] = &cp
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

	m.regionMu.Lock()
	defer m.regionMu.Unlock()

	for id, cc := range snap.Certs {
		m.certs[id] = cc
	}

	if snap.Account != nil {
		m.account = *snap.Account
	}

	return nil
}

// restoreAPI rebuilds an apiData from its exported snapshot form. A snapshot
// written before deployments captured their own tree has none, so each such
// deployment falls back to a copy of the restored live tree.
func restoreAPI(as *apiSnapshot) *apiData {
	ad := &apiData{
		api:         as.API,
		resources:   make(map[string]*driver.Resource, len(as.Resources)),
		deployments: make(map[string]*driver.Deployment, len(as.Deployments)),
		trees:       make(map[string]map[string]*driver.Resource, len(as.Deployments)),
		stages:      make(map[string]*driver.Stage, len(as.Stages)),
		docParts:    make(map[string]*driver.DocumentationPart, len(as.DocParts)),
		docVersions: make(map[string]*docVersion, len(as.DocVersions)),
	}

	for id, p := range as.DocParts {
		ad.docParts[id] = p
	}

	for v, dv := range as.DocVersions {
		ad.docVersions[v] = dv
	}

	for rid, r := range as.Resources {
		ad.resources[rid] = r
	}

	for did, d := range as.Deployments {
		ad.deployments[did] = d

		if tree, ok := as.DeploymentTrees[did]; ok {
			ad.trees[did] = tree
		} else {
			ad.trees[did] = copyTree(ad.resources)
		}
	}

	for name, s := range as.Stages {
		ad.stages[name] = s
	}

	return ad
}
