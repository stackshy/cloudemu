package location

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/location/driver"
)

// mapMeta yields the embedded Meta pointer of a MapInfo.
func mapMeta(m *driver.MapInfo) *driver.Meta { return &m.Meta }

// cloneMap returns an alias-free copy of a map.
func cloneMap(m *driver.MapInfo) driver.MapInfo {
	out := *m
	out.Meta = cloneMeta(&m.Meta)
	out.Configuration.CustomLayers = append([]string(nil), m.Configuration.CustomLayers...)

	return out
}

// deriveMapDataSource maps a configured style to the data provider the real
// service reports for it. An unrecognized style yields Esri, the default
// provider.
func deriveMapDataSource(style string) string {
	switch {
	case strings.Contains(style, driver.DataSourceHere):
		return driver.DataSourceHere
	case strings.Contains(style, driver.DataSourceGrab):
		return driver.DataSourceGrabMaps
	default:
		return driver.DataSourceEsri
	}
}

// CreateMap provisions a map with a stable ARN and matching create/update
// timestamps. A duplicate name yields ConflictException.
func (m *Mock) CreateMap(_ context.Context, in *driver.CreateMapInput) (*driver.MapInfo, error) {
	if in.MapName == "" {
		return nil, validation("MapName is required")
	}

	if in.Configuration.Style == "" {
		return nil, validation("Configuration.Style is required")
	}

	if m.maps.Has(in.MapName) {
		return nil, conflict("Map", in.MapName)
	}

	info := driver.MapInfo{
		Meta:          m.newMeta(kindMap, in.MapName, in.Description, in.Tags),
		Configuration: in.Configuration,
		DataSource:    deriveMapDataSource(in.Configuration.Style),
		PricingPlan:   in.PricingPlan,
	}

	m.maps.Set(in.MapName, info)

	out := cloneMap(&info)

	return &out, nil
}

// DescribeMap returns a clone of the stored map.
func (m *Mock) DescribeMap(_ context.Context, name string) (*driver.MapInfo, error) {
	return describeEntity(m.maps, "Map", name, cloneMap)
}

// UpdateMap applies description/pricing-plan changes and bumps UpdateTime. The
// map style is immutable.
func (m *Mock) UpdateMap(_ context.Context, in *driver.UpdateMapInput) (*driver.MapInfo, error) {
	return applyUpdate(m, m.maps, "Map", in.MapName, mapMeta, func(info *driver.MapInfo) {
		if in.Description != nil {
			info.Description = *in.Description
		}

		if in.PricingPlan != nil {
			info.PricingPlan = *in.PricingPlan
		}
	}, cloneMap)
}

// DeleteMap removes a map.
func (m *Mock) DeleteMap(_ context.Context, name string) error {
	return deleteEntity(m.maps, "Map", name)
}

// ListMaps returns a deterministic page of maps ordered by name.
func (m *Mock) ListMaps(_ context.Context, page driver.Page) ([]driver.MapInfo, string, error) {
	items, next := listEntities(m.maps, page, cloneMap)

	return items, next, nil
}
