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
	Name     string
	ID       string
	Location string
	SKUName  string // F1 / B1 / S1 / P1v3 / Y1 (Consumption) / EP1 (Elastic Premium)
	SKUTier  string // Free / Basic / Standard / PremiumV3 / Dynamic / ElasticPremium
	Kind     string // app / functionapp / linux
	Capacity int
	Tags     map[string]string
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

	m.plans.Set(p.Name, &stored)

	out := stored

	return &out, nil
}

// ListAppServicePlans returns every stored App Service plan.
func (m *Mock) ListAppServicePlans(_ context.Context) ([]AppServicePlan, error) {
	stored := m.plans.SortedValues()

	out := make([]AppServicePlan, 0, len(stored))
	for _, p := range stored {
		out = append(out, *p)
	}

	return out, nil
}
