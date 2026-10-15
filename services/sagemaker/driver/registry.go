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

// Model package group policy and batch-describe limits.
const (
	MaxBatchDescribePackages = 100
	MaxGroupPolicyLength     = 20480
)

// BatchPackageError reports why one ARN of a BatchDescribeModelPackage request
// could not be described.
type BatchPackageError struct {
	Code    string
	Message string
}

// BatchDescribeResult is the outcome of BatchDescribeModelPackage: the packages
// found and, per missing ARN, why it was not.
type BatchDescribeResult struct {
	Packages map[string]ModelPackage
	Errors   map[string]BatchPackageError
}

// registryAPI covers the model registry (groups + versioned packages).
type registryAPI interface {
	CreateModelPackageGroup(ctx context.Context, cfg ModelPackageGroupSpec) (*ModelPackageGroup, error)
	DescribeModelPackageGroup(ctx context.Context, name string) (*ModelPackageGroup, error)
	ListModelPackageGroups(ctx context.Context, opts ListOptions) (ListPage[ModelPackageGroup], error)
	DeleteModelPackageGroup(ctx context.Context, name string) error

	CreateModelPackage(ctx context.Context, cfg ModelPackageSpec) (*ModelPackage, error)
	DescribeModelPackage(ctx context.Context, arn string) (*ModelPackage, error)
	ListModelPackages(ctx context.Context, groupName string, opts ListOptions) (ListPage[ModelPackage], error)
	UpdateModelPackage(ctx context.Context, arn, approvalStatus string) (*ModelPackage, error)
	DeleteModelPackage(ctx context.Context, arn string) error
	BatchDescribeModelPackage(ctx context.Context, arns []string) (*BatchDescribeResult, error)

	PutModelPackageGroupPolicy(ctx context.Context, groupName, policy string) (string, error)
	GetModelPackageGroupPolicy(ctx context.Context, groupName string) (string, error)
	DeleteModelPackageGroupPolicy(ctx context.Context, groupName string) error
}
