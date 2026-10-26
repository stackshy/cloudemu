package driver

import "context"

// Origin control values of a package (the AllowPublish and AllowUpstream enums).
const (
	OriginAllow = "ALLOW"
	OriginBlock = "BLOCK"
)

// OriginRestrictions are the package-level origin controls: whether versions may
// be published directly and whether they may enter from an upstream source.
type OriginRestrictions struct {
	Publish  string
	Upstream string
}

// Package is a package record in a repository.
type Package struct {
	Format       string
	Namespace    string
	Name         string
	Restrictions OriginRestrictions
}

// ListPackagesInput filters the packages of a repository. Namespace and Prefix
// are prefix filters; an empty Format, Namespace, Prefix, Publish or Upstream
// applies no filter.
type ListPackagesInput struct {
	Repository RepositoryRef
	Format     string
	Namespace  string
	Prefix     string
	Publish    string
	Upstream   string
	Page       Page
}

// Packages is the optional capability for package records and their origin controls.
type Packages interface {
	// ListPackages lists the packages of a repository.
	ListPackages(ctx context.Context, in *ListPackagesInput) (pkgs []*Package, nextToken string, err error)
	// DescribePackage returns a package record.
	DescribePackage(ctx context.Context, ref PackageRef) (*Package, error)
	// PutPackageOriginConfiguration sets a package's origin controls, creating an empty record when it does not exist.
	PutPackageOriginConfiguration(ctx context.Context, ref PackageRef, restrictions OriginRestrictions) (*Package, error)
	// DeletePackage deletes a package with all its versions and assets.
	DeletePackage(ctx context.Context, ref PackageRef) (*Package, error)
}
