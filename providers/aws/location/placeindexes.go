package location

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/location/driver"
)

func placeIndexMeta(p *driver.PlaceIndexInfo) *driver.Meta { return &p.Meta }

func clonePlaceIndex(p *driver.PlaceIndexInfo) driver.PlaceIndexInfo {
	out := *p
	out.Meta = cloneMeta(&p.Meta)

	return out
}

// CreatePlaceIndex provisions a place index. DataSource is required; an absent
// IntendedUse defaults to SingleUse, matching the real service.
func (m *Mock) CreatePlaceIndex(_ context.Context, in *driver.CreatePlaceIndexInput) (*driver.PlaceIndexInfo, error) {
	if in.IndexName == "" {
		return nil, validation("IndexName is required")
	}

	if in.DataSource == "" {
		return nil, validation("DataSource is required")
	}

	if m.indexes.Has(in.IndexName) {
		return nil, conflict("PlaceIndex", in.IndexName)
	}

	cfg := in.DataSourceConfiguration
	if cfg.IntendedUse == "" {
		cfg.IntendedUse = driver.DefaultIntendedUse
	}

	info := driver.PlaceIndexInfo{
		Meta:                    m.newMeta(kindPlaceIndex, in.IndexName, in.Description, in.Tags),
		DataSource:              in.DataSource,
		DataSourceConfiguration: cfg,
		PricingPlan:             in.PricingPlan,
	}

	m.indexes.Set(in.IndexName, info)

	out := clonePlaceIndex(&info)

	return &out, nil
}

// DescribePlaceIndex returns a clone of the stored place index.
func (m *Mock) DescribePlaceIndex(_ context.Context, name string) (*driver.PlaceIndexInfo, error) {
	return describeEntity(m.indexes, "PlaceIndex", name, clonePlaceIndex)
}

// UpdatePlaceIndex applies description/config/pricing-plan changes and bumps
// UpdateTime.
func (m *Mock) UpdatePlaceIndex(_ context.Context, in *driver.UpdatePlaceIndexInput) (*driver.PlaceIndexInfo, error) {
	return applyUpdate(m, m.indexes, "PlaceIndex", in.IndexName, placeIndexMeta, func(info *driver.PlaceIndexInfo) {
		if in.Description != nil {
			info.Description = *in.Description
		}

		if in.DataSourceConfiguration != nil {
			cfg := *in.DataSourceConfiguration
			if cfg.IntendedUse == "" {
				cfg.IntendedUse = driver.DefaultIntendedUse
			}

			info.DataSourceConfiguration = cfg
		}

		if in.PricingPlan != nil {
			info.PricingPlan = *in.PricingPlan
		}
	}, clonePlaceIndex)
}

// DeletePlaceIndex removes a place index.
func (m *Mock) DeletePlaceIndex(_ context.Context, name string) error {
	return deleteEntity(m.indexes, "PlaceIndex", name)
}

// ListPlaceIndexes returns a deterministic page of place indexes ordered by name.
func (m *Mock) ListPlaceIndexes(_ context.Context, page driver.Page) ([]driver.PlaceIndexInfo, string, error) {
	items, next := listEntities(m.indexes, page, clonePlaceIndex)

	return items, next, nil
}
