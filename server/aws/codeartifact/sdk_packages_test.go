package codeartifact_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	ca "github.com/aws/aws-sdk-go-v2/service/codeartifact"
	catypes "github.com/aws/aws-sdk-go-v2/service/codeartifact/types"
)

func TestSDKPublishAndGetAsset(t *testing.T) {
	e := newEnv(t)
	e.seed(t, "repo")

	ctx := context.Background()
	data := []byte("bytes through the real SDK \x00\x01\xff")

	out := e.publish(t, "repo", "pkg", "1.0.0", "bin.dat", data, false)

	if out.Status != catypes.PackageVersionStatusPublished || aws.ToString(out.VersionRevision) == "" ||
		aws.ToString(out.Asset.Name) != "bin.dat" || aws.ToInt64(out.Asset.Size) != int64(len(data)) ||
		out.Asset.Hashes[string(catypes.HashAlgorithmSha256)] != sha256Hex(data) {
		t.Fatalf("publish = %+v asset=%+v", out, out.Asset)
	}

	domain, repo, format, ns, name := pkgInput("repo", "pkg")

	got, err := e.c.GetPackageVersionAsset(ctx, &ca.GetPackageVersionAssetInput{
		Domain: domain, Repository: repo, Format: format, Namespace: ns, Package: name,
		PackageVersion: aws.String("1.0.0"), Asset: aws.String("bin.dat"),
	})
	if err != nil {
		t.Fatalf("GetPackageVersionAsset: %v", err)
	}

	if aws.ToString(got.AssetName) != "bin.dat" || aws.ToString(got.PackageVersion) != "1.0.0" ||
		aws.ToString(got.PackageVersionRevision) != aws.ToString(out.VersionRevision) {
		t.Fatalf("headers: %q %q %q", aws.ToString(got.AssetName), aws.ToString(got.PackageVersion), aws.ToString(got.PackageVersionRevision))
	}

	if body := readAll(t, got.Asset); !bytes.Equal(body, data) {
		t.Fatalf("downloaded %d bytes, want %d identical bytes", len(body), len(data))
	}

	_, err = e.c.PublishPackageVersion(ctx, &ca.PublishPackageVersionInput{
		Domain: domain, Repository: repo, Format: format, Namespace: ns, Package: name, PackageVersion: aws.String("2"),
		AssetName: aws.String("a"), AssetSHA256: aws.String(sha256Hex([]byte("other"))), AssetContent: bytes.NewReader(data),
	})
	requireAPICode(t, err, "ValidationException", 400)

	_, err = e.c.GetPackageVersionAsset(ctx, &ca.GetPackageVersionAssetInput{
		Domain: domain, Repository: repo, Format: format, Namespace: ns, Package: name,
		PackageVersion: aws.String("1.0.0"), Asset: aws.String("missing"),
	})
	requireAPICode(t, err, "ResourceNotFoundException", 404)

	_, err = e.c.PublishPackageVersion(ctx, &ca.PublishPackageVersionInput{
		Domain: domain, Repository: repo, Format: catypes.PackageFormatNpm, Package: name, PackageVersion: aws.String("1"),
		AssetName: aws.String("a"), AssetSHA256: aws.String(sha256Hex(data)), AssetContent: bytes.NewReader(data),
	})
	requireAPICode(t, err, "ValidationException", 400)
}

func TestSDKPackageReadsAndStatus(t *testing.T) {
	e := newEnv(t)
	e.seed(t, "src", "dst")

	ctx := context.Background()
	domain, repo, format, ns, name := pkgInput("src", "pkg")

	rev := aws.ToString(e.publish(t, "src", "pkg", "1", "a.txt", []byte("one"), false).VersionRevision)
	e.publish(t, "src", "pkg", "2", "b.txt", []byte("two"), true)

	pkgs, err := e.c.ListPackages(ctx, &ca.ListPackagesInput{Domain: domain, Repository: repo})
	if err != nil || len(pkgs.Packages) != 1 || aws.ToString(pkgs.Packages[0].Package) != "pkg" ||
		pkgs.Packages[0].OriginConfiguration.Restrictions.Publish != catypes.AllowPublishAllow {
		t.Fatalf("ListPackages = %+v err=%v", pkgs, err)
	}

	desc, err := e.c.DescribePackage(ctx, &ca.DescribePackageInput{Domain: domain, Repository: repo, Format: format, Namespace: ns, Package: name})
	if err != nil || aws.ToString(desc.Package.Name) != "pkg" || aws.ToString(desc.Package.Namespace) != "ns" {
		t.Fatalf("DescribePackage = %+v err=%v", desc, err)
	}

	vers, err := e.c.ListPackageVersions(ctx, &ca.ListPackageVersionsInput{Domain: domain, Repository: repo, Format: format, Namespace: ns, Package: name})
	if err != nil || len(vers.Versions) != 2 || aws.ToString(vers.DefaultDisplayVersion) != "1" {
		t.Fatalf("ListPackageVersions = %+v err=%v", vers, err)
	}

	pv, err := e.c.DescribePackageVersion(ctx, &ca.DescribePackageVersionInput{
		Domain: domain, Repository: repo, Format: format, Namespace: ns, Package: name, PackageVersion: aws.String("1"),
	})
	if err != nil || pv.PackageVersion.Status != catypes.PackageVersionStatusPublished || pv.PackageVersion.PublishedTime == nil ||
		aws.ToString(pv.PackageVersion.Revision) != rev || pv.PackageVersion.Origin.OriginType != catypes.PackageVersionOriginTypeInternal {
		t.Fatalf("DescribePackageVersion = %+v err=%v", pv, err)
	}

	assets, err := e.c.ListPackageVersionAssets(ctx, &ca.ListPackageVersionAssetsInput{
		Domain: domain, Repository: repo, Format: format, Namespace: ns, Package: name, PackageVersion: aws.String("1"),
	})
	if err != nil || len(assets.Assets) != 1 || aws.ToString(assets.Assets[0].Name) != "a.txt" {
		t.Fatalf("ListPackageVersionAssets = %+v err=%v", assets, err)
	}

	readme, err := e.c.GetPackageVersionReadme(ctx, &ca.GetPackageVersionReadmeInput{
		Domain: domain, Repository: repo, Format: format, Namespace: ns, Package: name, PackageVersion: aws.String("1"),
	})
	if err != nil || aws.ToString(readme.Version) != "1" || aws.ToString(readme.VersionRevision) != rev {
		t.Fatalf("GetPackageVersionReadme = %+v err=%v", readme, err)
	}

	deps, err := e.c.ListPackageVersionDependencies(ctx, &ca.ListPackageVersionDependenciesInput{
		Domain: domain, Repository: repo, Format: format, Namespace: ns, Package: name, PackageVersion: aws.String("1"),
	})
	if err != nil || len(deps.Dependencies) != 0 || aws.ToString(deps.Package) != "pkg" {
		t.Fatalf("ListPackageVersionDependencies = %+v err=%v", deps, err)
	}

	st, err := e.c.UpdatePackageVersionsStatus(ctx, &ca.UpdatePackageVersionsStatusInput{
		Domain: domain, Repository: repo, Format: format, Namespace: ns, Package: name,
		Versions: []string{"1", "9"}, TargetStatus: catypes.PackageVersionStatusArchived,
		ExpectedStatus: catypes.PackageVersionStatusPublished,
	})
	if err != nil || st.SuccessfulVersions["1"].Status != catypes.PackageVersionStatusArchived ||
		st.FailedVersions["9"].ErrorCode != catypes.PackageVersionErrorCodeNotFound {
		t.Fatalf("UpdatePackageVersionsStatus = %+v err=%v", st, err)
	}

	cp, err := e.c.CopyPackageVersions(ctx, &ca.CopyPackageVersionsInput{
		Domain: domain, Format: format, Namespace: ns, Package: name,
		SourceRepository: aws.String("src"), DestinationRepository: aws.String("dst"), Versions: []string{"1", "2"},
	})
	if err != nil || len(cp.SuccessfulVersions) != 2 || len(cp.FailedVersions) != 0 {
		t.Fatalf("CopyPackageVersions = %+v err=%v", cp, err)
	}

	again, err := e.c.CopyPackageVersions(ctx, &ca.CopyPackageVersionsInput{
		Domain: domain, Format: format, Namespace: ns, Package: name,
		SourceRepository: aws.String("src"), DestinationRepository: aws.String("dst"), Versions: []string{"1"},
	})
	if err != nil || again.FailedVersions["1"].ErrorCode != catypes.PackageVersionErrorCodeAlreadyExists {
		t.Fatalf("second copy = %+v err=%v", again, err)
	}

	dis, err := e.c.DisposePackageVersions(ctx, &ca.DisposePackageVersionsInput{
		Domain: domain, Repository: repo, Format: format, Namespace: ns, Package: name, Versions: []string{"2"},
	})
	if err != nil || dis.SuccessfulVersions["2"].Status != catypes.PackageVersionStatusDisposed {
		t.Fatalf("DisposePackageVersions = %+v err=%v", dis, err)
	}

	del, err := e.c.DeletePackageVersions(ctx, &ca.DeletePackageVersionsInput{
		Domain: domain, Repository: repo, Format: format, Namespace: ns, Package: name, Versions: []string{"1"},
	})
	if err != nil || del.SuccessfulVersions["1"].Status != catypes.PackageVersionStatusDeleted {
		t.Fatalf("DeletePackageVersions = %+v err=%v", del, err)
	}

	dp, err := e.c.DeletePackage(ctx, &ca.DeletePackageInput{Domain: domain, Repository: repo, Format: format, Namespace: ns, Package: name})
	if err != nil || aws.ToString(dp.DeletedPackage.Package) != "pkg" {
		t.Fatalf("DeletePackage = %+v err=%v", dp, err)
	}

	_, err = e.c.DescribePackage(ctx, &ca.DescribePackageInput{Domain: domain, Repository: repo, Format: format, Namespace: ns, Package: name})
	requireAPICode(t, err, "ResourceNotFoundException", 404)
}

func TestSDKPackagePagination(t *testing.T) {
	e := newEnv(t)
	e.seed(t, "repo")

	ctx := context.Background()
	domain, repo, format, ns, name := pkgInput("repo", "pkg")

	for _, v := range []string{"1", "2", "3"} {
		e.publish(t, "repo", "pkg", v, "f", []byte(v), false)
	}

	var seen []string

	token := (*string)(nil)

	for {
		out, err := e.c.ListPackageVersions(ctx, &ca.ListPackageVersionsInput{
			Domain: domain, Repository: repo, Format: format, Namespace: ns, Package: name, MaxResults: aws.Int32(1), NextToken: token,
		})
		if err != nil {
			t.Fatal(err)
		}

		for _, v := range out.Versions {
			seen = append(seen, aws.ToString(v.Version))
		}

		if out.NextToken == nil {
			break
		}

		token = out.NextToken
	}

	if len(seen) != 3 {
		t.Fatalf("walked %v", seen)
	}

	_, err := e.c.ListPackageVersions(ctx, &ca.ListPackageVersionsInput{
		Domain: domain, Repository: repo, Format: format, Namespace: ns, Package: name, NextToken: aws.String("garbage"),
	})
	requireAPICode(t, err, "ValidationException", 400)

	_, err = e.c.ListPackageVersions(ctx, &ca.ListPackageVersionsInput{
		Domain: domain, Repository: repo, Format: format, Namespace: ns, Package: name, MaxResults: aws.Int32(5000),
	})
	requireAPICode(t, err, "ValidationException", 400)
}

func TestSDKOriginAndGroups(t *testing.T) {
	e := newEnv(t)
	e.seed(t, "repo-a", "repo-b")

	ctx := context.Background()
	domain, repo, format, ns, name := pkgInput("repo-a", "pkg")

	put, err := e.c.PutPackageOriginConfiguration(ctx, &ca.PutPackageOriginConfigurationInput{
		Domain: domain, Repository: repo, Format: format, Namespace: ns, Package: name,
		Restrictions: &catypes.PackageOriginRestrictions{Publish: catypes.AllowPublishBlock, Upstream: catypes.AllowUpstreamAllow},
	})
	if err != nil || put.OriginConfiguration.Restrictions.Publish != catypes.AllowPublishBlock {
		t.Fatalf("PutPackageOriginConfiguration = %+v err=%v", put, err)
	}

	data := []byte("x")

	_, err = e.c.PublishPackageVersion(ctx, &ca.PublishPackageVersionInput{
		Domain: domain, Repository: repo, Format: format, Namespace: ns, Package: name, PackageVersion: aws.String("1"),
		AssetName: aws.String("a"), AssetSHA256: aws.String(sha256Hex(data)), AssetContent: bytes.NewReader(data),
	})
	requireAPICode(t, err, "ConflictException", 409)

	cg, err := e.c.CreatePackageGroup(ctx, &ca.CreatePackageGroupInput{
		Domain: domain, PackageGroup: aws.String("/npm/*"), ContactInfo: aws.String("a@b.c"), Description: aws.String("npm"),
		Tags: []catypes.Tag{{Key: aws.String("k"), Value: aws.String("v")}},
	})
	if err != nil || aws.ToString(cg.PackageGroup.Pattern) != "/npm/*" || aws.ToString(cg.PackageGroup.ContactInfo) != "a@b.c" {
		t.Fatalf("CreatePackageGroup = %+v err=%v", cg, err)
	}

	if _, err = e.c.CreatePackageGroup(ctx, &ca.CreatePackageGroupInput{Domain: domain, PackageGroup: aws.String("/npm/space/*")}); err != nil {
		t.Fatalf("child group: %v", err)
	}

	_, err = e.c.CreatePackageGroup(ctx, &ca.CreatePackageGroupInput{Domain: domain, PackageGroup: aws.String("/npm/*")})
	requireAPICode(t, err, "ConflictException", 409)

	_, err = e.c.CreatePackageGroup(ctx, &ca.CreatePackageGroupInput{Domain: domain, PackageGroup: aws.String("npm/*")})
	requireAPICode(t, err, "ValidationException", 400)

	upd, err := e.c.UpdatePackageGroupOriginConfiguration(ctx, &ca.UpdatePackageGroupOriginConfigurationInput{
		Domain: domain, PackageGroup: aws.String("/npm/*"),
		Restrictions: map[string]catypes.PackageGroupOriginRestrictionMode{
			string(catypes.PackageGroupOriginRestrictionTypePublish): catypes.PackageGroupOriginRestrictionModeAllowSpecificRepositories,
		},
		AddAllowedRepositories: []catypes.PackageGroupAllowedRepository{
			{RepositoryName: aws.String("repo-a"), OriginRestrictionType: catypes.PackageGroupOriginRestrictionTypePublish},
		},
	})
	if err != nil {
		t.Fatalf("UpdatePackageGroupOriginConfiguration: %v", err)
	}

	if got := upd.AllowedRepositoryUpdates["PUBLISH"]["ADDED"]; len(got) != 1 || got[0] != "repo-a" {
		t.Fatalf("allowedRepositoryUpdates = %+v", upd.AllowedRepositoryUpdates)
	}

	child, err := e.c.DescribePackageGroup(ctx, &ca.DescribePackageGroupInput{Domain: domain, PackageGroup: aws.String("/npm/space/*")})
	if err != nil {
		t.Fatal(err)
	}

	pub := child.PackageGroup.OriginConfiguration.Restrictions["PUBLISH"]
	if pub.EffectiveMode != catypes.PackageGroupOriginRestrictionModeAllowSpecificRepositories ||
		aws.ToString(pub.InheritedFrom.Pattern) != "/npm/*" || aws.ToString(child.PackageGroup.Parent.Pattern) != "/npm/*" {
		t.Fatalf("child = %+v", child.PackageGroup)
	}

	allowed, err := e.c.ListAllowedRepositoriesForGroup(ctx, &ca.ListAllowedRepositoriesForGroupInput{
		Domain: domain, PackageGroup: aws.String("/npm/*"), OriginRestrictionType: catypes.PackageGroupOriginRestrictionTypePublish,
	})
	if err != nil || len(allowed.AllowedRepositories) != 1 || allowed.AllowedRepositories[0] != "repo-a" {
		t.Fatalf("ListAllowedRepositoriesForGroup = %+v err=%v", allowed, err)
	}

	groups, err := e.c.ListPackageGroups(ctx, &ca.ListPackageGroupsInput{Domain: domain})
	if err != nil || len(groups.PackageGroups) != 2 {
		t.Fatalf("ListPackageGroups = %+v err=%v", groups, err)
	}

	subs, err := e.c.ListSubPackageGroups(ctx, &ca.ListSubPackageGroupsInput{Domain: domain, PackageGroup: aws.String("/npm/*")})
	if err != nil || len(subs.PackageGroups) != 1 || aws.ToString(subs.PackageGroups[0].Pattern) != "/npm/space/*" {
		t.Fatalf("ListSubPackageGroups = %+v err=%v", subs, err)
	}

	assoc, err := e.c.GetAssociatedPackageGroup(ctx, &ca.GetAssociatedPackageGroupInput{
		Domain: domain, Format: catypes.PackageFormatNpm, Namespace: aws.String("space"), Package: aws.String("x"),
	})
	if err != nil || aws.ToString(assoc.PackageGroup.Pattern) != "/npm/space/*" || assoc.AssociationType != catypes.PackageGroupAssociationTypeStrong {
		t.Fatalf("GetAssociatedPackageGroup = %+v err=%v", assoc, err)
	}

	prev, err := e.c.ListAssociatedPackages(ctx, &ca.ListAssociatedPackagesInput{Domain: domain, PackageGroup: aws.String("/pypi/*"), Preview: aws.Bool(true)})
	if err != nil || len(prev.Packages) != 0 {
		t.Fatalf("preview of a missing group = %+v err=%v", prev, err)
	}

	_, err = e.c.ListAssociatedPackages(ctx, &ca.ListAssociatedPackagesInput{Domain: domain, PackageGroup: aws.String("/pypi/*")})
	requireAPICode(t, err, "ResourceNotFoundException", 404)

	desc := "updated"

	up, err := e.c.UpdatePackageGroup(ctx, &ca.UpdatePackageGroupInput{Domain: domain, PackageGroup: aws.String("/npm/*"), Description: &desc})
	if err != nil || aws.ToString(up.PackageGroup.Description) != desc || aws.ToString(up.PackageGroup.ContactInfo) != "a@b.c" {
		t.Fatalf("UpdatePackageGroup = %+v err=%v", up, err)
	}

	dg, err := e.c.DeletePackageGroup(ctx, &ca.DeletePackageGroupInput{Domain: domain, PackageGroup: aws.String("/npm/space/*")})
	if err != nil || aws.ToString(dg.PackageGroup.Pattern) != "/npm/space/*" {
		t.Fatalf("DeletePackageGroup = %+v err=%v", dg, err)
	}

	_, err = e.c.DescribePackageGroup(ctx, &ca.DescribePackageGroupInput{Domain: domain, PackageGroup: aws.String("/npm/space/*")})
	requireAPICode(t, err, "ResourceNotFoundException", 404)

	arn := aws.ToString(cg.PackageGroup.Arn)
	if !strings.Contains(arn, ":package-group/dom/npm/*") {
		t.Fatalf("group arn = %q", arn)
	}

	if _, err = e.c.TagResource(ctx, &ca.TagResourceInput{ResourceArn: aws.String(arn), Tags: []catypes.Tag{{Key: aws.String("t"), Value: aws.String("1")}}}); err != nil {
		t.Fatalf("TagResource on group: %v", err)
	}

	tags, err := e.c.ListTagsForResource(ctx, &ca.ListTagsForResourceInput{ResourceArn: aws.String(arn)})
	if err != nil || len(tags.Tags) != 2 {
		t.Fatalf("tags = %+v err=%v", tags, err)
	}
}
