package codeartifact_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/codeartifact/driver"
)

// requireAPIError asserts err is a typed CodeArtifact error with the given
// exception name and canonical code.
func requireAPIError(t *testing.T, err error, exception string) *driver.APIError {
	t.Helper()

	if err == nil {
		t.Fatalf("want %s, got nil error", exception)
	}

	var apiErr *driver.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("want *driver.APIError(%s), got %T: %v", exception, err, err)
	}

	if apiErr.Exception != exception {
		t.Fatalf("exception = %q, want %q (err: %v)", apiErr.Exception, exception, err)
	}

	return apiErr
}

func TestDomainNamePattern(t *testing.T) {
	cases := []struct {
		name string
		ok   bool
	}{
		{"ab", true},
		{"a1", true},
		{"my-domain", true},
		{strings.Repeat("a", 50), true},
		{"a", false},
		{strings.Repeat("a", 51), false},
		{"1abc", false},
		{"-abc", false},
		{"abc-", false},
		{"Abc", false},
		{"my domain", false},
		{"my_domain", false},
		{"dömain", false},
		{"", false},
	}

	for _, tc := range cases {
		m := newMock()
		_, err := m.CreateDomain(context.Background(), &driver.CreateDomainInput{Name: tc.name})

		if tc.ok {
			requireNoError(t, err)

			continue
		}

		requireAPIError(t, err, driver.ExValidation)

		got, _, listErr := m.ListDomains(context.Background(), driver.Page{})
		requireNoError(t, listErr)

		if len(got) != 0 {
			t.Fatalf("name %q: rejected create stored %d domains", tc.name, len(got))
		}
	}
}

func TestRepositoryNamePattern(t *testing.T) {
	cases := []struct {
		name string
		ok   bool
	}{
		{"ab", true},
		{"My.Repo_1-x", true},
		{strings.Repeat("a", 100), true},
		{"a", false},
		{strings.Repeat("a", 101), false},
		{".abc", false},
		{"-abc", false},
		{"a b", false},
		{"a/b", false},
		{"", false},
	}

	for _, tc := range cases {
		m := newMock()
		mustDomain(t, m, "dom")

		_, err := m.CreateRepository(context.Background(), &driver.CreateRepositoryInput{Domain: "dom", Repository: tc.name})

		if tc.ok {
			requireNoError(t, err)

			continue
		}

		requireAPIError(t, err, driver.ExValidation)
	}
}

func TestOwnerAndDescriptionPatterns(t *testing.T) {
	m := newMock()
	mustDomain(t, m, "dom")
	ctx := context.Background()

	for _, owner := range []string{"1234567890", "12345678901", "1234567890123", "12345678901a", " 23456789012"} {
		_, err := m.DescribeDomain(ctx, "dom", owner)
		requireAPIError(t, err, driver.ExValidation)
	}

	_, err := m.CreateRepository(ctx, &driver.CreateRepositoryInput{
		Domain: "dom", Repository: "r1", Description: strings.Repeat("d", 1001),
	})
	requireAPIError(t, err, driver.ExValidation)

	_, err = m.CreateRepository(ctx, &driver.CreateRepositoryInput{
		Domain: "dom", Repository: "r2", Description: strings.Repeat("d", 1000),
	})
	requireNoError(t, err)

	_, err = m.CreateRepository(ctx, &driver.CreateRepositoryInput{
		Domain: "dom", Repository: "r3", Description: "bad\x00control",
	})
	requireAPIError(t, err, driver.ExValidation)
}

func TestCreateDomainRejectsInvalidNameBeforeMutation(t *testing.T) {
	m := newMock()

	for _, bad := range []string{"my domain", "UPPER", "a"} {
		_, err := m.CreateDomain(context.Background(), &driver.CreateDomainInput{Name: bad})
		requireAPIError(t, err, driver.ExValidation)
	}

	got, _, err := m.ListDomains(context.Background(), driver.Page{})
	requireNoError(t, err)

	if len(got) != 0 {
		t.Fatalf("rejected creates left %d domains", len(got))
	}
}

func TestLookupsValidateNames(t *testing.T) {
	m := newMock()
	mustDomain(t, m, "dom")
	mustRepo(t, m, "dom", "repo")

	ctx := context.Background()
	desc := "d"

	checks := map[string]error{}

	_, checks["DescribeDomain"] = m.DescribeDomain(ctx, "Bad Domain", "")
	_, checks["DeleteDomain"] = m.DeleteDomain(ctx, "Bad Domain", "")
	_, checks["DescribeRepository/domain"] = m.DescribeRepository(ctx, "Bad Domain", "", "repo")
	_, checks["DescribeRepository/repo"] = m.DescribeRepository(ctx, "dom", "", "bad repo")
	_, checks["DeleteRepository"] = m.DeleteRepository(ctx, "dom", "", "bad repo")
	_, checks["UpdateRepository"] = m.UpdateRepository(ctx, &driver.UpdateRepositoryInput{
		Domain: "dom", Repository: "bad repo", Description: &desc,
	})
	_, _, checks["ListRepositoriesInDomain"] = m.ListRepositoriesInDomain(ctx, "Bad Domain", "", "", driver.Page{})
	_, checks["Associate"] = m.AssociateExternalConnection(ctx, "dom", "", "bad repo", "public:npmjs")

	for op, err := range checks {
		if err == nil {
			t.Errorf("%s: want ValidationException, got nil", op)

			continue
		}

		requireAPIError(t, err, driver.ExValidation)
	}
}

func TestDomainOwnerHonouredOnLookups(t *testing.T) {
	m := newMock()
	d := mustDomain(t, m, "dom")
	mustRepo(t, m, "dom", "repo")

	ctx := context.Background()
	foreign := "999999999999"

	if foreign == d.Owner {
		t.Fatal("test owner collides with the emulator account")
	}

	_, err := m.DescribeDomain(ctx, "dom", foreign)
	requireAPIError(t, err, driver.ExNotFound)

	_, err = m.DeleteDomain(ctx, "dom", foreign)
	requireAPIError(t, err, driver.ExNotFound)

	_, err = m.DescribeRepository(ctx, "dom", foreign, "repo")
	requireAPIError(t, err, driver.ExNotFound)

	_, _, err = m.ListRepositoriesInDomain(ctx, "dom", foreign, "", driver.Page{})
	requireAPIError(t, err, driver.ExNotFound)

	_, err = m.DescribeDomain(ctx, "dom", d.Owner)
	requireNoError(t, err)

	_, err = m.DescribeDomain(ctx, "dom", "")
	requireNoError(t, err)
}

func TestCreateRepositoryForeignDomainOwnerRejected(t *testing.T) {
	m := newMock()
	mustDomain(t, m, "dom")

	_, err := m.CreateRepository(context.Background(), &driver.CreateRepositoryInput{
		Domain: "dom", DomainOwner: "999999999999", Repository: "repo",
	})
	requireAPIError(t, err, driver.ExNotFound)

	got, _, err := m.ListRepositories(context.Background(), "", driver.Page{})
	requireNoError(t, err)

	if len(got) != 0 {
		t.Fatalf("rejected create stored %d repositories", len(got))
	}
}

func TestCreateRepositoryRecordsAdministratorAndOwner(t *testing.T) {
	opts := config.NewOptions()
	m := newMockWithOptions(opts)
	d := mustDomain(t, m, "dom")
	r := mustRepo(t, m, "dom", "repo")

	if r.DomainOwner != d.Owner || r.AdministratorAccount != opts.AccountID {
		t.Fatalf("owner=%q admin=%q, want owner=%q admin=%q", r.DomainOwner, r.AdministratorAccount, d.Owner, opts.AccountID)
	}
}

func TestNotFoundAndConflictCarryResourceIdentity(t *testing.T) {
	m := newMock()
	mustDomain(t, m, "dom")

	_, err := m.CreateDomain(context.Background(), &driver.CreateDomainInput{Name: "dom"})
	e := requireAPIError(t, err, driver.ExConflict)

	if e.ResourceID != "dom" || e.ResourceType != driver.ResourceTypeDomain {
		t.Fatalf("conflict identity = %q/%q", e.ResourceID, e.ResourceType)
	}

	_, err = m.DescribeRepository(context.Background(), "dom", "", "nope-repo")
	e = requireAPIError(t, err, driver.ExNotFound)

	if e.ResourceType != driver.ResourceTypeRepository {
		t.Fatalf("not-found resource type = %q", e.ResourceType)
	}

	if !cerrors.IsNotFound(err) {
		t.Fatalf("canonical code lost: %v", err)
	}
}

func TestValidationErrorsCarryFieldReason(t *testing.T) {
	m := newMock()
	_, err := m.CreateDomain(context.Background(), &driver.CreateDomainInput{Name: "A"})
	e := requireAPIError(t, err, driver.ExValidation)

	if e.Reason != driver.ReasonFieldValidationFailed {
		t.Fatalf("reason = %q, want %q", e.Reason, driver.ReasonFieldValidationFailed)
	}
}
