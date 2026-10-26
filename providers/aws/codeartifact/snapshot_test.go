package codeartifact_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/codeartifact"
	"github.com/stackshy/cloudemu/v2/services/codeartifact/driver"
)

// richState creates one of everything a snapshot has to carry.
func richState(t *testing.T) *codeartifact.Mock {
	t.Helper()

	m := seeded(t, config.NewOptions(), "repo", "other")
	ctx := context.Background()
	dom := driver.DomainRef{Domain: "dom"}

	_, err := m.PutDomainPermissionsPolicy(ctx, dom, "", testPolicy)
	requireNoError(t, err)

	_, err = m.PutRepositoryPermissionsPolicy(ctx, driver.RepositoryRef{Domain: "dom", Repository: "repo"}, "", testPolicy)
	requireNoError(t, err)

	ref := pkgRef("repo", "pkg")
	publishAsset(t, m, ref, "1", "f.bin", []byte("snapshot bytes"), false)
	publishAsset(t, m, ref, "2", "g.bin", []byte("unfinished"), true)

	_, err = m.PutPackageOriginConfiguration(ctx, ref, driver.OriginRestrictions{Publish: "ALLOW", Upstream: "ALLOW"})
	requireNoError(t, err)

	createGroup(t, m, "/generic/ns/*")

	_, err = m.UpdatePackageGroupOriginConfiguration(ctx, &driver.UpdateGroupOriginInput{
		Domain: dom, Pattern: "/generic/ns/*", Restrictions: map[string]string{driver.RestrictionPublish: driver.ModeAllowSpecificRepositories},
		AddAllowed: []driver.AllowedRepository{{RepositoryName: "repo", OriginRestrictionType: driver.RestrictionPublish}},
	})
	requireNoError(t, err)

	return m
}

// observe renders what a client can see of the rich state, as comparable JSON.
func observe(t *testing.T, m *codeartifact.Mock) string {
	t.Helper()

	ctx := context.Background()
	ref := pkgRef("repo", "pkg")

	d, err := m.DescribeDomain(ctx, "dom", "")
	requireNoError(t, err)

	pol, err := m.GetRepositoryPermissionsPolicy(ctx, driver.RepositoryRef{Domain: "dom", Repository: "repo"})
	requireNoError(t, err)

	v1, err := m.DescribePackageVersion(ctx, ref, "1")
	requireNoError(t, err)

	v2, err := m.DescribePackageVersion(ctx, ref, "2")
	requireNoError(t, err)

	pkg, err := m.DescribePackage(ctx, ref)
	requireNoError(t, err)

	g, err := m.DescribePackageGroup(ctx, driver.DomainRef{Domain: "dom"}, "/generic/ns/*")
	requireNoError(t, err)

	assets, _, err := m.ListPackageVersionAssets(ctx, ref, "1", driver.Page{})
	requireNoError(t, err)

	raw, err := json.Marshal([]any{d, pol, v1, v2, pkg, g, assets})
	requireNoError(t, err)

	return string(raw)
}

func TestSnapshotRoundTripAllState(t *testing.T) {
	src := richState(t)
	ctx := context.Background()
	before := observe(t, src)

	data, err := src.Snapshot(ctx, true)
	requireNoError(t, err)

	dst := newMock()
	requireNoError(t, dst.Restore(ctx, data))

	if after := observe(t, dst); after != before {
		t.Fatalf("restored state differs\n before: %s\n after:  %s", before, after)
	}

	got, err := dst.GetPackageVersionAsset(ctx, pkgRef("repo", "pkg"), "1", "f.bin", "")
	requireNoError(t, err)

	if string(got.Content) != "snapshot bytes" {
		t.Fatalf("restored bytes = %q", got.Content)
	}

	// The event sequence continues after a restore instead of restarting.
	pub := &recordingPublisher{}
	dst.SetEventPublisher(pub)
	publishAsset(t, dst, pkgRef("repo", "pkg"), "3", "h", []byte("h"), false)

	if evs := pub.all(); len(evs) != 1 || evs[0].detail["sequenceNumber"].(float64) <= 2 {
		t.Fatalf("sequence did not continue: %+v", evs)
	}

	// The restored mock keeps enforcing the same rules.
	_, err = dst.CreateDomain(ctx, &driver.CreateDomainInput{Name: "dom"})
	requireAPIError(t, err, driver.ExConflict)
}

func TestSnapshotWithoutAssetsKeepsMetadata(t *testing.T) {
	src := richState(t)
	ctx := context.Background()

	data, err := src.Snapshot(ctx, false)
	requireNoError(t, err)

	dst := newMock()
	requireNoError(t, dst.Restore(ctx, data))

	ref := pkgRef("repo", "pkg")

	list, _, err := dst.ListPackageVersionAssets(ctx, ref, "1", driver.Page{})
	requireNoError(t, err)

	if len(list.Assets) != 1 || list.Assets[0].Size != int64(len("snapshot bytes")) || list.Assets[0].Hashes[driver.HashSHA256] == "" {
		t.Fatalf("assets after metadata-only restore = %+v", list.Assets)
	}

	_, err = dst.GetPackageVersionAsset(ctx, ref, "1", "f.bin", "")
	requireAPIError(t, err, driver.ExNotFound)

	// The source still has its bytes: stripping happens on a copy.
	if _, err = src.GetPackageVersionAsset(ctx, ref, "1", "f.bin", ""); err != nil {
		t.Fatalf("snapshot without assets damaged the source: %v", err)
	}
}

func TestSnapshotFromBeforePackagesLoads(t *testing.T) {
	legacy := []byte(`{"domains":{"dom":{"Name":"dom","Owner":"123456789012","Arn":"arn:aws:codeartifact:us-east-1:123456789012:domain/dom","Status":"Active"}},` +
		`"repositories":{"dom/repo":{"Name":"repo","DomainName":"dom","DomainOwner":"123456789012","AdministratorAccount":"123456789012","Arn":"arn:aws:codeartifact:us-east-1:123456789012:repository/dom/repo"}}}`)

	m := newMock()
	requireNoError(t, m.Restore(context.Background(), legacy))

	d, err := m.DescribeDomain(context.Background(), "dom", "")
	requireNoError(t, err)

	if d.RepositoryCount != 1 || d.AssetSizeBytes != 0 {
		t.Fatalf("legacy domain = %+v", d)
	}

	pkgs, _, err := m.ListPackages(context.Background(), &driver.ListPackagesInput{Repository: driver.RepositoryRef{Domain: "dom", Repository: "repo"}})
	requireNoError(t, err)

	if len(pkgs) != 0 {
		t.Fatalf("legacy snapshot restored %d packages", len(pkgs))
	}

	publishAsset(t, m, pkgRef("repo", "pkg"), "1", "f", []byte("x"), false)
}
