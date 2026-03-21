// Package driver defines the interface for container registry service implementations.
package driver

import (
	"context"
)

// Repository represents a container repository.
type Repository struct {
	Name       string
	URI        string
	CreatedAt  string
	Tags       map[string]string
	ImageCount int
}

// RepositoryConfig describes a repository to create.
type RepositoryConfig struct {
	Name               string
	Tags               map[string]string
	ImageScanOnPush    bool
	ImageTagMutability string // "MUTABLE" or "IMMUTABLE"
}

// ImageDetail describes a container image.
type ImageDetail struct {
	RegistryID   string
	Repository   string
	Digest       string
	Tags         []string
	SizeBytes    int64
	PushedAt     string
	LastPulledAt string
	MediaType    string
}

// ImageManifest represents an image manifest for pushing.
type ImageManifest struct {
	Repository string
	Tag        string
	Digest     string
	MediaType  string
	SizeBytes  int64
	Layers     []LayerInfo
}

// LayerInfo describes a layer in an image.
type LayerInfo struct {
	Digest    string
	SizeBytes int64
	MediaType string
}

// LifecycleRule defines image lifecycle policies.
type LifecycleRule struct {
	Priority    int
	Description string
	TagStatus   string // "tagged", "untagged", "any"
	TagPattern  string // glob pattern for tag matching
	CountType   string // "imageCountMoreThan" or "sinceImagePushed"
	CountValue  int    // number of images or days
	Action      string // "expire"
}

// LifecyclePolicy is a set of lifecycle rules.
type LifecyclePolicy struct {
	Rules []LifecycleRule
}

// ScanResult represents an image vulnerability scan result.
type ScanResult struct {
	Repository    string
	Digest        string
	Status        string         // "COMPLETE", "IN_PROGRESS", "FAILED"
	FindingCounts map[string]int // severity -> count (CRITICAL, HIGH, MEDIUM, LOW, INFORMATIONAL)
	CompletedAt   string
}

// ContainerRegistry is the interface that container registry providers must implement.
type ContainerRegistry interface {
	// Repository management
	CreateRepository(ctx context.Context, config RepositoryConfig) (*Repository, error)
	DeleteRepository(ctx context.Context, name string, force bool) error
	GetRepository(ctx context.Context, name string) (*Repository, error)
	ListRepositories(ctx context.Context) ([]Repository, error)

	// Image management
	PutImage(ctx context.Context, manifest *ImageManifest) (*ImageDetail, error)
	GetImage(ctx context.Context, repository, reference string) (*ImageDetail, error)
	ListImages(ctx context.Context, repository string) ([]ImageDetail, error)
	DeleteImage(ctx context.Context, repository, reference string) error
	TagImage(ctx context.Context, repository, sourceRef, targetTag string) error

	// Lifecycle policies
	PutLifecyclePolicy(ctx context.Context, repository string, policy LifecyclePolicy) error
	GetLifecyclePolicy(ctx context.Context, repository string) (*LifecyclePolicy, error)
	EvaluateLifecyclePolicy(ctx context.Context, repository string) ([]string, error)

	// Image scanning
	StartImageScan(ctx context.Context, repository, reference string) (*ScanResult, error)
	GetImageScanResults(ctx context.Context, repository, reference string) (*ScanResult, error)
}
