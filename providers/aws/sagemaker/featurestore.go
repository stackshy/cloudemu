package sagemaker

import (
	"context"

	"github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/sagemaker/driver"
)

// recordKey joins a feature group name and a record identifier.
func recordKey(group, recordID string) string {
	return group + "\x00" + recordID
}

//nolint:gocritic // cfg matches the driver signature; copied on entry.
func (m *Mock) CreateFeatureGroup(_ context.Context, cfg driver.FeatureGroupSpec) (*driver.FeatureGroup, error) {
	if cfg.GroupName == "" {
		return nil, errors.New(errors.InvalidArgument, "featureGroupName is required")
	}

	if cfg.RecordIdentifierName == "" {
		return nil, errors.New(errors.InvalidArgument, "recordIdentifierFeatureName is required")
	}

	if m.featureGroups.Has(cfg.GroupName) {
		return nil, errors.Newf(errors.AlreadyExists, "feature group %q already exists", cfg.GroupName)
	}

	arn := m.arn("feature-group/" + cfg.GroupName)
	fg := &driver.FeatureGroup{
		GroupName:            cfg.GroupName,
		GroupARN:             arn,
		RecordIdentifierName: cfg.RecordIdentifierName,
		EventTimeFeatureName: cfg.EventTimeFeatureName,
		Features:             cfg.Features,
		OnlineStoreEnabled:   cfg.OnlineStoreEnabled,
		OfflineStoreS3URI:    cfg.OfflineStoreS3URI,
		RoleARN:              cfg.RoleARN,
		Status:               driver.FeatureGroupCreated, // synchronous Creating -> Created
		CreationTime:         m.now(),
		Tags:                 copyTags(cfg.Tags),
	}
	m.featureGroups.Set(cfg.GroupName, fg)
	m.setTags(arn, cfg.Tags)

	out := *fg

	return &out, nil
}

func (m *Mock) DescribeFeatureGroup(_ context.Context, name string) (*driver.FeatureGroup, error) {
	fg, ok := m.featureGroups.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "feature group %q not found", name)
	}

	out := *fg

	return &out, nil
}

func (m *Mock) ListFeatureGroups(_ context.Context) ([]driver.FeatureGroup, error) {
	all := m.featureGroups.All()
	out := make([]driver.FeatureGroup, 0, len(all))

	for _, v := range all {
		out = append(out, *v)
	}

	return out, nil
}

func (m *Mock) DeleteFeatureGroup(_ context.Context, name string) error {
	if !m.featureGroups.Has(name) {
		return errors.Newf(errors.NotFound, "feature group %q not found", name)
	}

	m.featureGroups.Delete(name)

	return nil
}

// --- Online store runtime ---

// PutRecord writes a record to the online store, keyed by the record
// identifier feature's value.
func (m *Mock) PutRecord(_ context.Context, groupName string, record []driver.FeatureValue) error {
	fg, ok := m.featureGroups.Get(groupName)
	if !ok {
		return errors.Newf(errors.NotFound, "feature group %q not found", groupName)
	}

	recordID := ""

	for _, fv := range record {
		if fv.Name == fg.RecordIdentifierName {
			recordID = fv.Value

			break
		}
	}

	if recordID == "" {
		return errors.Newf(errors.InvalidArgument, "record is missing identifier feature %q", fg.RecordIdentifierName)
	}

	stored := make([]driver.FeatureValue, len(record))
	copy(stored, record)
	m.featureRecords.Set(recordKey(groupName, recordID), stored)

	return nil
}

func (m *Mock) GetRecord(_ context.Context, groupName, recordID string) ([]driver.FeatureValue, error) {
	if !m.featureGroups.Has(groupName) {
		return nil, errors.Newf(errors.NotFound, "feature group %q not found", groupName)
	}

	rec, ok := m.featureRecords.Get(recordKey(groupName, recordID))
	if !ok {
		return nil, errors.Newf(errors.NotFound, "record %q not found", recordID)
	}

	out := make([]driver.FeatureValue, len(rec))
	copy(out, rec)

	return out, nil
}

func (m *Mock) DeleteRecord(_ context.Context, groupName, recordID string) error {
	if !m.featureGroups.Has(groupName) {
		return errors.Newf(errors.NotFound, "feature group %q not found", groupName)
	}

	m.featureRecords.Delete(recordKey(groupName, recordID))

	return nil
}
