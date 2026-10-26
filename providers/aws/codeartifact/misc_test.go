package codeartifact_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/services/codeartifact/driver"
)

func TestEmptyAccountLists(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	domains, next, err := m.ListDomains(ctx, driver.Page{})
	requireNoError(t, err)

	repos, next2, err := m.ListRepositories(ctx, "", driver.Page{})
	requireNoError(t, err)

	if len(domains) != 0 || len(repos) != 0 || next != "" || next2 != "" {
		t.Fatalf("empty account lists = %d/%d (%q/%q)", len(domains), len(repos), next, next2)
	}
}

func TestServiceQuotas(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	for i := 0; i < 10; i++ {
		mustDomain(t, m, fmt.Sprintf("dom-%02d", i))
	}

	_, err := m.CreateDomain(ctx, &driver.CreateDomainInput{Name: "dom-over"})
	e := requireAPIError(t, err, driver.ExServiceQuota)

	if e.ResourceType != driver.ResourceTypeDomain {
		t.Fatalf("resource type = %q", e.ResourceType)
	}

	// Deleting one frees a slot.
	_, err = m.DeleteDomain(ctx, "dom-00", "")
	requireNoError(t, err)
	mustDomain(t, m, "dom-over")
}

func TestRepositoriesPerDomainQuota(t *testing.T) {
	m := newMock()
	mustDomain(t, m, "dom")

	for i := 0; i < 1000; i++ {
		mustRepo(t, m, "dom", fmt.Sprintf("repo-%04d", i))
	}

	_, err := m.CreateRepository(context.Background(), &driver.CreateRepositoryInput{Domain: "dom", Repository: "repo-over"})
	requireAPIError(t, err, driver.ExServiceQuota)
}

func TestRejectedInputsLeaveNoSideEffects(t *testing.T) {
	opts := config.NewOptions()
	m := seeded(t, opts, "repo")
	pub := &recordingPublisher{}
	m.SetEventPublisher(pub)
	ctx := context.Background()

	before := observeBasic(t, m)

	_, _ = m.CreateDomain(ctx, &driver.CreateDomainInput{Name: "BAD NAME"})
	_, _ = m.CreateRepository(ctx, &driver.CreateRepositoryInput{Domain: "dom", Repository: "ok-repo", Upstreams: []driver.UpstreamRef{{RepositoryName: "ghost"}}})
	_, _ = m.PublishPackageVersion(ctx, &driver.PublishInput{Ref: pkgRef("repo", "pkg"), Version: "1", AssetName: "f", SHA256: "bad", Content: []byte("x")})
	_, _ = m.CreatePackageGroup(ctx, &driver.CreatePackageGroupInput{Domain: driver.DomainRef{Domain: "dom"}, Pattern: "no-slash"})
	_ = m.TagResource(ctx, "arn:aws:codeartifact:us-east-1:123456789012:domain/ghost", map[string]string{"a": "b"})
	_, _ = m.PutDomainPermissionsPolicy(ctx, driver.DomainRef{Domain: "dom"}, "", "not json")

	if after := observeBasic(t, m); after != before {
		t.Fatalf("rejected requests changed state:\n before %s\n after  %s", before, after)
	}

	if n := len(pub.all()); n != 0 {
		t.Fatalf("rejected requests emitted %d events", n)
	}
}

func observeBasic(t *testing.T, m interface {
	ListDomains(context.Context, driver.Page) ([]*driver.Domain, string, error)
	ListRepositories(context.Context, string, driver.Page) ([]*driver.Repository, string, error)
	ListPackageGroups(context.Context, driver.DomainRef, string, driver.Page) ([]*driver.PackageGroup, string, error)
	ListPackages(context.Context, *driver.ListPackagesInput) ([]*driver.Package, string, error)
}) string {
	t.Helper()

	ctx := context.Background()

	d, _, err := m.ListDomains(ctx, driver.Page{})
	requireNoError(t, err)

	r, _, err := m.ListRepositories(ctx, "", driver.Page{})
	requireNoError(t, err)

	g, _, err := m.ListPackageGroups(ctx, driver.DomainRef{Domain: "dom"}, "", driver.Page{})
	requireNoError(t, err)

	p, _, err := m.ListPackages(ctx, &driver.ListPackagesInput{Repository: driver.RepositoryRef{Domain: "dom", Repository: "repo"}})
	requireNoError(t, err)

	return fmt.Sprintf("%d domains %d repos %d groups %d packages", len(d), len(r), len(g), len(p))
}

func TestARNShapes(t *testing.T) {
	opts := config.NewOptions()
	m := seeded(t, opts, "repo")
	pub := &recordingPublisher{}
	m.SetEventPublisher(pub)

	d, err := m.DescribeDomain(context.Background(), "dom", "")
	requireNoError(t, err)

	r := mustRepoDescribe(t, m)
	g := createGroup(t, m, "/npm/space/*")

	prefix := "arn:aws:codeartifact:" + opts.Region + ":" + opts.AccountID + ":"

	want := map[string]string{
		d.Arn: prefix + "domain/dom",
		r.Arn: prefix + "repository/dom/repo",
		g.Arn: prefix + "package-group/dom/npm/space/*",
	}

	for got, w := range want {
		if got != w {
			t.Errorf("arn = %q, want %q", got, w)
		}
	}

	publishAsset(t, m, pkgRef("repo", "pkg"), "1", "f", []byte("x"), false)

	if res := pub.all()[0].resources[0]; res != prefix+"package/dom/repo/generic/ns/pkg" {
		t.Errorf("package arn = %q", res)
	}
}

func mustRepoDescribe(t *testing.T, m interface {
	DescribeRepository(context.Context, string, string, string) (*driver.Repository, error)
}) *driver.Repository {
	t.Helper()

	r, err := m.DescribeRepository(context.Background(), "dom", "", "repo")
	requireNoError(t, err)

	return r
}

func TestListRepositoriesInDomainAdministratorFilter(t *testing.T) {
	opts := config.NewOptions()
	m := seeded(t, opts, "aa", "bb")
	ctx := context.Background()
	dom := driver.DomainRef{Domain: "dom"}

	own, _, err := m.ListRepositoriesInDomainByAdministrator(ctx, dom, opts.AccountID, "", driver.Page{})
	requireNoError(t, err)

	other, _, err := m.ListRepositoriesInDomainByAdministrator(ctx, dom, "999999999999", "", driver.Page{})
	requireNoError(t, err)

	if len(own) != 2 || len(other) != 0 {
		t.Fatalf("own=%d other=%d", len(own), len(other))
	}

	_, _, err = m.ListRepositoriesInDomainByAdministrator(ctx, dom, "bad", "", driver.Page{})
	requireAPIError(t, err, driver.ExValidation)
}

func TestPackageFilterAndRefValidation(t *testing.T) {
	m := seeded(t, config.NewOptions(), "repo")
	ctx := context.Background()
	repo := driver.RepositoryRef{Domain: "dom", Repository: "repo"}

	for _, in := range []driver.ListPackagesInput{
		{Repository: repo, Format: "docker"}, {Repository: repo, Namespace: "a/b"}, {Repository: repo, Prefix: "bad name"},
		{Repository: repo, Publish: "MAYBE"}, {Repository: repo, Upstream: "MAYBE"},
	} {
		_, _, err := m.ListPackages(ctx, &in)
		requireAPIError(t, err, driver.ExValidation)
	}

	bad := []driver.PackageRef{pkgRef("repo", ""), pkgRef("repo", "a#b"), pkgRef("repo", "pkg")}
	bad[2].Namespace = "has space"

	for _, ref := range bad {
		_, err := m.DescribePackage(ctx, ref)
		requireAPIError(t, err, driver.ExValidation)
	}

	_, err := m.GetPackageVersionAsset(ctx, pkgRef("repo", "pkg"), "1", "", "")
	requireAPIError(t, err, driver.ExValidation)

	_, err = m.GetPackageVersionAsset(ctx, pkgRef("repo", "pkg"), "1", "f", "has space")
	requireAPIError(t, err, driver.ExValidation)

	_, err = m.GetPackageVersionReadme(ctx, pkgRef("repo", "pkg"), "bad version")
	requireAPIError(t, err, driver.ExValidation)
}

func TestBulkRequestValidation(t *testing.T) {
	m := seeded(t, config.NewOptions(), "repo")
	ctx := context.Background()
	ref := pkgRef("repo", "pkg")

	publishAsset(t, m, ref, "1", "f", []byte("x"), false)

	for _, in := range []driver.VersionsInput{
		{Ref: ref, Versions: []string{"bad version"}},
		{Ref: ref, Versions: []string{"1"}, ExpectedStatus: "Bogus"},
		{Ref: ref, Versions: []string{"1"}, VersionRevisions: map[string]string{"1": ""}},
		{Ref: ref, Versions: []string{"1"}, VersionRevisions: map[string]string{"bad version": "R"}},
	} {
		_, err := m.DisposePackageVersions(ctx, &in)
		requireAPIError(t, err, driver.ExValidation)
	}

	_, err := m.DisposePackageVersions(ctx, &driver.VersionsInput{Ref: pkgRef("repo", "ghost"), Versions: []string{"1"}})
	requireAPIError(t, err, driver.ExNotFound)
}
