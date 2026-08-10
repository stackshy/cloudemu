package opensearch

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/opensearch/driver"
)

func copyPackage(p *driver.Package) driver.Package { return *p }

func copyAssociation(a *driver.DomainPackageAssociation) driver.DomainPackageAssociation { return *a }

func assocKey(packageID, domainName string) string { return packageID + "|" + domainName }

// CreatePackage creates a package that is immediately AVAILABLE.
//
//nolint:gocritic // hugeParam: signature is fixed by the driver.OpenSearch interface (by-value input).
func (m *Mock) CreatePackage(_ context.Context, in driver.CreatePackageInput) (*driver.Package, error) {
	if in.PackageName == "" || in.PackageType == "" {
		return nil, validation("PackageName and PackageType are required")
	}

	now := m.now()
	pkg := &driver.Package{
		PackageID:          idgen.GenerateID("F"),
		PackageName:        in.PackageName,
		PackageType:        in.PackageType,
		PackageDescription: in.PackageDescription,
		PackageStatus:      driver.PackageStatusAvailable,
		CreatedAt:          now,
		LastUpdatedAt:      now,
		AvailableVersion:   "1",
		EngineVersion:      in.EngineVersion,
		S3BucketName:       in.S3BucketName,
		S3Key:              in.S3Key,
	}

	// Claim the (unique) package name atomically before publishing the record.
	if !m.pkgNames.SetIfAbsent(in.PackageName, pkg.PackageID) {
		return nil, alreadyExists("Package already exists: %s", in.PackageName)
	}

	if !m.packages.SetIfAbsent(pkg.PackageID, pkg) {
		m.pkgNames.Delete(in.PackageName)

		return nil, alreadyExists("Package already exists: %s", pkg.PackageID)
	}

	out := copyPackage(pkg)

	return &out, nil
}

// DeletePackage removes a package and returns its final record.
func (m *Mock) DeletePackage(_ context.Context, packageID string) (*driver.Package, error) {
	pkg, ok := m.packages.Get(packageID)
	if !ok {
		return nil, notFound("Package not found: %s", packageID)
	}

	out := copyPackage(pkg)
	out.PackageStatus = driver.PackageStatusDeleted

	m.packages.Delete(packageID)
	m.pkgNames.Delete(pkg.PackageName)

	return &out, nil
}

// DescribePackages returns the paginated list of packages, sorted by ID.
func (m *Mock) DescribePackages(_ context.Context, page driver.Page) ([]driver.Package, string, error) {
	return listStore(m.packages, copyPackage, page)
}

// UpdatePackage updates a package's description and source location.
func (m *Mock) UpdatePackage(_ context.Context, packageID, description, bucket, key string) (*driver.Package, error) {
	pkg, ok := m.packages.Get(packageID)
	if !ok {
		return nil, notFound("Package not found: %s", packageID)
	}

	out := copyPackage(pkg)
	out.PackageDescription = description

	if bucket != "" {
		out.S3BucketName = bucket
	}

	if key != "" {
		out.S3Key = key
	}

	out.LastUpdatedAt = m.now()
	m.packages.Set(packageID, &out)

	result := copyPackage(&out)

	return &result, nil
}

// UpdatePackageScope adds or removes users from a package's access scope,
// returning the package ID and the resulting user list.
func (m *Mock) UpdatePackageScope(
	_ context.Context, packageID, operation string, users []string,
) (scopeID string, scopedUsers []string, err error) {
	if _, ok := m.packages.Get(packageID); !ok {
		return "", nil, notFound("Package not found: %s", packageID)
	}

	switch operation {
	case "ADD", "OVERRIDE", "REMOVE":
	default:
		return "", nil, validation("Unsupported operation: %s", operation)
	}

	return packageID, copyStrings(users), nil
}

// GetPackageVersionHistory returns a synthesized single-version history.
func (m *Mock) GetPackageVersionHistory(
	_ context.Context, packageID string, _ driver.Page,
) (pkgID string, history []map[string]json.RawMessage, nextToken string, err error) {
	pkg, ok := m.packages.Get(packageID)
	if !ok {
		return "", nil, "", notFound("Package not found: %s", packageID)
	}

	history = []map[string]json.RawMessage{{
		"PackageVersion":   rawString("1"),
		"CommitMessage":    rawString("Initial version"),
		"CreatedAt":        rawFloat(float64(pkg.CreatedAt.Unix())),
		"PluginProperties": json.RawMessage("null"),
	}}

	return packageID, history, "", nil
}

// associate records a package/domain association and returns its record.
func (m *Mock) associate(packageID, domainName string) (*driver.DomainPackageAssociation, error) {
	pkg, ok := m.packages.Get(packageID)
	if !ok {
		return nil, notFound("Package not found: %s", packageID)
	}

	if _, err := m.getDomain(domainName); err != nil {
		return nil, err
	}

	assoc := &driver.DomainPackageAssociation{
		PackageID:           packageID,
		PackageName:         pkg.PackageName,
		PackageType:         pkg.PackageType,
		DomainName:          domainName,
		DomainPackageStatus: "ACTIVE",
		PackageVersion:      pkg.AvailableVersion,
		ReferencePath:       "packages/" + packageID,
	}
	m.pkgAssoc.Set(assocKey(packageID, domainName), assoc)

	out := copyAssociation(assoc)

	return &out, nil
}

// AssociatePackage associates one package with a domain.
func (m *Mock) AssociatePackage(_ context.Context, packageID, domainName string) (*driver.DomainPackageAssociation, error) {
	return m.associate(packageID, domainName)
}

// AssociatePackages associates multiple packages with a domain. All items are
// validated before any is written, so a mid-batch failure leaves no partial
// associations behind.
func (m *Mock) AssociatePackages(_ context.Context, packageIDs []string, domainName string) ([]driver.DomainPackageAssociation, error) {
	if _, err := m.getDomain(domainName); err != nil {
		return nil, err
	}

	for _, id := range packageIDs {
		if _, ok := m.packages.Get(id); !ok {
			return nil, notFound("Package not found: %s", id)
		}
	}

	out := make([]driver.DomainPackageAssociation, 0, len(packageIDs))

	for _, id := range packageIDs {
		assoc, err := m.associate(id, domainName)
		if err != nil {
			return nil, err
		}

		out = append(out, *assoc)
	}

	return out, nil
}

// dissociate removes a package/domain association and returns its final record.
func (m *Mock) dissociate(packageID, domainName string) (*driver.DomainPackageAssociation, error) {
	assoc, ok := m.pkgAssoc.Get(assocKey(packageID, domainName))
	if !ok {
		return nil, notFound("Association not found for package %s and domain %s", packageID, domainName)
	}

	out := copyAssociation(assoc)
	out.DomainPackageStatus = "DISSOCIATING"

	m.pkgAssoc.Delete(assocKey(packageID, domainName))

	return &out, nil
}

// DissociatePackage removes one package/domain association.
func (m *Mock) DissociatePackage(_ context.Context, packageID, domainName string) (*driver.DomainPackageAssociation, error) {
	return m.dissociate(packageID, domainName)
}

// DissociatePackages removes multiple package/domain associations. All items are
// validated before any is removed, so a mid-batch failure leaves the existing
// associations intact.
func (m *Mock) DissociatePackages(_ context.Context, packageIDs []string, domainName string) ([]driver.DomainPackageAssociation, error) {
	for _, id := range packageIDs {
		if _, ok := m.pkgAssoc.Get(assocKey(id, domainName)); !ok {
			return nil, notFound("Association not found for package %s and domain %s", id, domainName)
		}
	}

	out := make([]driver.DomainPackageAssociation, 0, len(packageIDs))

	for _, id := range packageIDs {
		assoc, err := m.dissociate(id, domainName)
		if err != nil {
			return nil, err
		}

		out = append(out, *assoc)
	}

	return out, nil
}

// ListPackagesForDomain lists the packages associated with a domain.
func (m *Mock) ListPackagesForDomain(_ context.Context, domainName string,
	page driver.Page,
) ([]driver.DomainPackageAssociation, string, error) {
	if _, err := m.getDomain(domainName); err != nil {
		return nil, "", err
	}

	out, err := m.filterAssociations(func(a *driver.DomainPackageAssociation) bool {
		return a.DomainName == domainName
	}, page)

	return out, "", err
}

// ListDomainsForPackage lists the domains a package is associated with.
func (m *Mock) ListDomainsForPackage(_ context.Context, packageID string,
	page driver.Page,
) ([]driver.DomainPackageAssociation, string, error) {
	if _, ok := m.packages.Get(packageID); !ok {
		return nil, "", notFound("Package not found: %s", packageID)
	}

	out, err := m.filterAssociations(func(a *driver.DomainPackageAssociation) bool {
		return a.PackageID == packageID
	}, page)

	return out, "", err
}

// filterAssociations returns matching associations sorted by key. Pagination is
// applied; a corrupt token surfaces as an InvalidPaginationTokenException.
func (m *Mock) filterAssociations(
	match func(*driver.DomainPackageAssociation) bool, page driver.Page,
) ([]driver.DomainPackageAssociation, error) {
	keys := m.pkgAssoc.Keys()
	sort.Strings(keys)

	out := make([]driver.DomainPackageAssociation, 0, len(keys))

	for _, k := range keys {
		if a, ok := m.pkgAssoc.Get(k); ok && match(a) {
			out = append(out, copyAssociation(a))
		}
	}

	start, end, _, err := paginate(len(out), page)
	if err != nil {
		return nil, err
	}

	return out[start:end], nil
}
