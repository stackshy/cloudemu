package location

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/location/driver"
)

func trackerMeta(t *driver.TrackerInfo) *driver.Meta { return &t.Meta }

func cloneTracker(t *driver.TrackerInfo) driver.TrackerInfo {
	out := *t
	out.Meta = cloneMeta(&t.Meta)

	return out
}

// CreateTracker provisions a tracker. An absent PositionFiltering defaults to
// TimeBased, matching the real service.
func (m *Mock) CreateTracker(_ context.Context, in *driver.CreateTrackerInput) (*driver.TrackerInfo, error) {
	if in.TrackerName == "" {
		return nil, validation("TrackerName is required")
	}

	if m.trackers.Has(in.TrackerName) {
		return nil, conflict("Tracker", in.TrackerName)
	}

	filtering := in.PositionFiltering
	if filtering == "" {
		filtering = driver.DefaultPositionFilter
	}

	info := driver.TrackerInfo{
		Meta:                          m.newMeta(kindTracker, in.TrackerName, in.Description, in.Tags),
		KmsKeyID:                      in.KmsKeyID,
		PositionFiltering:             filtering,
		EventBridgeEnabled:            in.EventBridgeEnabled,
		KmsKeyEnableGeospatialQueries: in.KmsKeyEnableGeospatialQueries,
		PricingPlan:                   in.PricingPlan,
		PricingPlanDataSource:         in.PricingPlanDataSource,
	}

	m.trackers.Set(in.TrackerName, info)

	out := cloneTracker(&info)

	return &out, nil
}

// DescribeTracker returns a clone of the stored tracker.
func (m *Mock) DescribeTracker(_ context.Context, name string) (*driver.TrackerInfo, error) {
	return describeEntity(m.trackers, "Tracker", name, cloneTracker)
}

// UpdateTracker applies description/filtering/pricing changes and bumps
// UpdateTime.
func (m *Mock) UpdateTracker(_ context.Context, in *driver.UpdateTrackerInput) (*driver.TrackerInfo, error) {
	return applyUpdate(m, m.trackers, "Tracker", in.TrackerName, trackerMeta, func(info *driver.TrackerInfo) {
		if in.Description != nil {
			info.Description = *in.Description
		}

		if in.PositionFiltering != nil && *in.PositionFiltering != "" {
			info.PositionFiltering = *in.PositionFiltering
		}

		if in.EventBridgeEnabled != nil {
			info.EventBridgeEnabled = *in.EventBridgeEnabled
		}

		if in.PricingPlan != nil {
			info.PricingPlan = *in.PricingPlan
		}

		if in.PricingPlanDataSource != nil {
			info.PricingPlanDataSource = *in.PricingPlanDataSource
		}
	}, cloneTracker)
}

// DeleteTracker removes a tracker.
func (m *Mock) DeleteTracker(_ context.Context, name string) error {
	return deleteEntity(m.trackers, "Tracker", name)
}

// ListTrackers returns a deterministic page ordered by name.
func (m *Mock) ListTrackers(_ context.Context, page driver.Page) ([]driver.TrackerInfo, string, error) {
	items, next := listEntities(m.trackers, page, cloneTracker)

	return items, next, nil
}
