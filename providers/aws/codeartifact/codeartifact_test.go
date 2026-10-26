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
	return newMockWithOptions(config.NewOptions())
}

func newMockWithOptions(opts *config.Options) *codeartifact.Mock {
	return codeartifact.New(opts)
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
		Domain: "ghost", Repository: "repo",
	})
	if !cerrors.IsNotFound(err) {
		t.Fatalf("repo without domain: got %v, want NotFound", err)
	}
}

func TestRepositoryComputedAndCount(t *testing.T) {
	m := newMock()
	mustDomain(t, m, "dom")
	r := mustRepo(t, m, "dom", "repo")

	if r.AdministratorAccount == "" || r.DomainOwner == "" {
		t.Fatalf("empty computed field: %+v", r)
	}

	if !strings.HasSuffix(r.Arn, ":repository/dom/repo") {
		t.Fatalf("arn = %q, want suffix :repository/dom/repo", r.Arn)
	}

	d, err := m.DescribeDomain(context.Background(), "dom", "")
	requireNoError(t, err)

	if d.RepositoryCount != 1 {
		t.Fatalf("repositoryCount = %d, want 1", d.RepositoryCount)
	}
}

func TestUpdateRepositoryPreservesIdentity(t *testing.T) {
	m := newMock()
	mustDomain(t, m, "dom")
	created := mustRepo(t, m, "dom", "repo")

	desc := "new description"

	upd, err := m.UpdateRepository(context.Background(), &driver.UpdateRepositoryInput{
		Domain: "dom", Repository: "repo", Description: &desc,
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
	mustDomain(t, m, "dom")
	mustRepo(t, m, "dom", "up")

	_, err := m.CreateRepository(context.Background(), &driver.CreateRepositoryInput{
		Domain: "dom", Repository: "repo", Description: "keep",
		Upstreams: []driver.UpstreamRef{{RepositoryName: "up"}},
	})
	requireNoError(t, err)

	// Update with neither description nor upstreams: both preserved.
	upd, err := m.UpdateRepository(context.Background(), &driver.UpdateRepositoryInput{
		Domain: "dom", Repository: "repo",
	})
	requireNoError(t, err)

	if upd.Description != "keep" || len(upd.Upstreams) != 1 {
		t.Fatalf("absent fields not preserved: %+v", upd)
	}
}

func TestDeleteDomainWithRepositoriesConflict(t *testing.T) {
	m := newMock()
	mustDomain(t, m, "dom")
	mustRepo(t, m, "dom", "repo")

	_, err := m.DeleteDomain(context.Background(), "dom", "")
	if !cerrors.IsAlreadyExists(err) {
		t.Fatalf("delete non-empty domain: got %v, want conflict", err)
	}

	// After removing the repo, delete succeeds.
	_, err = m.DeleteRepository(context.Background(), "dom", "", "repo")
	requireNoError(t, err)

	_, err = m.DeleteDomain(context.Background(), "dom", "")
	requireNoError(t, err)
}

func TestListRepositoriesInDomainFiltered(t *testing.T) {
	m := newMock()
	mustDomain(t, m, "dom1")
	mustDomain(t, m, "dom2")
	mustRepo(t, m, "dom1", "aa")
	mustRepo(t, m, "dom1", "bb")
	mustRepo(t, m, "dom2", "cc")

	repos, _, err := m.ListRepositoriesInDomain(context.Background(), "dom1", "", "", driver.Page{})
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
	mustDomain(t, m, "dom")
	mustRepo(t, m, "dom", "repo")

	assoc, err := m.AssociateExternalConnection(context.Background(), "dom", "", "repo", "public:pypi")
	requireNoError(t, err)

	if len(assoc.ExternalConnections) != 1 || assoc.ExternalConnections[0].PackageFormat != "pypi" {
		t.Fatalf("externalConnections = %+v", assoc.ExternalConnections)
	}

	dis, err := m.DisassociateExternalConnection(context.Background(), "dom", "", "repo", "public:pypi")
	requireNoError(t, err)

	if len(dis.ExternalConnections) != 0 {
		t.Fatalf("externalConnections after disassociate = %+v", dis.ExternalConnections)
	}
}

func TestTagsOnDomainAndRepository(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	domainArn := mustDomain(t, m, "dom").Arn
	repoArn := mustRepo(t, m, "dom", "repo").Arn

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

	created := mustDomain(t, m, "dom")
	repo := mustRepo(t, m, "dom", "repo")

	data, err := m.Snapshot(ctx, false)
	requireNoError(t, err)

	restored := newMock()
	requireNoError(t, restored.Restore(ctx, data))

	d, err := restored.DescribeDomain(ctx, "dom", "")
	requireNoError(t, err)

	if d.Arn != created.Arn || d.RepositoryCount != 1 {
		t.Fatalf("domain not restored with identity/count: %+v", d)
	}

	r, err := restored.DescribeRepository(ctx, "dom", "", "repo")
	requireNoError(t, err)

	if r.Arn != repo.Arn {
		t.Fatalf("repository arn not preserved: %q != %q", r.Arn, repo.Arn)
	}
}
