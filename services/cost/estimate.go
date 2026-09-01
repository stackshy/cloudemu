package cost

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/pricing"
	"github.com/stackshy/cloudemu/v2/services/resourcediscovery"
)

// Inventory is the read surface the cost estimator needs: the cross-service
// resource walk that pricing is applied to. *resourcediscovery.Engine
// satisfies it, so callers pass the engine directly.
type Inventory interface {
	ListAll(ctx context.Context) ([]resourcediscovery.Resource, error)
}

// ServiceMonthly returns the estimated monthly USD of the current inventory,
// aggregated by resourcediscovery service token (e.g. "compute", "storage").
// Resources that price at zero (free control-plane objects, usage-metered
// services) are excluded, so a service appears only when it carries a cost.
//
// It applies the shared services/pricing model — the single source of truth for
// per-resource cost — over the live inventory, so it never re-implements pricing
// logic. It is the provider-agnostic cost surface the AWS Cost Explorer wire
// handler serves.
func ServiceMonthly(ctx context.Context, inv Inventory) (map[string]float64, error) {
	res, err := inv.ListAll(ctx)
	if err != nil {
		return nil, err
	}

	byService := make(map[string]float64, len(res))

	for i := range res {
		r := &res[i]

		est := pricing.Monthly(r.Provider, r.Service, r.Type, r.SKU, r.Region, r.Properties)
		if est <= 0 {
			continue
		}

		byService[r.Service] += est
	}

	return byService, nil
}
