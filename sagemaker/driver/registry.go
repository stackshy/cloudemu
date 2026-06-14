package driver

import "context"

// Model package status values.
const (
	PackagePending    = "Pending"
	PackageInProgress = "InProgress"
	PackageCompleted  = "Completed"
	PackageFailed     = "Failed"
)

// Model approval status values.
const (
	ApprovalApproved      = "Approved"
	ApprovalRejected      = "Rejected"
	ApprovalPendingManual = "PendingManualApproval"
)

// ModelPackageGroupSpec describes a model package group to create.
type ModelPackageGroupSpec struct {
	GroupName   string
	Description string
	Tags        []Tag
}

// ModelPackageGroup is a container for versioned model packages.
type ModelPackageGroup struct {
	GroupName    string
	GroupARN     string
	Description  string
	Status       string
	CreationTime string
	Tags         []Tag
}

// ModelPackageSpec describes a model package (registry entry) to create.
type ModelPackageSpec struct {
	GroupName      string
	Description    string
	InferenceImage string
	ModelDataURL   string
	ApprovalStatus string
	Tags           []Tag
}

// ModelPackage is a versioned model registry entry.
type ModelPackage struct {
	PackageARN     string
	GroupName      string
	Version        int
	Description    string
	InferenceImage string
	ModelDataURL   string
	Status         string
	ApprovalStatus string
	CreationTime   string
	Tags           []Tag
}

// registryAPI covers the model registry (groups + versioned packages).
type registryAPI interface {
	CreateModelPackageGroup(ctx context.Context, cfg ModelPackageGroupSpec) (*ModelPackageGroup, error)
	DescribeModelPackageGroup(ctx context.Context, name string) (*ModelPackageGroup, error)
	ListModelPackageGroups(ctx context.Context) ([]ModelPackageGroup, error)
	DeleteModelPackageGroup(ctx context.Context, name string) error

	CreateModelPackage(ctx context.Context, cfg ModelPackageSpec) (*ModelPackage, error)
	DescribeModelPackage(ctx context.Context, arn string) (*ModelPackage, error)
	ListModelPackages(ctx context.Context, groupName string) ([]ModelPackage, error)
	UpdateModelPackage(ctx context.Context, arn, approvalStatus string) (*ModelPackage, error)
	DeleteModelPackage(ctx context.Context, arn string) error
}
