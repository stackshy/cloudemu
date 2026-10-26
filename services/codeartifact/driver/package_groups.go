package driver

import (
	"context"
	"time"
)

// Package group origin restriction types (the PackageGroupOriginRestrictionType enum).
const (
	RestrictionPublish          = "PUBLISH"
	RestrictionExternalUpstream = "EXTERNAL_UPSTREAM"
	RestrictionInternalUpstream = "INTERNAL_UPSTREAM"
)

// Package group origin restriction modes (the PackageGroupOriginRestrictionMode enum).
const (
	ModeAllow                     = "ALLOW"
	ModeAllowSpecificRepositories = "ALLOW_SPECIFIC_REPOSITORIES"
	ModeBlock                     = "BLOCK"
	ModeInherit                   = "INHERIT"
)

// Association strengths between a package and a package group.
const (
	AssociationStrong = "STRONG"
	AssociationWeak   = "WEAK"
)

// Updates reported for an allowed repository list change.
const (
	AllowedRepositoryAdded   = "ADDED"
	AllowedRepositoryRemoved = "REMOVED"
)

// GroupReference points at a package group by ARN and pattern.
type GroupReference struct {
	Arn     string
	Pattern string
}

// GroupRestriction is one origin restriction of a package group: the configured
// mode, the mode in effect after inheritance, where it was inherited from and
// how many repositories its allowed list holds.
type GroupRestriction struct {
	Mode              string
	EffectiveMode     string
	InheritedFrom     *GroupReference
	RepositoriesCount int64
}

// PackageGroup is a pattern-defined set of packages with origin controls.
type PackageGroup struct {
	Arn          string
	Pattern      string
	DomainName   string
	DomainOwner  string
	CreatedTime  time.Time
	ContactInfo  string
	Description  string
	Restrictions map[string]GroupRestriction
	Parent       *GroupReference
}

// AllowedRepository names a repository in the allowed list of one restriction type.
type AllowedRepository struct {
	RepositoryName        string
	OriginRestrictionType string
}

// AssociatedPackage is a package together with how strongly a group claims it.
type AssociatedPackage struct {
	Format          string
	Namespace       string
	Package         string
	AssociationType string
}

// CreatePackageGroupInput is a CreatePackageGroup request.
type CreatePackageGroupInput struct {
	Domain      DomainRef
	Pattern     string
	ContactInfo string
	Description string
	Tags        map[string]string
}

// UpdatePackageGroupInput is an UpdatePackageGroup request; a nil field is left
// unchanged.
type UpdatePackageGroupInput struct {
	Domain      DomainRef
	Pattern     string
	ContactInfo *string
	Description *string
}

// UpdateGroupOriginInput is an UpdatePackageGroupOriginConfiguration request.
type UpdateGroupOriginInput struct {
	Domain        DomainRef
	Pattern       string
	Restrictions  map[string]string
	AddAllowed    []AllowedRepository
	RemoveAllowed []AllowedRepository
}

// GroupOriginUpdate is the outcome of an origin configuration update: the group
// and, per restriction type, the repositories added and removed.
type GroupOriginUpdate struct {
	Group   *PackageGroup
	Updates map[string]map[string][]string
}

// PackageGroups is the optional capability for package groups.
type PackageGroups interface {
	// CreatePackageGroup creates a package group.
	CreatePackageGroup(ctx context.Context, in *CreatePackageGroupInput) (*PackageGroup, error)
	// DescribePackageGroup returns a package group with its parent and effective restrictions.
	DescribePackageGroup(ctx context.Context, ref DomainRef, pattern string) (*PackageGroup, error)
	// UpdatePackageGroup changes the contact info and description of a group.
	UpdatePackageGroup(ctx context.Context, in *UpdatePackageGroupInput) (*PackageGroup, error)
	// DeletePackageGroup deletes a group; its children move up to its parent.
	DeletePackageGroup(ctx context.Context, ref DomainRef, pattern string) (*PackageGroup, error)
	// UpdatePackageGroupOriginConfiguration sets restriction modes and edits the allowed repository lists.
	UpdatePackageGroupOriginConfiguration(ctx context.Context, in *UpdateGroupOriginInput) (*GroupOriginUpdate, error)

	// ListPackageGroups lists a domain's package groups.
	ListPackageGroups(ctx context.Context, ref DomainRef, prefix string, page Page) (groups []*PackageGroup, nextToken string, err error)
	// ListSubPackageGroups lists the direct children of a package group.
	ListSubPackageGroups(ctx context.Context, ref DomainRef, pattern string, page Page) (groups []*PackageGroup, nextToken string, err error)
	// ListAssociatedPackages lists the packages whose most specific group is the given one.
	ListAssociatedPackages(ctx context.Context, ref DomainRef, pattern string, preview bool,
		page Page) (pkgs []AssociatedPackage, nextToken string, err error)
	// GetAssociatedPackageGroup returns the group a package coordinate belongs to and how strongly.
	GetAssociatedPackageGroup(ctx context.Context, ref DomainRef, format, namespace, pkg string) (*PackageGroup, string, error)
	// ListAllowedRepositoriesForGroup lists the allowed repositories of one restriction type.
	ListAllowedRepositoriesForGroup(ctx context.Context, ref DomainRef, pattern, restrictionType string,
		page Page) (repos []string, nextToken string, err error)
}
