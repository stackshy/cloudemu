package driver

import "context"

// Feature group status values.
const (
	FeatureGroupCreating     = "Creating"
	FeatureGroupCreated      = "Created"
	FeatureGroupCreateFailed = "CreateFailed"
	FeatureGroupDeleting     = "Deleting"
	FeatureGroupDeleteFailed = "DeleteFailed"
)

// FeatureDefinition describes one feature in a group.
type FeatureDefinition struct {
	Name string
	Type string // Integral, Fractional, String
}

// FeatureGroupSpec describes a feature group to create.
type FeatureGroupSpec struct {
	GroupName            string
	RecordIdentifierName string
	EventTimeFeatureName string
	Features             []FeatureDefinition
	OnlineStoreEnabled   bool
	OfflineStoreS3URI    string
	RoleARN              string
	Tags                 []Tag
}

// FeatureGroup is a Feature Store feature group.
type FeatureGroup struct {
	GroupName            string
	GroupARN             string
	RecordIdentifierName string
	EventTimeFeatureName string
	Features             []FeatureDefinition
	OnlineStoreEnabled   bool
	OfflineStoreS3URI    string
	RoleARN              string
	Status               string
	FailureReason        string
	CreationTime         string
	Tags                 []Tag
}

// FeatureValue is one feature name/value pair in an online-store record.
type FeatureValue struct {
	Name  string
	Value string
}

// featureStoreAPI covers Feature Store control plane plus the online-store
// runtime (PutRecord/GetRecord/DeleteRecord), which the real service exposes
// via the sagemaker-featurestore-runtime endpoint.
type featureStoreAPI interface {
	CreateFeatureGroup(ctx context.Context, cfg FeatureGroupSpec) (*FeatureGroup, error)
	DescribeFeatureGroup(ctx context.Context, name string) (*FeatureGroup, error)
	ListFeatureGroups(ctx context.Context) ([]FeatureGroup, error)
	DeleteFeatureGroup(ctx context.Context, name string) error

	PutRecord(ctx context.Context, groupName string, record []FeatureValue) error
	GetRecord(ctx context.Context, groupName, recordID string) ([]FeatureValue, error)
	DeleteRecord(ctx context.Context, groupName, recordID string) error
}
