package driver

import (
	"context"
	"time"
)

// Package version statuses (the PackageVersionStatus enum of the CodeArtifact API).
const (
	VersionStatusPublished  = "Published"
	VersionStatusUnfinished = "Unfinished"
	VersionStatusUnlisted   = "Unlisted"
	VersionStatusArchived   = "Archived"
	VersionStatusDisposed   = "Disposed"
	VersionStatusDeleted    = "Deleted"
)

// Origin types of a package version.
const (
	OriginTypeInternal = "INTERNAL"
	OriginTypeExternal = "EXTERNAL"
	OriginTypeUnknown  = "UNKNOWN"
)

// Error codes of a per-version failure in a bulk package version operation (the
// PackageVersionErrorCode enum).
const (
	VersionErrAlreadyExists    = "ALREADY_EXISTS"
	VersionErrMismatchedRev    = "MISMATCHED_REVISION"
	VersionErrMismatchedStatus = "MISMATCHED_STATUS"
	VersionErrNotAllowed       = "NOT_ALLOWED"
	VersionErrNotFound         = "NOT_FOUND"
	VersionErrSkipped          = "SKIPPED"
)

// SortByPublishedTime is the only sort order ListPackageVersions supports.
const SortByPublishedTime = "PUBLISHED_TIME"

// Asset hash algorithms (the HashAlgorithm enum).
const (
	HashMD5    = "MD5"
	HashSHA1   = "SHA-1"
	HashSHA256 = "SHA-256"
	HashSHA512 = "SHA-512"
)

// MaxAssetBytes is the largest asset the emulator stores. AWS allows 5 GiB (an
// adjustable quota); an in-memory emulator holds the bytes, so it stops at
// 128 MiB.
const MaxAssetBytes = 128 << 20

// VersionOrigin says how a package version entered the repository.
type VersionOrigin struct {
	OriginType     string
	RepositoryName string
}

// PackageVersionSummary is one entry of ListPackageVersions.
type PackageVersionSummary struct {
	Version  string
	Revision string
	Status   string
	Origin   VersionOrigin
}

// PackageVersion is the full description of a package version.
type PackageVersion struct {
	Format        string
	Namespace     string
	PackageName   string
	DisplayName   string
	Version       string
	Revision      string
	Status        string
	PublishedTime time.Time
	Origin        VersionOrigin
}

// VersionIdentity names a package version and its current revision. It is the
// whole payload of the readme and dependency calls: generic package versions
// carry no extractable metadata, so those calls report the identity only.
type VersionIdentity struct {
	Format    string
	Namespace string
	Package   string
	Version   string
	Revision  string
}

// Asset is one file of a package version.
type Asset struct {
	Name   string
	Size   int64
	Hashes map[string]string
}

// AssetList is the assets of a package version.
type AssetList struct {
	VersionIdentity
	Assets []Asset
}

// AssetContent is a downloaded asset: its bytes and the identity headers.
type AssetContent struct {
	Asset
	Content  []byte
	Version  string
	Revision string
}

// ListPackageVersionsInput filters the versions of a package.
type ListPackageVersionsInput struct {
	Ref        PackageRef
	Status     string
	SortBy     string
	OriginType string
	Page       Page
}

// PackageVersionList is a page of package versions.
type PackageVersionList struct {
	DefaultDisplayVersion string
	Format                string
	Namespace             string
	Package               string
	Versions              []PackageVersionSummary
	NextToken             string
}

// PublishInput is a PublishPackageVersion request. SHA256 is the hex digest the
// caller computed over Content.
type PublishInput struct {
	Ref        PackageRef
	Version    string
	AssetName  string
	SHA256     string
	Content    []byte
	Unfinished bool
}

// PublishResult is the outcome of a publish.
type PublishResult struct {
	VersionIdentity
	Status string
	Asset  Asset
}

// VersionsInput names the versions of a package a bulk operation applies to.
// VersionRevisions optionally pins each version to a revision. ExpectedStatus,
// when set, must match the status of every version.
type VersionsInput struct {
	Ref              PackageRef
	Versions         []string
	VersionRevisions map[string]string
	ExpectedStatus   string
}

// UpdateStatusInput is an UpdatePackageVersionsStatus request.
type UpdateStatusInput struct {
	VersionsInput
	TargetStatus string
}

// CopyInput is a CopyPackageVersions request within one domain.
type CopyInput struct {
	Ref                   PackageRef
	SourceRepository      string
	DestinationRepository string
	Versions              []string
	VersionRevisions      map[string]string
	AllowOverwrite        bool
	IncludeFromUpstream   bool
}

// SuccessfulVersion is a version a bulk operation applied to.
type SuccessfulVersion struct {
	Revision string
	Status   string
}

// FailedVersion is a version a bulk operation did not apply to and why.
type FailedVersion struct {
	ErrorCode    string
	ErrorMessage string
}

// VersionsResult is the per-version outcome of a bulk operation, keyed by
// version.
type VersionsResult struct {
	Successful map[string]SuccessfulVersion
	Failed     map[string]FailedVersion
}

// PackageVersions is the optional capability for package versions and their assets.
//
// Only generic packages can be published through the API.
type PackageVersions interface {
	// ListPackageVersions lists the versions of a package, newest published first.
	ListPackageVersions(ctx context.Context, in *ListPackageVersionsInput) (*PackageVersionList, error)
	// DescribePackageVersion returns the description of one package version.
	DescribePackageVersion(ctx context.Context, ref PackageRef, version string) (*PackageVersion, error)
	// PublishPackageVersion adds an asset to a generic package version, creating the version when needed.
	PublishPackageVersion(ctx context.Context, in *PublishInput) (*PublishResult, error)
	// ListPackageVersionAssets lists the assets of a package version.
	ListPackageVersionAssets(ctx context.Context, ref PackageRef, version string, page Page) (list *AssetList, nextToken string, err error)
	// GetPackageVersionAsset returns the bytes of one asset.
	GetPackageVersionAsset(ctx context.Context, ref PackageRef, version, asset, revision string) (*AssetContent, error)
	// GetPackageVersionReadme returns the identity of a version; generic versions have no readme text.
	GetPackageVersionReadme(ctx context.Context, ref PackageRef, version string) (*VersionIdentity, error)
	// ListPackageVersionDependencies returns the identity of a version; generic versions have no dependencies.
	ListPackageVersionDependencies(ctx context.Context, ref PackageRef, version string) (*VersionIdentity, error)
	// UpdatePackageVersionsStatus moves versions to Archived, Published or Unlisted.
	UpdatePackageVersionsStatus(ctx context.Context, in *UpdateStatusInput) (*VersionsResult, error)
	// DisposePackageVersions deletes the assets of versions and marks them Disposed.
	DisposePackageVersions(ctx context.Context, in *VersionsInput) (*VersionsResult, error)
	// DeletePackageVersions removes versions and their assets.
	DeletePackageVersions(ctx context.Context, in *VersionsInput) (*VersionsResult, error)
	// CopyPackageVersions copies versions between two repositories of one domain.
	CopyPackageVersions(ctx context.Context, in *CopyInput) (*VersionsResult, error)
}
