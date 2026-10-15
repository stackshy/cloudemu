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

	APIKeys  map[string]*driver.APIKey                     `json:"apiKeys,omitempty"`
	Plans    map[string]*driver.UsagePlan                  `json:"usagePlans,omitempty"`
	PlanKeys map[string][]string                           `json:"usagePlanKeys,omitempty"`
	Domains  map[string]*driver.DomainName                 `json:"domainNames,omitempty"`
	Mappings map[string]map[string]*driver.BasePathMapping `json:"basePathMappings,omitempty"`
	VpcLinks map[string]*driver.VpcLink                    `json:"vpcLinks,omitempty"`
	Usage    map[string]map[string]int64                   `json:"usage,omitempty"`
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
	Authorizers     map[string]*driver.Authorizer          `json:"authorizers,omitempty"`
	Models          map[string]*driver.Model               `json:"models,omitempty"`
	Validators      map[string]*driver.RequestValidator    `json:"requestValidators,omitempty"`
	GatewayResps    map[string]*driver.GatewayResponse     `json:"gatewayResponses,omitempty"`
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

	m.snapshotRegionExt(&snap)
	m.regionMu.RUnlock()

	return json.Marshal(snap)
}

func (m *Mock) snapshotRegionExt(snap *apigatewaySnapshot) {
	m.snapshotKeysAndPlans(snap)
	m.snapshotDomainsAndLinks(snap)

	m.usageMu.Lock()
	defer m.usageMu.Unlock()

	if len(m.usage) > 0 {
		snap.Usage = make(map[string]map[string]int64, len(m.usage))

		for k, days := range m.usage {
			snap.Usage[k] = copyDayCounts(days)
		}
	}
}

func (m *Mock) snapshotKeysAndPlans(snap *apigatewaySnapshot) {
	if len(m.keys) > 0 {
		snap.APIKeys = make(map[string]*driver.APIKey, len(m.keys))

		for id, k := range m.keys {
			cp := copyAPIKey(k, true)
			snap.APIKeys[id] = &cp
		}
	}

	if len(m.plans) == 0 {
		return
	}

	snap.Plans, snap.PlanKeys = make(map[string]*driver.UsagePlan, len(m.plans)), map[string][]string{}

	for id, p := range m.plans {
		cp := copyPlan(p)
		snap.Plans[id] = &cp

		for kid := range m.planKeys[id] {
			snap.PlanKeys[id] = append(snap.PlanKeys[id], kid)
		}
	}
}

func (m *Mock) snapshotDomainsAndLinks(snap *apigatewaySnapshot) {
	if len(m.domains) > 0 {
		snap.Domains, snap.Mappings = make(map[string]*driver.DomainName, len(m.domains)), map[string]map[string]*driver.BasePathMapping{}

		for name, d := range m.domains {
			cp := copyDomain(d)
			snap.Domains[name] = &cp
			snap.Mappings[name] = map[string]*driver.BasePathMapping{}

			for bp, mp := range m.mappings[name] {
				c := *mp
				snap.Mappings[name][bp] = &c
			}
		}
	}

	if len(m.vpcLinks) > 0 {
		snap.VpcLinks = make(map[string]*driver.VpcLink, len(m.vpcLinks))

		for id, l := range m.vpcLinks {
			cp := copyVpcLink(l)
			snap.VpcLinks[id] = &cp
		}
	}
}

func copyDayCounts(in map[string]int64) map[string]int64 {
	out := make(map[string]int64, len(in))
	for k, v := range in {
		out[k] = v
	}

	return out
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

	snapshotAPIExt(as, ad)

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

// snapshotAPIExt copies the per-API authorizers, models, validators and gateway
// response overrides. ad.mu is held for reading.
func snapshotAPIExt(as *apiSnapshot, ad *apiData) {
	as.Authorizers = make(map[string]*driver.Authorizer, len(ad.authorizers))

	for id, az := range ad.authorizers {
		cp := copyAuthorizer(az)
		as.Authorizers[id] = &cp
	}

	as.Models = make(map[string]*driver.Model, len(ad.models))

	for name, mod := range ad.models {
		cp := *mod
		as.Models[name] = &cp
	}

	as.Validators = make(map[string]*driver.RequestValidator, len(ad.validators))

	for id, v := range ad.validators {
		cp := *v
		as.Validators[id] = &cp
	}

	as.GatewayResps = make(map[string]*driver.GatewayResponse, len(ad.gwResponses))

	for t, gr := range ad.gwResponses {
		cp := copyGatewayResponse(gr)
		as.GatewayResps[t] = &cp
	}
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

	m.restoreRegionExt(&snap)

	return nil
}

// restoreRegionExt rebuilds the account-level resources. regionMu is held.
func (m *Mock) restoreRegionExt(snap *apigatewaySnapshot) {
	for id, k := range snap.APIKeys {
		m.keys[id] = k
	}

	for id, p := range snap.Plans {
		m.plans[id] = p
		m.planKeys[id] = map[string]bool{}

		for _, kid := range snap.PlanKeys[id] {
			m.planKeys[id][kid] = true
		}
	}

	for name, d := range snap.Domains {
		m.domains[name] = d
		m.mappings[name] = snap.Mappings[name]

		if m.mappings[name] == nil {
			m.mappings[name] = map[string]*driver.BasePathMapping{}
		}
	}

	for id, l := range snap.VpcLinks {
		m.vpcLinks[id] = l
	}

	m.usageMu.Lock()
	defer m.usageMu.Unlock()

	for k, days := range snap.Usage {
		m.usage[k] = days
	}
}

// restoreAPIExt restores the per-API extension stores. A snapshot written before
// they existed has none, so the default models stay.
func restoreAPIExt(ad *apiData, as *apiSnapshot) {
	for id, az := range as.Authorizers {
		ad.authorizers[id] = az
	}

	for name, mod := range as.Models {
		ad.models[name] = mod
	}

	for id, v := range as.Validators {
		ad.validators[id] = v
	}

	for t, gr := range as.GatewayResps {
		ad.gwResponses[t] = gr
	}
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
		apiExt:      newAPIExt(),
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

	restoreAPIExt(ad, as)

	return ad
}
