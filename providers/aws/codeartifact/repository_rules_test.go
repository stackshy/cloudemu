package codeartifact_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/codeartifact/driver"
)

func createRepoWithUpstreams(m interface {
	CreateRepository(context.Context, *driver.CreateRepositoryInput) (*driver.Repository, error)
}, repo string, ups ...string) error {
	refs := make([]driver.UpstreamRef, 0, len(ups))
	for _, u := range ups {
		refs = append(refs, driver.UpstreamRef{RepositoryName: u})
	}

	_, err := m.CreateRepository(context.Background(), &driver.CreateRepositoryInput{
		Domain: "dom", Repository: repo, Upstreams: refs,
	})

	return err
}

func TestAssociateExternalConnectionRules(t *testing.T) {
	m := newMock()
	mustDomain(t, m, "dom")
	mustRepo(t, m, "dom", "repo")
	ctx := context.Background()

	_, err := m.AssociateExternalConnection(ctx, "dom", "", "repo", "public:does-not-exist")
	requireAPIError(t, err, driver.ExNotFound)

	formats := map[string]string{
		"public:maven-clojars": "maven", "public:maven-commonsware": "maven", "public:maven-googleandroid": "maven",
		"public:maven-gradleplugins": "maven", "public:maven-central": "maven", "public:npmjs": "npm",
		"public:nuget-org": "nuget", "public:pypi": "pypi", "public:ruby-gems-org": "ruby", "public:crates-io": "cargo",
	}

	for name, format := range formats {
		repo := "r-" + format + "-" + name[len("public:"):]
		mustRepo(t, m, "dom", repo)

		out, assocErr := m.AssociateExternalConnection(ctx, "dom", "", repo, name)
		requireNoError(t, assocErr)

		if len(out.ExternalConnections) != 1 || out.ExternalConnections[0].PackageFormat != format ||
			out.ExternalConnections[0].Status != driver.ExternalConnStatusAvailable {
			t.Fatalf("%s: connections = %+v, want one %s/Available", name, out.ExternalConnections, format)
		}
	}

	_, err = m.AssociateExternalConnection(ctx, "dom", "", "repo", "public:npmjs")
	requireNoError(t, err)

	_, err = m.AssociateExternalConnection(ctx, "dom", "", "repo", "public:pypi")
	e := requireAPIError(t, err, driver.ExConflict)

	if e.ResourceType != driver.ResourceTypeRepository {
		t.Fatalf("conflict resource type = %q", e.ResourceType)
	}

	got, err := m.DescribeRepository(ctx, "dom", "", "repo")
	requireNoError(t, err)

	if len(got.ExternalConnections) != 1 || got.ExternalConnections[0].ExternalConnectionName != "public:npmjs" {
		t.Fatalf("second associate overwrote the first: %+v", got.ExternalConnections)
	}

	_, err = m.DisassociateExternalConnection(ctx, "dom", "", "repo", "public:pypi")
	requireAPIError(t, err, driver.ExNotFound)

	out, err := m.DisassociateExternalConnection(ctx, "dom", "", "repo", "public:npmjs")
	requireNoError(t, err)

	if len(out.ExternalConnections) != 0 {
		t.Fatalf("connections after disassociate = %+v", out.ExternalConnections)
	}
}

func TestUpstreamValidation(t *testing.T) {
	m := newMock()
	mustDomain(t, m, "dom")
	mustRepo(t, m, "dom", "base")

	requireAPIError(t, createRepoWithUpstreams(m, "dangling", "ghost"), driver.ExNotFound)
	requireAPIError(t, createRepoWithUpstreams(m, "selfref", "selfref"), driver.ExValidation)
	requireAPIError(t, createRepoWithUpstreams(m, "dupes", "base", "base"), driver.ExValidation)
	requireAPIError(t, createRepoWithUpstreams(m, "badname", "bad name"), driver.ExValidation)

	many := make([]string, 0, 11)

	for i := 0; i < 11; i++ {
		name := "up" + string(rune('a'+i)) + "x"
		mustRepo(t, m, "dom", name)

		many = append(many, name)
	}

	e := requireAPIError(t, createRepoWithUpstreams(m, "toomany", many...), driver.ExServiceQuota)

	if e.ResourceType != driver.ResourceTypeRepository {
		t.Fatalf("quota resource type = %q", e.ResourceType)
	}

	requireNoError(t, createRepoWithUpstreams(m, "ten", many[:10]...))

	got, _, err := m.ListRepositoriesInDomain(context.Background(), "dom", "", "dangling", driver.Page{})
	requireNoError(t, err)

	if len(got) != 0 {
		t.Fatalf("rejected create left repository %q", got[0].Name)
	}
}

func TestUpstreamCycleRejected(t *testing.T) {
	m := newMock()
	mustDomain(t, m, "dom")
	mustRepo(t, m, "dom", "repo-a")
	requireNoError(t, createRepoWithUpstreams(m, "repo-b", "repo-a"))

	ups := []driver.UpstreamRef{{RepositoryName: "repo-b"}}
	_, err := m.UpdateRepository(context.Background(), &driver.UpdateRepositoryInput{
		Domain: "dom", Repository: "repo-a", Upstreams: &ups,
	})
	requireAPIError(t, err, driver.ExValidation)

	got, err := m.DescribeRepository(context.Background(), "dom", "", "repo-a")
	requireNoError(t, err)

	if len(got.Upstreams) != 0 {
		t.Fatalf("cycle was stored: %+v", got.Upstreams)
	}
}

func TestUpstreamsXorExternalConnection(t *testing.T) {
	m := newMock()
	mustDomain(t, m, "dom")
	mustRepo(t, m, "dom", "base")
	mustRepo(t, m, "dom", "repo")
	ctx := context.Background()

	_, err := m.AssociateExternalConnection(ctx, "dom", "", "repo", "public:npmjs")
	requireNoError(t, err)

	ups := []driver.UpstreamRef{{RepositoryName: "base"}}
	_, err = m.UpdateRepository(ctx, &driver.UpdateRepositoryInput{Domain: "dom", Repository: "repo", Upstreams: &ups})
	requireAPIError(t, err, driver.ExConflict)

	requireNoError(t, createRepoWithUpstreams(m, "withup", "base"))

	_, err = m.AssociateExternalConnection(ctx, "dom", "", "withup", "public:npmjs")
	requireAPIError(t, err, driver.ExConflict)
}

func TestUpdateRepositoryUpstreamsMerge(t *testing.T) {
	m := newMock()
	mustDomain(t, m, "dom")
	mustRepo(t, m, "dom", "base")
	requireNoError(t, createRepoWithUpstreams(m, "repo", "base"))
	ctx := context.Background()

	desc := "only description"
	got, err := m.UpdateRepository(ctx, &driver.UpdateRepositoryInput{Domain: "dom", Repository: "repo", Description: &desc})
	requireNoError(t, err)

	if len(got.Upstreams) != 1 || got.Description != desc {
		t.Fatalf("description-only update changed upstreams: %+v", got)
	}

	empty := []driver.UpstreamRef{}
	got, err = m.UpdateRepository(ctx, &driver.UpdateRepositoryInput{Domain: "dom", Repository: "repo", Upstreams: &empty})
	requireNoError(t, err)

	if len(got.Upstreams) != 0 || got.Description != desc {
		t.Fatalf("empty upstreams update = %+v", got)
	}
}

func TestDeleteRepositoryUpstreamDependent(t *testing.T) {
	m := newMock()
	mustDomain(t, m, "dom")
	mustRepo(t, m, "dom", "base")
	requireNoError(t, createRepoWithUpstreams(m, "downstream", "base"))
	ctx := context.Background()

	_, err := m.DeleteRepository(ctx, "dom", "", "base")
	requireAPIError(t, err, driver.ExConflict)

	_, err = m.DescribeRepository(ctx, "dom", "", "base")
	requireNoError(t, err)

	_, err = m.DeleteRepository(ctx, "dom", "", "downstream")
	requireNoError(t, err)

	_, err = m.DeleteRepository(ctx, "dom", "", "base")
	requireNoError(t, err)
}

func TestDeleteDomainGuardsAndCleanup(t *testing.T) {
	m := newMock()
	mustDomain(t, m, "dom")
	mustRepo(t, m, "dom", "repo")
	ctx := context.Background()

	_, err := m.DeleteDomain(ctx, "dom", "")
	requireAPIError(t, err, driver.ExConflict)

	_, err = m.DeleteRepository(ctx, "dom", "", "repo")
	requireNoError(t, err)

	out, err := m.DeleteDomain(ctx, "dom", "")
	requireNoError(t, err)

	if out.Status != driver.DomainStatusDeleted {
		t.Fatalf("status = %q", out.Status)
	}

	_, err = m.DescribeDomain(ctx, "dom", "")
	requireAPIError(t, err, driver.ExNotFound)
}

func TestMalformedUpstreamsAreValidationErrorsEvenAtTheRepositoryCap(t *testing.T) {
	m := newMock()
	mustDomain(t, m, "dom")

	for i := 0; i < 1000; i++ {
		mustRepo(t, m, "dom", fmt.Sprintf("repo-%04d", i))
	}

	requireAPIError(t, createRepoWithUpstreams(m, "selfref", "selfref"), driver.ExValidation)
	requireAPIError(t, createRepoWithUpstreams(m, "dupes", "repo-0001", "repo-0001"), driver.ExValidation)
	requireAPIError(t, createRepoWithUpstreams(m, "badname", "bad name"), driver.ExValidation)
	requireAPIError(t, createRepoWithUpstreams(m, "wellformed", "repo-0001"), driver.ExServiceQuota)

	ups := []driver.UpstreamRef{{RepositoryName: "repo-0000"}, {RepositoryName: "repo-0000"}}
	_, err := m.UpdateRepository(context.Background(), &driver.UpdateRepositoryInput{Domain: "dom", Repository: "repo-0001", Upstreams: &ups})
	requireAPIError(t, err, driver.ExValidation)
}
