package cost

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/pricing"
	"github.com/stackshy/cloudemu/v2/services/resourcediscovery"
)

// Inventory is the read-only resource source the estimator prices. It is
// satisfied by *resourcediscovery.Engine, so any provider's discovery engine
// can be priced without this package knowing about a specific provider. The
// FinOps wire handlers (AWS Cost Explorer, Azure Cost Management) share it so
// the pricing + aggregation logic lives here once, provider-agnostic, and each
// handler only shapes the result into its provider's wire format.
type Inventory interface {
	ListAll(ctx context.Context) ([]resourcediscovery.Resource, error)
}

// Line is one always-on resource with its estimated monthly USD cost. The
// discovery attributes (Provider/Service/Type/Region) are carried through so a
// wire handler can group the lines by whichever cloud-native dimension its API
// exposes (service name, resource type, region, …) without re-pricing.
type Line struct {
	Provider   string
	Service    string
	Type       string
	ID         string
	ARN        string
	Region     string
	MonthlyUSD float64
}

// Estimate prices every resource the inventory holds and returns one Line per
// resource that carries a positive monthly cost, plus the summed total. Usage-
// based and free resources price at zero and are dropped, matching every other
// cost surface in the emulator (the /_cloudemu/cost endpoint applies the same
// always-on filter). A nil inventory yields an empty estimate.
func Estimate(ctx context.Context, inv Inventory) ([]Line, float64, error) {
	if inv == nil {
		return nil, 0, nil
	}

	res, err := inv.ListAll(ctx)
	if err != nil {
		return nil, 0, err
	}

	var (
		lines []Line
		total float64
	)

	for i := range res {
		r := &res[i]

		est := pricing.Monthly(r.Provider, r.Service, r.Type, r.SKU, r.Region, r.Properties)
		if est <= 0 {
			continue
		}

		lines = append(lines, Line{
			Provider:   r.Provider,
			Service:    r.Service,
			Type:       r.Type,
			ID:         r.ID,
			ARN:        r.ARN,
			Region:     r.Region,
			MonthlyUSD: est,
		})
		total += est
	}

	return lines, total, nil
}

// ServiceMonthly prices the inventory and returns the total monthly USD grouped
// by each resource's portable service. It is the provider-agnostic aggregation
// the grouped FinOps responses build on, so callers never re-implement the
// price-then-bucket walk.
func ServiceMonthly(ctx context.Context, inv Inventory) (map[string]float64, error) {
	lines, _, err := Estimate(ctx, inv)
	if err != nil {
		return nil, err
	}

	out := make(map[string]float64, len(lines))
	for i := range lines {
		out[lines[i].Service] += lines[i].MonthlyUSD
	}

	return out, nil
}
