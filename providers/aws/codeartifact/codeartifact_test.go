package codeartifact_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/aws/codeartifact"
	"github.com/stackshy/cloudemu/v2/services/codeartifact/driver"
)

func newMock() *codeartifact.Mock {
	return codeartifact.New(config.NewOptions())
}

func requireNoError(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func mustDomain(t *testing.T, m *codeartifact.Mock, name string) *driver.Domain {
	t.Helper()

	d, err := m.CreateDomain(context.Background(), &driver.CreateDomainInput{Name: name})
	requireNoError(t, err)

	return d
}

func mustRepo(t *testing.T, m *codeartifact.Mock, domain, repo string) *driver.Repository {
	t.Helper()

	r, err := m.CreateRepository(context.Background(), &driver.CreateRepositoryInput{
		Domain: domain, Repository: repo,
	})
	requireNoError(t, err)

	return r
}

func TestCreateDomainComputedFields(t *testing.T) {
	m := newMock()
	d := mustDomain(t, m, "my-domain")

	if d.Status != driver.DomainStatusActive {
		t.Fatalf("status = %q, want Active", d.Status)
	}

	if d.Owner == "" || d.Arn == "" || d.EncryptionKey == "" {
		t.Fatalf("empty computed field: %+v", d)
	}

	if !strings.HasSuffix(d.Arn, ":domain/my-domain") {
		t.Fatalf("arn = %q, want suffix :domain/my-domain", d.Arn)
	}

	if d.CreatedTime.IsZero() {
		t.Fatal("createdTime is zero")
	}

	if d.AssetSizeBytes != 0 || d.RepositoryCount != 0 {
		t.Fatalf("assetSizeBytes=%d repositoryCount=%d, want 0/0", d.AssetSizeBytes, d.RepositoryCount)
	}
}

func TestCreateDomainDuplicateConflict(t *testing.T) {
	m := newMock()
	mustDomain(t, m, "dup")

	_, err := m.CreateDomain(context.Background(), &driver.CreateDomainInput{Name: "dup"})
	if !cerrors.IsAlreadyExists(err) {
		t.Fatalf("duplicate domain: got %v, want AlreadyExists", err)
	}
}

func TestDomainComputedStableAcrossReads(t *testing.T) {
	m := newMock()
	created := mustDomain(t, m, "stable")

	d1, err := m.DescribeDomain(context.Background(), "stable", "")
	requireNoError(t, err)

	if d1.Arn != created.Arn || !d1.CreatedTime.Equal(created.CreatedTime) ||
		d1.EncryptionKey != created.EncryptionKey {
		t.Fatal("computed fields drifted between create and describe")
	}
}

func TestRepositoryRequiresDomain(t *testing.T) {
	m := newMock()

	_, err := m.CreateRepository(context.Background(), &driver.CreateRepositoryInput{
		Domain: "ghost", Repository: "r",
	})
	if !cerrors.IsNotFound(err) {
		t.Fatalf("repo without domain: got %v, want NotFound", err)
	}
}

func TestRepositoryComputedAndCount(t *testing.T) {
	m := newMock()
	mustDomain(t, m, "d")
	r := mustRepo(t, m, "d", "repo")

	if r.AdministratorAccount == "" || r.DomainOwner == "" {
		t.Fatalf("empty computed field: %+v", r)
	}

	if !strings.HasSuffix(r.Arn, ":repository/d/repo") {
		t.Fatalf("arn = %q, want suffix :repository/d/repo", r.Arn)
	}

	d, err := m.DescribeDomain(context.Background(), "d", "")
	requireNoError(t, err)

	if d.RepositoryCount != 1 {
		t.Fatalf("repositoryCount = %d, want 1", d.RepositoryCount)
	}
}

func TestUpdateRepositoryPreservesIdentity(t *testing.T) {
	m := newMock()
	mustDomain(t, m, "d")
	created := mustRepo(t, m, "d", "repo")

	desc := "new description"

	upd, err := m.UpdateRepository(context.Background(), &driver.UpdateRepositoryInput{
		Domain: "d", Repository: "repo", Description: &desc,
	})
	requireNoError(t, err)

	if upd.Description != desc {
		t.Fatalf("description = %q, want %q", upd.Description, desc)
	}

	if upd.Arn != created.Arn || !upd.CreatedTime.Equal(created.CreatedTime) {
		t.Fatal("identity fields drifted after update")
	}
}

func TestUpdateRepositoryAbsentFieldsUnchanged(t *testing.T) {
	m := newMock()
	mustDomain(t, m, "d")

	_, err := m.CreateRepository(context.Background(), &driver.CreateRepositoryInput{
		Domain: "d", Repository: "repo", Description: "keep",
		Upstreams: []driver.UpstreamRef{{RepositoryName: "up"}},
	})
	requireNoError(t, err)

	// Update with neither description nor upstreams: both preserved.
	upd, err := m.UpdateRepository(context.Background(), &driver.UpdateRepositoryInput{
		Domain: "d", Repository: "repo",
	})
	requireNoError(t, err)

	if upd.Description != "keep" || len(upd.Upstreams) != 1 {
		t.Fatalf("absent fields not preserved: %+v", upd)
	}
}

func TestDeleteDomainWithRepositoriesConflict(t *testing.T) {
	m := newMock()
	mustDomain(t, m, "d")
	mustRepo(t, m, "d", "repo")

	_, err := m.DeleteDomain(context.Background(), "d", "")
	if !cerrors.IsAlreadyExists(err) {
		t.Fatalf("delete non-empty domain: got %v, want conflict", err)
	}

	// After removing the repo, delete succeeds.
	_, err = m.DeleteRepository(context.Background(), "d", "", "repo")
	requireNoError(t, err)

	_, err = m.DeleteDomain(context.Background(), "d", "")
	requireNoError(t, err)
}

func TestListRepositoriesInDomainFiltered(t *testing.T) {
	m := newMock()
	mustDomain(t, m, "d1")
	mustDomain(t, m, "d2")
	mustRepo(t, m, "d1", "a")
	mustRepo(t, m, "d1", "b")
	mustRepo(t, m, "d2", "c")

	repos, _, err := m.ListRepositoriesInDomain(context.Background(), "d1", "", "", driver.Page{})
	requireNoError(t, err)

	if len(repos) != 2 {
		t.Fatalf("d1 repos = %d, want 2", len(repos))
	}

	all, _, err := m.ListRepositories(context.Background(), "", driver.Page{})
	requireNoError(t, err)

	if len(all) != 3 {
		t.Fatalf("all repos = %d, want 3", len(all))
	}
}

func TestExternalConnectionAssociateDisassociate(t *testing.T) {
	m := newMock()
	mustDomain(t, m, "d")
	mustRepo(t, m, "d", "repo")

	assoc, err := m.AssociateExternalConnection(context.Background(), "d", "", "repo", "public:pypi")
	requireNoError(t, err)

	if len(assoc.ExternalConnections) != 1 || assoc.ExternalConnections[0].PackageFormat != "pypi" {
		t.Fatalf("externalConnections = %+v", assoc.ExternalConnections)
	}

	dis, err := m.DisassociateExternalConnection(context.Background(), "d", "", "repo", "public:pypi")
	requireNoError(t, err)

	if len(dis.ExternalConnections) != 0 {
		t.Fatalf("externalConnections after disassociate = %+v", dis.ExternalConnections)
	}
}

func TestTagsOnDomainAndRepository(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	domainArn := mustDomain(t, m, "d").Arn
	repoArn := mustRepo(t, m, "d", "repo").Arn

	for _, arn := range []string{domainArn, repoArn} {
		requireNoError(t, m.TagResource(ctx, arn, map[string]string{"k": "v", "drop": "x"}))

		tags, err := m.ListTagsForResource(ctx, arn)
		requireNoError(t, err)

		if tags["k"] != "v" {
			t.Fatalf("tags = %v, want k=v", tags)
		}

		requireNoError(t, m.UntagResource(ctx, arn, []string{"drop"}))

		tags, _ = m.ListTagsForResource(ctx, arn)
		if _, ok := tags["drop"]; ok {
			t.Fatalf("drop tag not removed: %v", tags)
		}
	}
}

func TestTagsUnknownResource(t *testing.T) {
	m := newMock()

	err := m.TagResource(context.Background(),
		"arn:aws:codeartifact:us-east-1:123456789012:domain/missing", map[string]string{"k": "v"})
	if !cerrors.IsNotFound(err) {
		t.Fatalf("tag missing domain: got %v, want NotFound", err)
	}
}

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	created := mustDomain(t, m, "d")
	repo := mustRepo(t, m, "d", "repo")

	data, err := m.Snapshot(ctx, false)
	requireNoError(t, err)

	restored := newMock()
	requireNoError(t, restored.Restore(ctx, data))

	d, err := restored.DescribeDomain(ctx, "d", "")
	requireNoError(t, err)

	if d.Arn != created.Arn || d.RepositoryCount != 1 {
		t.Fatalf("domain not restored with identity/count: %+v", d)
	}

	r, err := restored.DescribeRepository(ctx, "d", "", "repo")
	requireNoError(t, err)

	if r.Arn != repo.Arn {
		t.Fatalf("repository arn not preserved: %q != %q", r.Arn, repo.Arn)
	}
}
