package ec2

import (
	"context"
	"fmt"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/compute/driver"
)

// Placement group strategy constants.
const (
	placementStrategyCluster   = "cluster"
	placementStrategySpread    = "spread"
	placementStrategyPartition = "partition"
	defaultPartitionCount      = 2
)

// CreatePlacementGroup creates an EC2 placement group (aws_placement_group). The
// name must be unique; a duplicate is AlreadyExists (InvalidPlacementGroup.Duplicate).
func (m *Mock) CreatePlacementGroup(
	_ context.Context, cfg driver.PlacementGroupConfig,
) (*driver.PlacementGroup, error) {
	if cfg.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "placement group name must not be empty")
	}

	if err := validatePlacementStrategy(cfg.Strategy); err != nil {
		return nil, err
	}

	if m.placementGroups.Has(cfg.Name) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "placement group %q already exists", cfg.Name)
	}

	partitionCount := cfg.PartitionCount
	if cfg.Strategy == placementStrategyPartition && partitionCount == 0 {
		partitionCount = defaultPartitionCount
	}

	pg := &driver.PlacementGroup{
		ID:             fmt.Sprintf("pg-%016x", m.pgCounter.Add(1)),
		Name:           cfg.Name,
		Strategy:       cfg.Strategy,
		State:          "available",
		PartitionCount: partitionCount,
		SpreadLevel:    cfg.SpreadLevel,
		Tags:           copyTags(cfg.Tags),
	}
	m.placementGroups.Set(cfg.Name, pg)

	result := *pg

	return &result, nil
}

// DeletePlacementGroup deletes a placement group by name.
func (m *Mock) DeletePlacementGroup(_ context.Context, name string) error {
	if !m.placementGroups.Delete(name) {
		return cerrors.Newf(cerrors.NotFound, "placement group %q not found", name)
	}

	return nil
}

// DescribePlacementGroups returns placement groups matching the given names or
// group ids, or all groups when both are empty. An explicitly named group that
// does not exist is NotFound, matching real EC2.
func (m *Mock) DescribePlacementGroups(
	_ context.Context, names, ids []string,
) ([]driver.PlacementGroup, error) {
	for _, name := range names {
		if !m.placementGroups.Has(name) {
			return nil, cerrors.Newf(cerrors.NotFound, "placement group %q not found", name)
		}
	}

	idSet := map[string]bool{}
	for _, id := range ids {
		idSet[id] = true
	}

	nameSet := map[string]bool{}
	for _, name := range names {
		nameSet[name] = true
	}

	out := make([]driver.PlacementGroup, 0)

	for _, pg := range m.placementGroups.All() {
		if len(names) > 0 && !nameSet[pg.Name] {
			continue
		}

		if len(ids) > 0 && !idSet[pg.ID] {
			continue
		}

		copyPG := *pg
		copyPG.Tags = copyTags(pg.Tags)
		out = append(out, copyPG)
	}

	return out, nil
}

func validatePlacementStrategy(strategy string) error {
	switch strategy {
	case placementStrategyCluster, placementStrategySpread, placementStrategyPartition:
		return nil
	default:
		return cerrors.Newf(cerrors.InvalidArgument, "invalid placement group strategy %q", strategy)
	}
}
