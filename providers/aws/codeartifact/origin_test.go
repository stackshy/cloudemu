package codeartifact_test

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/services/codeartifact/driver"
)

func TestPutPackageOriginConfiguration(t *testing.T) {
	m := seeded(t, config.NewOptions(), "repo")
	ctx := context.Background()
	ref := pkgRef("repo", "pkg")

	for _, bad := range []driver.OriginRestrictions{{}, {Publish: "ALLOW"}, {Publish: "ALLOW", Upstream: "MAYBE"}} {
		_, err := m.PutPackageOriginConfiguration(ctx, ref, bad)
		requireAPIError(t, err, driver.ExValidation)
	}

	got, err := m.PutPackageOriginConfiguration(ctx, ref, driver.OriginRestrictions{Publish: "BLOCK", Upstream: "ALLOW"})
	requireNoError(t, err)

	if got.Restrictions.Publish != "BLOCK" || got.Restrictions.Upstream != "ALLOW" || got.Name != "pkg" {
		t.Fatalf("put = %+v", got)
	}

	desc, err := m.DescribePackage(ctx, ref)
	requireNoError(t, err)

	if desc.Restrictions != got.Restrictions {
		t.Fatalf("describe = %+v", desc)
	}

	// The package exists with no versions and shows up in lists.
	pkgs, _, err := m.ListPackages(ctx, &driver.ListPackagesInput{Repository: driver.RepositoryRef{Domain: "dom", Repository: "repo"}})
	requireNoError(t, err)

	vers, err := m.ListPackageVersions(ctx, &driver.ListPackageVersionsInput{Ref: ref})
	requireNoError(t, err)

	if len(pkgs) != 1 || len(vers.Versions) != 0 {
		t.Fatalf("pkgs=%d versions=%d", len(pkgs), len(vers.Versions))
	}

	_, err = m.DeletePackage(ctx, ref)
	requireNoError(t, err)

	_, err = m.DescribePackage(ctx, ref)
	requireAPIError(t, err, driver.ExNotFound)

	pypi := ref
	pypi.Format, pypi.Namespace = "pypi", "ns"

	_, err = m.PutPackageOriginConfiguration(ctx, pypi, driver.OriginRestrictions{Publish: "ALLOW", Upstream: "ALLOW"})
	requireAPIError(t, err, driver.ExValidation)
}

func TestOriginConfigKeepsEmptyPackageAfterLastVersionDelete(t *testing.T) {
	m := seeded(t, config.NewOptions(), "repo")
	ctx := context.Background()
	ref := pkgRef("repo", "pkg")

	publishAsset(t, m, ref, "1", "f", []byte("x"), false)

	_, err := m.PutPackageOriginConfiguration(ctx, ref, driver.OriginRestrictions{Publish: "ALLOW", Upstream: "BLOCK"})
	requireNoError(t, err)

	_, err = m.DeletePackageVersions(ctx, &driver.VersionsInput{Ref: ref, Versions: []string{"1"}})
	requireNoError(t, err)

	_, err = m.DescribePackage(ctx, ref)
	requireNoError(t, err)
}

func TestPublishDefaultsAndBlockedByOrigin(t *testing.T) {
	m := seeded(t, config.NewOptions(), "repo")
	ctx := context.Background()
	ref := pkgRef("repo", "pkg")

	publishAsset(t, m, ref, "1", "f", []byte("x"), false)

	pkg, err := m.DescribePackage(ctx, ref)
	requireNoError(t, err)

	if pkg.Restrictions.Publish != "ALLOW" || pkg.Restrictions.Upstream != "BLOCK" {
		t.Fatalf("default restrictions = %+v, want publish ALLOW upstream BLOCK", pkg.Restrictions)
	}

	_, err = m.PutPackageOriginConfiguration(ctx, ref, driver.OriginRestrictions{Publish: "BLOCK", Upstream: "BLOCK"})
	requireNoError(t, err)

	_, err = m.PublishPackageVersion(ctx, &driver.PublishInput{
		Ref: ref, Version: "2", AssetName: "f", SHA256: sha([]byte("y")), Content: []byte("y"),
	})
	requireAPIError(t, err, driver.ExConflict)

	vers, err := m.ListPackageVersions(ctx, &driver.ListPackageVersionsInput{Ref: ref})
	requireNoError(t, err)

	if len(vers.Versions) != 1 {
		t.Fatalf("blocked publish stored a version: %+v", vers.Versions)
	}

	_, err = m.PutPackageOriginConfiguration(ctx, ref, driver.OriginRestrictions{Publish: "ALLOW", Upstream: "BLOCK"})
	requireNoError(t, err)

	publishAsset(t, m, ref, "2", "f", []byte("y"), false)
}

func TestEffectiveOriginBlockWins(t *testing.T) {
	ctx := context.Background()
	dom := driver.DomainRef{Domain: "dom"}

	type tc struct {
		name     string
		pkgPub   string
		groupPub string
		allow    bool
		repo     string
	}

	cases := []tc{
		{"both allow", "ALLOW", driver.ModeAllow, true, "repo"},
		{"group blocks", "ALLOW", driver.ModeBlock, false, "repo"},
		{"package blocks", "BLOCK", driver.ModeAllow, false, "repo"},
		{"specific repo allowed", "ALLOW", driver.ModeAllowSpecificRepositories, true, "repo"},
		{"specific repo not listed", "ALLOW", driver.ModeAllowSpecificRepositories, false, "other"},
	}

	for _, c := range cases {
		m := seeded(t, config.NewOptions(), "repo", "other")

		_, err := m.CreatePackageGroup(ctx, &driver.CreatePackageGroupInput{Domain: dom, Pattern: "/generic/ns/*"})
		requireNoError(t, err)

		_, err = m.UpdatePackageGroupOriginConfiguration(ctx, &driver.UpdateGroupOriginInput{
			Domain: dom, Pattern: "/generic/ns/*", Restrictions: map[string]string{driver.RestrictionPublish: c.groupPub},
		})
		requireNoError(t, err)

		if c.groupPub == driver.ModeAllowSpecificRepositories {
			_, err = m.UpdatePackageGroupOriginConfiguration(ctx, &driver.UpdateGroupOriginInput{
				Domain: dom, Pattern: "/generic/ns/*",
				AddAllowed: []driver.AllowedRepository{{RepositoryName: "repo", OriginRestrictionType: driver.RestrictionPublish}},
			})
			requireNoError(t, err)
		}

		ref := pkgRef(c.repo, "pkg")

		_, err = m.PutPackageOriginConfiguration(ctx, ref, driver.OriginRestrictions{Publish: c.pkgPub, Upstream: "ALLOW"})
		requireNoError(t, err)

		_, err = m.PublishPackageVersion(ctx, &driver.PublishInput{
			Ref: ref, Version: "1", AssetName: "f", SHA256: sha([]byte("x")), Content: []byte("x"),
		})

		if c.allow {
			requireNoError(t, err)
		} else {
			requireAPIError(t, err, driver.ExConflict)
		}
	}
}

func TestWeakMatchPublishBlocked(t *testing.T) {
	m := seeded(t, config.NewOptions(), "repo")
	ctx := context.Background()

	_, err := m.CreatePackageGroup(ctx, &driver.CreatePackageGroupInput{Domain: driver.DomainRef{Domain: "dom"}, Pattern: "/generic/ns/Async-Storage$"})
	requireNoError(t, err)

	exact := pkgRef("repo", "Async-Storage")
	publishAsset(t, m, exact, "1", "f", []byte("x"), false)

	variant := pkgRef("repo", "async_storage")

	_, err = m.PublishPackageVersion(ctx, &driver.PublishInput{
		Ref: variant, Version: "1", AssetName: "f", SHA256: sha([]byte("x")), Content: []byte("x"),
	})
	requireAPIError(t, err, driver.ExConflict)
}

func TestDefaultRestrictionsFollowGroup(t *testing.T) {
	m := seeded(t, config.NewOptions(), "repo")
	ctx := context.Background()
	dom := driver.DomainRef{Domain: "dom"}

	_, err := m.CreatePackageGroup(ctx, &driver.CreatePackageGroupInput{Domain: dom, Pattern: "/generic/ns/*"})
	requireNoError(t, err)

	_, err = m.UpdatePackageGroupOriginConfiguration(ctx, &driver.UpdateGroupOriginInput{
		Domain: dom, Pattern: "/generic/ns/*", Restrictions: map[string]string{driver.RestrictionExternalUpstream: driver.ModeBlock},
	})
	requireNoError(t, err)

	publishAsset(t, m, pkgRef("repo", "pkg"), "1", "f", []byte("x"), false)

	pkg, err := m.DescribePackage(ctx, pkgRef("repo", "pkg"))
	requireNoError(t, err)

	if pkg.Restrictions.Publish != "ALLOW" || pkg.Restrictions.Upstream != "ALLOW" {
		t.Fatalf("restrictions under a restrictive group = %+v, want ALLOW/ALLOW", pkg.Restrictions)
	}
}
