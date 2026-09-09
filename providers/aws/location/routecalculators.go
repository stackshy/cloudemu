package location

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/location/driver"
)

func routeCalculatorMeta(c *driver.RouteCalculatorInfo) *driver.Meta { return &c.Meta }

func cloneRouteCalculator(c *driver.RouteCalculatorInfo) driver.RouteCalculatorInfo {
	out := *c
	out.Meta = cloneMeta(&c.Meta)

	return out
}

// CreateRouteCalculator provisions a route calculator. DataSource is required.
func (m *Mock) CreateRouteCalculator(
	_ context.Context, in *driver.CreateRouteCalculatorInput,
) (*driver.RouteCalculatorInfo, error) {
	if in.CalculatorName == "" {
		return nil, validation("CalculatorName is required")
	}

	if in.DataSource == "" {
		return nil, validation("DataSource is required")
	}

	if m.calculators.Has(in.CalculatorName) {
		return nil, conflict("RouteCalculator", in.CalculatorName)
	}

	info := driver.RouteCalculatorInfo{
		Meta:        m.newMeta(kindRouteCalculator, in.CalculatorName, in.Description, in.Tags),
		DataSource:  in.DataSource,
		PricingPlan: in.PricingPlan,
	}

	m.calculators.Set(in.CalculatorName, info)

	out := cloneRouteCalculator(&info)

	return &out, nil
}

// DescribeRouteCalculator returns a clone of the stored route calculator.
func (m *Mock) DescribeRouteCalculator(_ context.Context, name string) (*driver.RouteCalculatorInfo, error) {
	return describeEntity(m.calculators, "RouteCalculator", name, cloneRouteCalculator)
}

// UpdateRouteCalculator applies description/pricing-plan changes and bumps
// UpdateTime.
func (m *Mock) UpdateRouteCalculator(
	_ context.Context, in *driver.UpdateRouteCalculatorInput,
) (*driver.RouteCalculatorInfo, error) {
	return applyUpdate(m, m.calculators, "RouteCalculator", in.CalculatorName, routeCalculatorMeta,
		func(info *driver.RouteCalculatorInfo) {
			if in.Description != nil {
				info.Description = *in.Description
			}

			if in.PricingPlan != nil {
				info.PricingPlan = *in.PricingPlan
			}
		}, cloneRouteCalculator)
}

// DeleteRouteCalculator removes a route calculator.
func (m *Mock) DeleteRouteCalculator(_ context.Context, name string) error {
	return deleteEntity(m.calculators, "RouteCalculator", name)
}

// ListRouteCalculators returns a deterministic page ordered by name.
func (m *Mock) ListRouteCalculators(_ context.Context, page driver.Page) ([]driver.RouteCalculatorInfo, string, error) {
	items, next := listEntities(m.calculators, page, cloneRouteCalculator)

	return items, next, nil
}
