package codeartifact_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/codeartifact"
	"github.com/stackshy/cloudemu/v2/services/codeartifact/driver"
)

func sha(data []byte) string {
	sum := sha256.Sum256(data)

	return hex.EncodeToString(sum[:])
}

func pkgRef(repo, name string) driver.PackageRef {
	return driver.PackageRef{Domain: "dom", Repository: repo, Format: driver.FormatGeneric, Namespace: "ns", Package: name}
}

// seeded returns a mock with domain "dom" and the given repositories.
func seeded(t *testing.T, opts *config.Options, repos ...string) *codeartifact.Mock {
	t.Helper()

	m := newMockWithOptions(opts)
	mustDomain(t, m, "dom")

	for _, r := range repos {
		mustRepo(t, m, "dom", r)
	}

	return m
}

func publishAsset(t *testing.T, m *codeartifact.Mock, ref driver.PackageRef, version, asset string, data []byte, unfinished bool) *driver.PublishResult {
	t.Helper()

	res, err := m.PublishPackageVersion(context.Background(), &driver.PublishInput{
		Ref: ref, Version: version, AssetName: asset, SHA256: sha(data), Content: data, Unfinished: unfinished,
	})
	requireNoError(t, err)

	return res
}

func TestPublishRoundTripBytes(t *testing.T) {
	m := seeded(t, config.NewOptions(), "repo")
	ref := pkgRef("repo", "pkg")
	data := []byte("hello codeartifact")

	res := publishAsset(t, m, ref, "1.0.0", "file.txt", data, false)

	if res.Status != driver.VersionStatusPublished || res.Revision == "" || res.Asset.Size != int64(len(data)) {
		t.Fatalf("publish result = %+v", res)
	}

	list, next, err := m.ListPackageVersionAssets(context.Background(), ref, "1.0.0", driver.Page{})
	requireNoError(t, err)

	if next != "" || len(list.Assets) != 1 || list.Assets[0].Name != "file.txt" {
		t.Fatalf("assets = %+v next=%q", list, next)
	}

	h := list.Assets[0].Hashes
	if h[driver.HashSHA256] != sha(data) || len(h[driver.HashMD5]) != 32 || len(h[driver.HashSHA1]) != 40 || len(h[driver.HashSHA512]) != 128 {
		t.Fatalf("hashes = %+v", h)
	}

	got, err := m.GetPackageVersionAsset(context.Background(), ref, "1.0.0", "file.txt", "")
	requireNoError(t, err)

	if !bytes.Equal(got.Content, data) || got.Version != "1.0.0" || got.Revision != res.Revision {
		t.Fatalf("asset = %+v", got)
	}

	d, err := m.DescribeDomain(context.Background(), "dom", "")
	requireNoError(t, err)

	if d.AssetSizeBytes != int64(len(data)) {
		t.Fatalf("assetSizeBytes = %d, want %d", d.AssetSizeBytes, len(data))
	}
}

func TestPublishHashMismatch(t *testing.T) {
	m := seeded(t, config.NewOptions(), "repo")

	_, err := m.PublishPackageVersion(context.Background(), &driver.PublishInput{
		Ref: pkgRef("repo", "pkg"), Version: "1", AssetName: "a", SHA256: sha([]byte("other")), Content: []byte("data"),
	})
	requireAPIError(t, err, driver.ExValidation)

	_, _, err = m.ListPackages(context.Background(), &driver.ListPackagesInput{Repository: driver.RepositoryRef{Domain: "dom", Repository: "repo"}})
	requireNoError(t, err)

	pkgs, _, _ := m.ListPackages(context.Background(), &driver.ListPackagesInput{Repository: driver.RepositoryRef{Domain: "dom", Repository: "repo"}})
	if len(pkgs) != 0 {
		t.Fatalf("rejected publish stored %d packages", len(pkgs))
	}
}

func TestPublishNonGenericRejected(t *testing.T) {
	m := seeded(t, config.NewOptions(), "repo")
	data := []byte("x")

	for _, f := range []string{"npm", "pypi", "maven", "nuget", "ruby", "swift", "cargo"} {
		ref := pkgRef("repo", "pkg")
		ref.Format = f

		_, err := m.PublishPackageVersion(context.Background(), &driver.PublishInput{
			Ref: ref, Version: "1", AssetName: "a", SHA256: sha(data), Content: data,
		})
		requireAPIError(t, err, driver.ExValidation)
	}

	ref := pkgRef("repo", "pkg")
	ref.Namespace = ""

	_, err := m.PublishPackageVersion(context.Background(), &driver.PublishInput{
		Ref: ref, Version: "1", AssetName: "a", SHA256: sha(data), Content: data,
	})
	requireAPIError(t, err, driver.ExValidation)
}

func TestPublishFieldValidation(t *testing.T) {
	m := seeded(t, config.NewOptions(), "repo")
	data := []byte("x")
	base := driver.PublishInput{Ref: pkgRef("repo", "pkg"), Version: "1", AssetName: "a", SHA256: sha(data), Content: data}

	bad := []driver.PublishInput{base, base, base, base}
	bad[0].Version = "has space"
	bad[1].AssetName = ""
	bad[2].SHA256 = "ABC"
	bad[3].Ref.Package = "a/b"

	for i := range bad {
		_, err := m.PublishPackageVersion(context.Background(), &bad[i])
		requireAPIError(t, err, driver.ExValidation)
	}
}

func TestPublishUnfinishedThenPublished(t *testing.T) {
	m := seeded(t, config.NewOptions(), "repo")
	ref := pkgRef("repo", "pkg")

	first := publishAsset(t, m, ref, "1", "a.txt", []byte("aaa"), true)
	if first.Status != driver.VersionStatusUnfinished {
		t.Fatalf("status = %q, want Unfinished", first.Status)
	}

	// Identical asset again on an Unfinished version is a no-op.
	again := publishAsset(t, m, ref, "1", "a.txt", []byte("aaa"), true)
	if again.Revision != first.Revision {
		t.Fatal("idempotent re-publish changed the revision")
	}

	_, err := m.PublishPackageVersion(context.Background(), &driver.PublishInput{
		Ref: ref, Version: "1", AssetName: "a.txt", SHA256: sha([]byte("bbb")), Content: []byte("bbb"), Unfinished: true,
	})
	requireAPIError(t, err, driver.ExConflict)

	last := publishAsset(t, m, ref, "1", "b.txt", []byte("bbb"), false)
	if last.Status != driver.VersionStatusPublished || last.Revision == first.Revision {
		t.Fatalf("final = %+v", last)
	}

	list, _, err := m.ListPackageVersionAssets(context.Background(), ref, "1", driver.Page{})
	requireNoError(t, err)

	if len(list.Assets) != 2 {
		t.Fatalf("assets = %d, want 2", len(list.Assets))
	}

	_, err = m.PublishPackageVersion(context.Background(), &driver.PublishInput{
		Ref: ref, Version: "1", AssetName: "c.txt", SHA256: sha([]byte("c")), Content: []byte("c"),
	})
	requireAPIError(t, err, driver.ExConflict)
}

func TestPublishLimits(t *testing.T) {
	m := seeded(t, config.NewOptions(), "repo")
	ref := pkgRef("repo", "pkg")

	const maxAssets = 350

	for i := 0; i < maxAssets; i++ {
		publishAsset(t, m, ref, "1", fmt.Sprintf("asset-%03d", i), []byte{byte(i), byte(i >> 8)}, true)
	}

	_, err := m.PublishPackageVersion(context.Background(), &driver.PublishInput{
		Ref: ref, Version: "1", AssetName: "one-too-many", SHA256: sha([]byte("z")), Content: []byte("z"), Unfinished: true,
	})
	requireAPIError(t, err, driver.ExServiceQuota)

	huge := make([]byte, 128<<20+1)

	_, err = m.PublishPackageVersion(context.Background(), &driver.PublishInput{
		Ref: pkgRef("repo", "big"), Version: "1", AssetName: "big.bin", SHA256: sha(huge), Content: huge,
	})
	requireAPIError(t, err, driver.ExServiceQuota)
}

func TestDomainAssetSizeDedupAndRevision(t *testing.T) {
	m := seeded(t, config.NewOptions(), "repo")
	ref := pkgRef("repo", "pkg")
	data := []byte("same bytes")

	v1 := publishAsset(t, m, ref, "1", "f", data, false)
	v2 := publishAsset(t, m, ref, "2", "f", data, false)
	other := publishAsset(t, m, ref, "3", "f", []byte("different"), false)

	d, err := m.DescribeDomain(context.Background(), "dom", "")
	requireNoError(t, err)

	if want := int64(len(data) + len("different")); d.AssetSizeBytes != want {
		t.Fatalf("assetSizeBytes = %d, want %d (identical bytes count once)", d.AssetSizeBytes, want)
	}

	if v1.Revision != v2.Revision || v1.Revision == other.Revision {
		t.Fatalf("revisions: %s %s %s", v1.Revision, v2.Revision, other.Revision)
	}

	if len(v1.Revision) != 32 {
		t.Fatalf("revision length = %d", len(v1.Revision))
	}
}

func TestPackageReadsStableAndFiltered(t *testing.T) {
	clk := config.NewFakeClock(time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC))
	m := seeded(t, config.NewOptions(config.WithClock(clk)), "repo")
	ctx := context.Background()

	publishAsset(t, m, pkgRef("repo", "zeta"), "1", "f", []byte("z"), false)
	clk.Advance(time.Hour)
	publishAsset(t, m, pkgRef("repo", "alpha"), "1", "f", []byte("a"), false)
	clk.Advance(time.Hour)
	publishAsset(t, m, pkgRef("repo", "alpha"), "2", "f", []byte("a2"), false)

	repo := driver.RepositoryRef{Domain: "dom", Repository: "repo"}

	pkgs, _, err := m.ListPackages(ctx, &driver.ListPackagesInput{Repository: repo})
	requireNoError(t, err)

	if len(pkgs) != 2 || pkgs[0].Name != "alpha" || pkgs[1].Name != "zeta" {
		t.Fatalf("packages = %+v", pkgs)
	}

	prefixed, _, err := m.ListPackages(ctx, &driver.ListPackagesInput{Repository: repo, Prefix: "ze", Format: "generic", Namespace: "n"})
	requireNoError(t, err)

	if len(prefixed) != 1 || prefixed[0].Name != "zeta" {
		t.Fatalf("filtered = %+v", prefixed)
	}

	none, _, err := m.ListPackages(ctx, &driver.ListPackagesInput{Repository: repo, Publish: driver.OriginBlock})
	requireNoError(t, err)

	if len(none) != 0 {
		t.Fatalf("publish=BLOCK filter matched %d packages", len(none))
	}

	versions, err := m.ListPackageVersions(ctx, &driver.ListPackageVersionsInput{Ref: pkgRef("repo", "alpha")})
	requireNoError(t, err)

	if len(versions.Versions) != 2 || versions.Versions[0].Version != "2" || versions.DefaultDisplayVersion != "2" {
		t.Fatalf("versions = %+v", versions)
	}

	d1, err := m.DescribePackageVersion(ctx, pkgRef("repo", "alpha"), "1")
	requireNoError(t, err)

	clk.Advance(48 * time.Hour)

	d2, err := m.DescribePackageVersion(ctx, pkgRef("repo", "alpha"), "1")
	requireNoError(t, err)

	if !d1.PublishedTime.Equal(d2.PublishedTime) || d1.Revision != d2.Revision || d1.Origin.RepositoryName != "repo" {
		t.Fatalf("describe drifted: %+v vs %+v", d1, d2)
	}
}

func TestPackageReadErrors(t *testing.T) {
	m := seeded(t, config.NewOptions(), "repo")
	ctx := context.Background()
	publishAsset(t, m, pkgRef("repo", "pkg"), "1", "f", []byte("x"), false)

	_, err := m.DescribePackage(ctx, pkgRef("repo", "ghost"))
	requireAPIError(t, err, driver.ExNotFound)

	_, err = m.DescribePackageVersion(ctx, pkgRef("repo", "pkg"), "9")
	requireAPIError(t, err, driver.ExNotFound)

	_, err = m.GetPackageVersionAsset(ctx, pkgRef("repo", "pkg"), "1", "missing", "")
	requireAPIError(t, err, driver.ExNotFound)

	_, err = m.GetPackageVersionAsset(ctx, pkgRef("repo", "pkg"), "1", "f", "WRONGREVISION")
	requireAPIError(t, err, driver.ExNotFound)

	bad := pkgRef("repo", "pkg")
	bad.Format = "docker"

	_, err = m.DescribePackage(ctx, bad)
	requireAPIError(t, err, driver.ExValidation)

	_, err = m.ListPackageVersions(ctx, &driver.ListPackageVersionsInput{Ref: pkgRef("repo", "pkg"), Status: "Nope"})
	requireAPIError(t, err, driver.ExValidation)

	_, _, err = m.ListPackages(ctx, &driver.ListPackagesInput{Repository: driver.RepositoryRef{Domain: "dom", Repository: "ghost"}})
	requireAPIError(t, err, driver.ExNotFound)
}

func TestReadmeAndDependenciesGeneric(t *testing.T) {
	m := seeded(t, config.NewOptions(), "repo")
	ref := pkgRef("repo", "pkg")
	res := publishAsset(t, m, ref, "1", "f", []byte("x"), false)

	for name, call := range map[string]func() (*driver.VersionIdentity, error){
		"readme": func() (*driver.VersionIdentity, error) {
			return m.GetPackageVersionReadme(context.Background(), ref, "1")
		},
		"dependencies": func() (*driver.VersionIdentity, error) {
			return m.ListPackageVersionDependencies(context.Background(), ref, "1")
		},
	} {
		id, err := call()
		requireNoError(t, err)

		if id.Format != "generic" || id.Namespace != "ns" || id.Package != "pkg" || id.Version != "1" || id.Revision != res.Revision {
			t.Fatalf("%s identity = %+v", name, id)
		}
	}

	_, err := m.GetPackageVersionReadme(context.Background(), ref, "9")
	requireAPIError(t, err, driver.ExNotFound)
}

func TestUpdateStatusMatrix(t *testing.T) {
	m := seeded(t, config.NewOptions(), "repo")
	ref := pkgRef("repo", "pkg")
	ctx := context.Background()

	publishAsset(t, m, ref, "1", "f", []byte("1"), false)
	publishAsset(t, m, ref, "2", "f", []byte("2"), true)

	update := func(target string, versions ...string) *driver.VersionsResult {
		res, err := m.UpdatePackageVersionsStatus(ctx, &driver.UpdateStatusInput{
			VersionsInput: driver.VersionsInput{Ref: ref, Versions: versions}, TargetStatus: target,
		})
		requireNoError(t, err)

		return res
	}

	for _, target := range []string{"Archived", "Unlisted", "Published"} {
		res := update(target, "1")
		if res.Successful["1"].Status != target || len(res.Failed) != 0 {
			t.Fatalf("to %s: %+v", target, res)
		}
	}

	if res := update("Published", "1"); res.Failed["1"].ErrorCode != driver.VersionErrSkipped {
		t.Fatalf("same status = %+v", res)
	}

	if res := update("Published", "2"); res.Successful["2"].Status != driver.VersionStatusPublished {
		t.Fatalf("Unfinished -> Published = %+v", res)
	}

	if res := update("Archived", "ghost"); res.Failed["ghost"].ErrorCode != driver.VersionErrNotFound {
		t.Fatalf("missing = %+v", res)
	}

	for _, bad := range []string{"Unfinished", "Disposed", "Deleted", "Bogus"} {
		_, err := m.UpdatePackageVersionsStatus(ctx, &driver.UpdateStatusInput{
			VersionsInput: driver.VersionsInput{Ref: ref, Versions: []string{"1"}}, TargetStatus: bad,
		})
		requireAPIError(t, err, driver.ExValidation)
	}

	_, err := m.DisposePackageVersions(ctx, &driver.VersionsInput{Ref: ref, Versions: []string{"1"}})
	requireNoError(t, err)

	if res := update("Published", "1"); res.Failed["1"].ErrorCode != driver.VersionErrNotAllowed {
		t.Fatalf("disposed -> Published = %+v", res)
	}
}

func TestUpdateStatusExpectedGuards(t *testing.T) {
	m := seeded(t, config.NewOptions(), "repo")
	ref := pkgRef("repo", "pkg")
	ctx := context.Background()
	res := publishAsset(t, m, ref, "1", "f", []byte("1"), false)

	out, err := m.UpdatePackageVersionsStatus(ctx, &driver.UpdateStatusInput{
		VersionsInput: driver.VersionsInput{Ref: ref, Versions: []string{"1"}, ExpectedStatus: "Archived"}, TargetStatus: "Unlisted",
	})
	requireNoError(t, err)

	if out.Failed["1"].ErrorCode != driver.VersionErrMismatchedStatus {
		t.Fatalf("expectedStatus mismatch = %+v", out)
	}

	out, err = m.UpdatePackageVersionsStatus(ctx, &driver.UpdateStatusInput{
		VersionsInput: driver.VersionsInput{Ref: ref, Versions: []string{"1"}, VersionRevisions: map[string]string{"1": "OLDREV"}}, TargetStatus: "Unlisted",
	})
	requireNoError(t, err)

	if out.Failed["1"].ErrorCode != driver.VersionErrMismatchedRev {
		t.Fatalf("revision mismatch = %+v", out)
	}

	out, err = m.UpdatePackageVersionsStatus(ctx, &driver.UpdateStatusInput{
		VersionsInput: driver.VersionsInput{Ref: ref, Versions: []string{"1"}, ExpectedStatus: "Published", VersionRevisions: map[string]string{"1": res.Revision}},
		TargetStatus:  "Unlisted",
	})
	requireNoError(t, err)

	if got := out.Successful["1"]; got.Status != "Unlisted" || got.Revision != res.Revision {
		t.Fatalf("success = %+v", out)
	}
}

func TestDisposeDeletesAssets(t *testing.T) {
	m := seeded(t, config.NewOptions(), "repo")
	ref := pkgRef("repo", "pkg")
	ctx := context.Background()
	before := publishAsset(t, m, ref, "1", "f", []byte("payload"), false)

	res, err := m.DisposePackageVersions(ctx, &driver.VersionsInput{Ref: ref, Versions: []string{"1", "2"}})
	requireNoError(t, err)

	if res.Successful["1"].Status != driver.VersionStatusDisposed || res.Failed["2"].ErrorCode != driver.VersionErrNotFound {
		t.Fatalf("dispose = %+v", res)
	}

	if res.Successful["1"].Revision == before.Revision {
		t.Fatal("revision must change when assets are removed")
	}

	v, err := m.DescribePackageVersion(ctx, ref, "1")
	requireNoError(t, err)

	if v.Status != driver.VersionStatusDisposed {
		t.Fatalf("status = %q", v.Status)
	}

	_, err = m.GetPackageVersionAsset(ctx, ref, "1", "f", "")
	requireAPIError(t, err, driver.ExNotFound)

	d, err := m.DescribeDomain(ctx, "dom", "")
	requireNoError(t, err)

	if d.AssetSizeBytes != 0 {
		t.Fatalf("assetSizeBytes after dispose = %d", d.AssetSizeBytes)
	}

	again, err := m.DisposePackageVersions(ctx, &driver.VersionsInput{Ref: ref, Versions: []string{"1"}})
	requireNoError(t, err)

	if again.Failed["1"].ErrorCode != driver.VersionErrSkipped {
		t.Fatalf("second dispose = %+v", again)
	}
}

func TestArchivedVersionCannotBeDownloaded(t *testing.T) {
	m := seeded(t, config.NewOptions(), "repo")
	ref := pkgRef("repo", "pkg")
	publishAsset(t, m, ref, "1", "f", []byte("x"), false)

	_, err := m.UpdatePackageVersionsStatus(context.Background(), &driver.UpdateStatusInput{
		VersionsInput: driver.VersionsInput{Ref: ref, Versions: []string{"1"}}, TargetStatus: "Archived",
	})
	requireNoError(t, err)

	_, err = m.GetPackageVersionAsset(context.Background(), ref, "1", "f", "")
	requireAPIError(t, err, driver.ExConflict)
}

func TestDeleteVersionsAndPackage(t *testing.T) {
	m := seeded(t, config.NewOptions(), "repo")
	ref := pkgRef("repo", "pkg")
	ctx := context.Background()

	publishAsset(t, m, ref, "1", "f", []byte("1"), false)
	publishAsset(t, m, ref, "2", "f", []byte("2"), false)

	res, err := m.DeletePackageVersions(ctx, &driver.VersionsInput{Ref: ref, Versions: []string{"1", "9"}, ExpectedStatus: "Published"})
	requireNoError(t, err)

	if res.Successful["1"].Status != driver.VersionStatusDeleted || res.Failed["9"].ErrorCode != driver.VersionErrNotFound {
		t.Fatalf("delete = %+v", res)
	}

	mis, err := m.DeletePackageVersions(ctx, &driver.VersionsInput{Ref: ref, Versions: []string{"2"}, ExpectedStatus: "Archived"})
	requireNoError(t, err)

	if mis.Failed["2"].ErrorCode != driver.VersionErrMismatchedStatus {
		t.Fatalf("expectedStatus = %+v", mis)
	}

	_, err = m.DeletePackageVersions(ctx, &driver.VersionsInput{Ref: ref, Versions: []string{"2"}})
	requireNoError(t, err)

	// The last version is gone and no origin config was set: the package is gone.
	_, err = m.DescribePackage(ctx, ref)
	requireAPIError(t, err, driver.ExNotFound)

	publishAsset(t, m, ref, "1", "f", []byte("1"), false)

	del, err := m.DeletePackage(ctx, ref)
	requireNoError(t, err)

	if del.Name != "pkg" || del.Format != "generic" {
		t.Fatalf("deleted = %+v", del)
	}

	_, err = m.DeletePackage(ctx, ref)
	requireAPIError(t, err, driver.ExNotFound)

	_, err = m.DeletePackageVersions(ctx, &driver.VersionsInput{Ref: ref, Versions: make([]string, 101)})
	requireAPIError(t, err, driver.ExValidation)
}

func TestCopyPackageVersions(t *testing.T) {
	m := seeded(t, config.NewOptions(), "src", "dst")
	ctx := context.Background()
	ref := pkgRef("src", "pkg")
	one := publishAsset(t, m, ref, "1", "f", []byte("one"), false)
	publishAsset(t, m, ref, "2", "f", []byte("two"), false)

	copyIn := func(in driver.CopyInput) *driver.VersionsResult {
		in.Ref = ref
		in.SourceRepository, in.DestinationRepository = "src", "dst"

		res, err := m.CopyPackageVersions(ctx, &in)
		requireNoError(t, err)

		return res
	}

	res := copyIn(driver.CopyInput{Versions: []string{"1", "9"}})
	if res.Successful["1"].Revision != one.Revision || res.Failed["9"].ErrorCode != driver.VersionErrNotFound {
		t.Fatalf("copy = %+v", res)
	}

	got, err := m.GetPackageVersionAsset(ctx, pkgRef("dst", "pkg"), "1", "f", "")
	requireNoError(t, err)

	if string(got.Content) != "one" {
		t.Fatalf("copied bytes = %q", got.Content)
	}

	if again := copyIn(driver.CopyInput{Versions: []string{"1"}}); again.Failed["1"].ErrorCode != driver.VersionErrAlreadyExists {
		t.Fatalf("re-copy = %+v", again)
	}

	if over := copyIn(driver.CopyInput{Versions: []string{"1"}, AllowOverwrite: true}); over.Successful["1"].Revision != one.Revision {
		t.Fatalf("overwrite = %+v", over)
	}

	if pinned := copyIn(driver.CopyInput{VersionRevisions: map[string]string{"2": "WRONG"}}); pinned.Failed["2"].ErrorCode != driver.VersionErrMismatchedRev {
		t.Fatalf("pinned = %+v", pinned)
	}

	bad := []driver.CopyInput{
		{Ref: ref, SourceRepository: "src", DestinationRepository: "src", Versions: []string{"1"}},
		{Ref: ref, SourceRepository: "src", DestinationRepository: "dst"},
		{Ref: ref, SourceRepository: "src", DestinationRepository: "dst", Versions: []string{"1"}, VersionRevisions: map[string]string{"1": "R"}},
		{Ref: ref, SourceRepository: "src", DestinationRepository: "dst", Versions: make([]string, 101)},
	}

	for i := range bad {
		_, err = m.CopyPackageVersions(ctx, &bad[i])
		requireAPIError(t, err, driver.ExValidation)
	}

	_, err = m.CopyPackageVersions(ctx, &driver.CopyInput{Ref: ref, SourceRepository: "src", DestinationRepository: "ghost", Versions: []string{"1"}})
	requireAPIError(t, err, driver.ExNotFound)
}

func TestCopyDisposedNotAllowed(t *testing.T) {
	m := seeded(t, config.NewOptions(), "src", "dst")
	ref := pkgRef("src", "pkg")
	publishAsset(t, m, ref, "1", "f", []byte("one"), false)

	_, err := m.DisposePackageVersions(context.Background(), &driver.VersionsInput{Ref: ref, Versions: []string{"1"}})
	requireNoError(t, err)

	res, err := m.CopyPackageVersions(context.Background(), &driver.CopyInput{
		Ref: ref, SourceRepository: "src", DestinationRepository: "dst", Versions: []string{"1"},
	})
	requireNoError(t, err)

	if res.Failed["1"].ErrorCode != driver.VersionErrNotAllowed {
		t.Fatalf("copy of disposed = %+v", res)
	}
}

func TestCopyIncludeFromUpstream(t *testing.T) {
	m := seeded(t, config.NewOptions(), "base")
	requireNoError(t, createRepoWithUpstreams(m, "src", "base"))
	mustRepo(t, m, "dom", "dst")
	ctx := context.Background()

	publishAsset(t, m, pkgRef("base", "pkg"), "1", "f", []byte("from base"), false)

	in := driver.CopyInput{Ref: pkgRef("src", "pkg"), SourceRepository: "src", DestinationRepository: "dst", Versions: []string{"1"}}

	res, err := m.CopyPackageVersions(ctx, &in)
	requireNoError(t, err)

	if res.Failed["1"].ErrorCode != driver.VersionErrNotFound {
		t.Fatalf("without includeFromUpstream = %+v", res)
	}

	in.IncludeFromUpstream = true

	res, err = m.CopyPackageVersions(ctx, &in)
	requireNoError(t, err)

	if _, ok := res.Successful["1"]; !ok {
		t.Fatalf("with includeFromUpstream = %+v", res)
	}

	got, err := m.GetPackageVersionAsset(ctx, pkgRef("dst", "pkg"), "1", "f", "")
	requireNoError(t, err)

	if string(got.Content) != "from base" {
		t.Fatalf("copied bytes = %q", got.Content)
	}
}

func TestPublishConflictsWithUpstreamVersion(t *testing.T) {
	m := seeded(t, config.NewOptions(), "base")
	requireNoError(t, createRepoWithUpstreams(m, "down", "base"))
	ctx := context.Background()

	publishAsset(t, m, pkgRef("base", "pkg"), "1", "f", []byte("base"), false)
	publishAsset(t, m, pkgRef("down", "pkg"), "2", "f", []byte("two"), false)

	_, err := m.PutPackageOriginConfiguration(ctx, pkgRef("down", "pkg"), driver.OriginRestrictions{Publish: "ALLOW", Upstream: "ALLOW"})
	requireNoError(t, err)

	_, err = m.PublishPackageVersion(ctx, &driver.PublishInput{
		Ref: pkgRef("down", "pkg"), Version: "1", AssetName: "f", SHA256: sha([]byte("x")), Content: []byte("x"),
	})
	requireAPIError(t, err, driver.ExConflict)

	publishAsset(t, m, pkgRef("down", "pkg"), "3", "f", []byte("three"), false)
}

func TestAssetAndVersionPagination(t *testing.T) {
	m := seeded(t, config.NewOptions(), "repo")
	ref := pkgRef("repo", "pkg")
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		publishAsset(t, m, ref, fmt.Sprintf("v%d", i), "f", []byte{byte(i)}, false)
		publishAsset(t, m, ref, "multi", fmt.Sprintf("asset-%d", i), []byte{byte(i), 9}, true)
	}

	var seen []string

	token := ""

	for {
		list, next, err := m.ListPackageVersionAssets(ctx, ref, "multi", driver.Page{MaxResults: 1, NextToken: token})
		requireNoError(t, err)

		for _, a := range list.Assets {
			seen = append(seen, a.Name)
		}

		if next == "" {
			break
		}

		token = next
	}

	if len(seen) != 5 || seen[0] != "asset-0" || seen[4] != "asset-4" {
		t.Fatalf("asset walk = %v", seen)
	}

	_, _, err := m.ListPackageVersionAssets(ctx, ref, "multi", driver.Page{NextToken: "garbage"})
	requireAPIError(t, err, driver.ExValidation)

	all, err := m.ListPackageVersions(ctx, &driver.ListPackageVersionsInput{Ref: ref, Page: driver.Page{MaxResults: 2}})
	requireNoError(t, err)

	if len(all.Versions) != 2 || all.NextToken == "" {
		t.Fatalf("first page = %+v", all)
	}
}
