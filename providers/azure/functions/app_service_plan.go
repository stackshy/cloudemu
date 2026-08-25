package functions

import (
	"context"
	"fmt"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// AppServicePlan is an Azure App Service plan (Microsoft.Web/serverfarms) — the
// resource that carries the pricing tier an App Service or Function App bills
// on. Only the cost-relevant SKU is modeled.
type AppServicePlan struct {
	Name string
	// Subscription and ResourceGroup scope the plan's storage key
	// (planKey) — unlike a Web App name, an App Service plan name is only
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
		p.SKUTier = "Dynamic"
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
// subscription and resource group, or NotFound.
func (m *Mock) DeleteAppServicePlan(_ context.Context, subscription, resourceGroup, name string) error {
	if !m.plans.Delete(planKey(subscription, resourceGroup, name)) {
		return cerrors.Newf(cerrors.NotFound, "app service plan %s not found", name)
	}

	return nil
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

		if resourceGroup != "" && p.ResourceGroup != resourceGroup {
			continue
		}

		out = append(out, *p)
	}

	return out, nil
}
