package codeartifact_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/codeartifact/driver"
)

const testPolicy = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":"*","Action":"codeartifact:ReadFromRepository","Resource":"*"}]}`

func TestDomainPolicyPutGetDelete(t *testing.T) {
	m := newMock()
	d := mustDomain(t, m, "dom")
	ctx := context.Background()
	ref := driver.DomainRef{Domain: "dom"}

	_, err := m.GetDomainPermissionsPolicy(ctx, ref)
	requireAPIError(t, err, driver.ExNotFound)

	put, err := m.PutDomainPermissionsPolicy(ctx, ref, "", testPolicy)
	requireNoError(t, err)

	if put.Revision == "" || put.ResourceArn != d.Arn || put.Document != testPolicy {
		t.Fatalf("put = %+v", put)
	}

	got, err := m.GetDomainPermissionsPolicy(ctx, ref)
	requireNoError(t, err)

	if *got != *put {
		t.Fatalf("get = %+v, want %+v", got, put)
	}

	del, err := m.DeleteDomainPermissionsPolicy(ctx, ref, "")
	requireNoError(t, err)

	if del.Revision != put.Revision {
		t.Fatalf("delete returned %+v", del)
	}

	_, err = m.GetDomainPermissionsPolicy(ctx, ref)
	requireAPIError(t, err, driver.ExNotFound)

	_, err = m.DeleteDomainPermissionsPolicy(ctx, ref, "")
	requireAPIError(t, err, driver.ExNotFound)
}

func TestPolicyRevisionMismatchConflict(t *testing.T) {
	m := newMock()
	mustDomain(t, m, "dom")
	ctx := context.Background()
	ref := driver.DomainRef{Domain: "dom"}

	first, err := m.PutDomainPermissionsPolicy(ctx, ref, "", testPolicy)
	requireNoError(t, err)

	_, err = m.PutDomainPermissionsPolicy(ctx, ref, "stale-revision", testPolicy)
	requireAPIError(t, err, driver.ExConflict)

	second, err := m.PutDomainPermissionsPolicy(ctx, ref, first.Revision, testPolicy)
	requireNoError(t, err)

	if second.Revision == first.Revision {
		t.Fatal("revision must change on every put")
	}

	_, err = m.DeleteDomainPermissionsPolicy(ctx, ref, first.Revision)
	requireAPIError(t, err, driver.ExConflict)

	_, err = m.DeleteDomainPermissionsPolicy(ctx, ref, second.Revision)
	requireNoError(t, err)

	// A revision names a policy that does not exist any more.
	_, err = m.PutDomainPermissionsPolicy(ctx, ref, second.Revision, testPolicy)
	requireAPIError(t, err, driver.ExConflict)
}

func TestPolicyDocumentValidation(t *testing.T) {
	m := newMock()
	mustDomain(t, m, "dom")
	ctx := context.Background()
	ref := driver.DomainRef{Domain: "dom"}

	pad := strings.Repeat(" ", 7168-len(testPolicy))
	exact := testPolicy + pad

	for _, bad := range []string{"", "not json", `{}`, `{"Version":"2012-10-17"}`, exact + " "} {
		_, err := m.PutDomainPermissionsPolicy(ctx, ref, "", bad)
		requireAPIError(t, err, driver.ExValidation)
	}

	_, err := m.PutDomainPermissionsPolicy(ctx, ref, "", exact)
	requireNoError(t, err)

	_, err = m.PutDomainPermissionsPolicy(ctx, ref, strings.Repeat("r", 101), testPolicy)
	requireAPIError(t, err, driver.ExValidation)
}

func TestRepositoryPolicyPutGetDelete(t *testing.T) {
	m := newMock()
	mustDomain(t, m, "dom")
	r := mustRepo(t, m, "dom", "repo")
	ctx := context.Background()
	ref := driver.RepositoryRef{Domain: "dom", Repository: "repo"}

	_, err := m.GetRepositoryPermissionsPolicy(ctx, ref)
	requireAPIError(t, err, driver.ExNotFound)

	put, err := m.PutRepositoryPermissionsPolicy(ctx, ref, "", testPolicy)
	requireNoError(t, err)

	if put.ResourceArn != r.Arn {
		t.Fatalf("resource arn = %q, want %q", put.ResourceArn, r.Arn)
	}

	_, err = m.PutRepositoryPermissionsPolicy(ctx, ref, "wrong", testPolicy)
	requireAPIError(t, err, driver.ExConflict)

	_, err = m.DeleteRepositoryPermissionsPolicy(ctx, ref, "wrong")
	requireAPIError(t, err, driver.ExConflict)

	_, err = m.DeleteRepositoryPermissionsPolicy(ctx, ref, put.Revision)
	requireNoError(t, err)

	_, err = m.PutRepositoryPermissionsPolicy(ctx, driver.RepositoryRef{Domain: "dom", Repository: "ghost"}, "", testPolicy)
	requireAPIError(t, err, driver.ExNotFound)
}

func TestPolicyRemovedWithResource(t *testing.T) {
	m := newMock()
	mustDomain(t, m, "dom")
	mustRepo(t, m, "dom", "repo")
	ctx := context.Background()

	_, err := m.PutRepositoryPermissionsPolicy(ctx, driver.RepositoryRef{Domain: "dom", Repository: "repo"}, "", testPolicy)
	requireNoError(t, err)

	_, err = m.PutDomainPermissionsPolicy(ctx, driver.DomainRef{Domain: "dom"}, "", testPolicy)
	requireNoError(t, err)

	_, err = m.DeleteRepository(ctx, "dom", "", "repo")
	requireNoError(t, err)

	mustRepo(t, m, "dom", "repo")

	_, err = m.GetRepositoryPermissionsPolicy(ctx, driver.RepositoryRef{Domain: "dom", Repository: "repo"})
	requireAPIError(t, err, driver.ExNotFound)

	_, err = m.DeleteRepository(ctx, "dom", "", "repo")
	requireNoError(t, err)

	_, err = m.DeleteDomain(ctx, "dom", "")
	requireNoError(t, err)

	mustDomain(t, m, "dom")

	_, err = m.GetDomainPermissionsPolicy(ctx, driver.DomainRef{Domain: "dom"})
	requireAPIError(t, err, driver.ExNotFound)
}
