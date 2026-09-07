package location

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/location/driver"
)

func geofenceCollectionMeta(c *driver.GeofenceCollectionInfo) *driver.Meta { return &c.Meta }

func cloneGeofenceCollection(c *driver.GeofenceCollectionInfo) driver.GeofenceCollectionInfo {
	out := *c
	out.Meta = cloneMeta(&c.Meta)

	return out
}

// CreateGeofenceCollection provisions a geofence collection. GeofenceCount is
// always zero: storing and evaluating geofences is out of scope.
func (m *Mock) CreateGeofenceCollection(
	_ context.Context, in *driver.CreateGeofenceCollectionInput,
) (*driver.GeofenceCollectionInfo, error) {
	if in.CollectionName == "" {
		return nil, validation("CollectionName is required")
	}

	if m.collections.Has(in.CollectionName) {
		return nil, conflict("GeofenceCollection", in.CollectionName)
	}

	info := driver.GeofenceCollectionInfo{
		Meta:                  m.newMeta(kindGeofenceCollection, in.CollectionName, in.Description, in.Tags),
		KmsKeyID:              in.KmsKeyID,
		PricingPlan:           in.PricingPlan,
		PricingPlanDataSource: in.PricingPlanDataSource,
	}

	m.collections.Set(in.CollectionName, info)

	out := cloneGeofenceCollection(&info)

	return &out, nil
}

// DescribeGeofenceCollection returns a clone of the stored collection.
func (m *Mock) DescribeGeofenceCollection(_ context.Context, name string) (*driver.GeofenceCollectionInfo, error) {
	return describeEntity(m.collections, "GeofenceCollection", name, cloneGeofenceCollection)
}

// UpdateGeofenceCollection applies description/pricing changes and bumps
// UpdateTime.
func (m *Mock) UpdateGeofenceCollection(
	_ context.Context, in *driver.UpdateGeofenceCollectionInput,
) (*driver.GeofenceCollectionInfo, error) {
	return applyUpdate(m, m.collections, "GeofenceCollection", in.CollectionName, geofenceCollectionMeta,
		func(info *driver.GeofenceCollectionInfo) {
			if in.Description != nil {
				info.Description = *in.Description
			}

			if in.PricingPlan != nil {
				info.PricingPlan = *in.PricingPlan
			}

			if in.PricingPlanDataSource != nil {
				info.PricingPlanDataSource = *in.PricingPlanDataSource
			}
		}, cloneGeofenceCollection)
}

// DeleteGeofenceCollection removes a collection.
func (m *Mock) DeleteGeofenceCollection(_ context.Context, name string) error {
	return deleteEntity(m.collections, "GeofenceCollection", name)
}

// ListGeofenceCollections returns a deterministic page ordered by name.
func (m *Mock) ListGeofenceCollections(
	_ context.Context, page driver.Page,
) ([]driver.GeofenceCollectionInfo, string, error) {
	items, next := listEntities(m.collections, page, cloneGeofenceCollection)

	return items, next, nil
}
