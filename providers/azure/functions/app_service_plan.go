package functions

import (
	"context"
	"fmt"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
)

// AppServicePlan is an Azure App Service plan (Microsoft.Web/serverfarms), the
// resource that carries the pricing tier an App Service or Function App bills
// on. Only the cost-relevant SKU is modeled.
type AppServicePlan struct {
	Name string
	// Subscription and ResourceGroup scope the plan's storage key
	// (planKey): unlike a Web App name, an App Service plan name is only
	// required to be unique within a resource group, so two different
	// resource groups (even in the same subscription) can each have a plan
	// named e.g. "default".
	Subscription  string
	ResourceGroup string
	ID            string
	Location      string
	SKUName       string // F1 / B1 / S1 / P1v3 / Y1 (Consumption) / EP1 (Elastic Premium)
	SKUTier       string // Free / Basic / Standard / PremiumV3 / Dynamic / ElasticPremium
	Kind          string // app / functionapp / linux
	Capacity      int
	Tags          map[string]string

	// Plan properties a caller can change after create (armappservice
	// PlanProperties / PlanPatchResourceProperties).
	Reserved                  bool // true for a Linux plan
	PerSiteScaling            bool
	ZoneRedundant             bool
	MaximumElasticWorkerCount int // Elastic Premium burst ceiling
}

// App Service plan pricing tiers, the tier real Azure derives from a SKU name
// when the caller omits it (azurerm sends only the SKU name + capacity).
const (
	tierFree             = "Free"
	tierShared           = "Shared"
	tierBasic            = "Basic"
	tierStandard         = "Standard"
	tierPremium          = "Premium"
	tierPremiumV2        = "PremiumV2"
	tierPremiumV3        = "PremiumV3"
	tierDynamic          = "Dynamic"
	tierElasticPremium   = "ElasticPremium"
	tierIsolated         = "Isolated"
	tierIsolatedV2       = "IsolatedV2"
	tierWorkflowStandard = "WorkflowStandard"
)

// deriveSKUTier maps an App Service plan SKU name to its pricing tier the way
// real Azure does server-side when a create omits the tier: Y->Dynamic (Consumption),
// EP->ElasticPremium, WS->WorkflowStandard (Logic Apps), B->Basic, S->Standard,
// P#v2->PremiumV2, P#v3->PremiumV3, P(other)->Premium, F->Free, D->Shared,
// I(#v2)->IsolatedV2 else Isolated. An unrecognized name falls back to Standard
// rather than forcing Dynamic (which would mis-bill a dedicated plan).
func deriveSKUTier(skuName string) string {
	name := strings.ToUpper(strings.TrimSpace(skuName))
	if name == "" {
		return tierDynamic
	}

	// Longest prefixes first so EP/WS win over the single-letter matches.
	prefixes := []struct{ prefix, tier string }{
		{"EP", tierElasticPremium},
		{"WS", tierWorkflowStandard},
		{"Y", tierDynamic},
		{"F", tierFree},
		{"D", tierShared},
		{"B", tierBasic},
		{"S", tierStandard},
	}
	for _, p := range prefixes {
		if strings.HasPrefix(name, p.prefix) {
			return p.tier
		}
	}

	if strings.HasPrefix(name, "P") {
		return premiumTier(name)
	}

	if strings.HasPrefix(name, "I") {
		if strings.HasSuffix(name, "V2") {
			return tierIsolatedV2
		}

		return tierIsolated
	}

	return tierStandard
}

// premiumTier resolves the three Premium generations (legacy P#, P#v2, P#v3).
func premiumTier(name string) string {
	switch {
	case strings.HasSuffix(name, "V2"):
		return tierPremiumV2
	case strings.HasSuffix(name, "V3"):
		return tierPremiumV3
	default:
		return tierPremium
	}
}

// planKey builds the composite key AppServicePlans are stored under, so a
// plan named "default" in one resource group can never collide with (or be
// overwritten by) a same-named plan in another resource group.
func planKey(subscription, resourceGroup, name string) string {
	return subscription + "/" + resourceGroup + "/" + name
}

// CreateAppServicePlan stores a plan, defaulting the fields real Azure fills in.
//
//nolint:gocritic // p is a value seed matching the CreateScaleSet convention.
func (m *Mock) CreateAppServicePlan(_ context.Context, p AppServicePlan) (*AppServicePlan, error) {
	if p.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "app service plan name is required")
	}

	if p.ID == "" {
		p.ID = fmt.Sprintf(
			"/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Web/serverfarms/%s", p.Name)
	}

	if p.Location == "" {
		p.Location = m.opts.Region
	}

	if p.SKUName == "" {
		p.SKUName = "Y1"
	}

	if p.SKUTier == "" {
		p.SKUTier = deriveSKUTier(p.SKUName)
	}

	if p.Capacity == 0 {
		p.Capacity = 1
	}

	stored := p

	m.plans.Set(planKey(p.Subscription, p.ResourceGroup, p.Name), &stored)

	out := stored

	return &out, nil
}

// GetAppServicePlan returns one App Service plan scoped to the given
// subscription and resource group, or NotFound.
func (m *Mock) GetAppServicePlan(_ context.Context, subscription, resourceGroup, name string) (*AppServicePlan, error) {
	p, ok := m.plans.Get(planKey(subscription, resourceGroup, name))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "app service plan %s not found", name)
	}

	out := *p

	return &out, nil
}

// DeleteAppServicePlan removes one App Service plan scoped to the given
// subscription and resource group, or NotFound. A plan that still has a Web App
// assigned to it (any site whose ServerFarmID targets the plan's ARM id) cannot
// be deleted: real Azure answers 409 Conflict ("Server farm ... cannot be
// deleted because it has web app(s) assigned to it"), so the delete is rejected
// with FailedPrecondition (mapped to 409 by the wire layer) rather than
// silently leaving every site pointing at a plan that no longer exists.
func (m *Mock) DeleteAppServicePlan(_ context.Context, subscription, resourceGroup, name string) error {
	if _, ok := m.plans.Get(planKey(subscription, resourceGroup, name)); !ok {
		return cerrors.Newf(cerrors.NotFound, "app service plan %s not found", name)
	}

	if site := m.planAssignedSite(subscription, resourceGroup, name); site != "" {
		return cerrors.Newf(cerrors.FailedPrecondition,
			"Server farm %q cannot be deleted because it has web app(s) assigned to it (site %q)", name, site)
	}

	m.plans.Delete(planKey(subscription, resourceGroup, name))

	return nil
}

// planAssignedSite returns the name of a site still assigned to the named plan
// (its ServerFarmID equal to the plan's ARM id), or "" when none reference it.
// A site's plan may live in a different resource group than the site, so every
// site in the subscription is a candidate, the join mirrors listPlanWebApps.
func (m *Mock) planAssignedSite(subscription, resourceGroup, name string) string {
	planID := idgen.AzureID(subscription, resourceGroup, "Microsoft.Web", "serverfarms", name)

	m.sitesMu.RLock()
	defer m.sitesMu.RUnlock()

	for _, meta := range m.sites.SortedValues() {
		if strings.EqualFold(meta.ServerFarmID, planID) {
			return meta.Name
		}
	}

	return ""
}

// ListAppServicePlans returns the App Service plans in the given resource
// group, or all plans in the subscription when resourceGroup is empty, or
// every stored plan when subscription is also empty (the resource-discovery
// / cost walkers, which have no ARM request scope to filter by).
func (m *Mock) ListAppServicePlans(_ context.Context, subscription, resourceGroup string) ([]AppServicePlan, error) {
	stored := m.plans.SortedValues()

	out := make([]AppServicePlan, 0, len(stored))

	for _, p := range stored {
		if subscription != "" && p.Subscription != subscription {
			continue
		}

		if resourceGroup != "" && !strings.EqualFold(p.ResourceGroup, resourceGroup) {
			continue
		}

		out = append(out, *p)
	}

	return out, nil
}

// AppServicePlanPatch is a partial App Service plan update (ARM PATCH
// .../serverfarms/{name}). A nil field leaves the stored value untouched; a
// non-nil Tags map replaces the plan's tags wholesale.
type AppServicePlanPatch struct {
	Kind                      *string
	SKUName                   *string
	SKUTier                   *string
	Capacity                  *int
	Tags                      map[string]string
	Reserved                  *bool
	PerSiteScaling            *bool
	ZoneRedundant             *bool
	MaximumElasticWorkerCount *int
}

// PatchAppServicePlan applies a partial update to one App Service plan scoped
// to the given subscription and resource group, returning the stored result or
// NotFound. The read-modify-write runs under the store lock. A SKU name change
// without an explicit tier re-derives the tier, as a create does.
func (m *Mock) PatchAppServicePlan(
	_ context.Context, subscription, resourceGroup, name string, patch AppServicePlanPatch,
) (*AppServicePlan, error) {
	var out AppServicePlan

	found := m.plans.Update(planKey(subscription, resourceGroup, name), func(p *AppServicePlan) *AppServicePlan {
		next := *p
		applyPlanPatch(&next, &patch)
		out = next

		return &next
	})
	if !found {
		return nil, cerrors.Newf(cerrors.NotFound, "app service plan %s not found", name)
	}

	return &out, nil
}

func applyPlanPatch(p *AppServicePlan, patch *AppServicePlanPatch) {
	setIf(&p.Kind, patch.Kind)
	setIf(&p.Capacity, patch.Capacity)
	setIf(&p.Reserved, patch.Reserved)
	setIf(&p.PerSiteScaling, patch.PerSiteScaling)
	setIf(&p.ZoneRedundant, patch.ZoneRedundant)
	setIf(&p.MaximumElasticWorkerCount, patch.MaximumElasticWorkerCount)

	if patch.SKUName != nil && *patch.SKUName != "" && *patch.SKUName != p.SKUName {
		p.SKUName = *patch.SKUName
		p.SKUTier = deriveSKUTier(p.SKUName)
	}

	if patch.SKUTier != nil && *patch.SKUTier != "" {
		p.SKUTier = *patch.SKUTier
	}

	if patch.Tags != nil {
		tags := make(map[string]string, len(patch.Tags))
		for k, v := range patch.Tags {
			tags[k] = v
		}

		p.Tags = tags
	}
}

// setIf assigns *src to *dst when src is non-nil.
func setIf[T any](dst, src *T) {
	if src != nil {
		*dst = *src
	}
}
