package codeartifact_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/services/codeartifact/driver"
)

func TestPaginationBoundsAndTokens(t *testing.T) {
	m := seeded(t, config.NewOptions(), "repo")
	ctx := context.Background()
	publishAsset(t, m, pkgRef("repo", "pkg"), "1", "f", []byte("x"), false)
	createGroup(t, m, "/npm/*")

	repo := driver.RepositoryRef{Domain: "dom", Repository: "repo"}
	dom := driver.DomainRef{Domain: "dom"}

	lists := map[string]func(p driver.Page) error{
		"ListDomains": func(p driver.Page) error { _, _, err := m.ListDomains(ctx, p); return err },
		"ListRepositories": func(p driver.Page) error {
			_, _, err := m.ListRepositories(ctx, "", p)

			return err
		},
		"ListRepositoriesInDomain": func(p driver.Page) error {
			_, _, err := m.ListRepositoriesInDomain(ctx, "dom", "", "", p)

			return err
		},
		"ListPackages": func(p driver.Page) error {
			_, _, err := m.ListPackages(ctx, &driver.ListPackagesInput{Repository: repo, Page: p})

			return err
		},
		"ListPackageVersions": func(p driver.Page) error {
			_, err := m.ListPackageVersions(ctx, &driver.ListPackageVersionsInput{Ref: pkgRef("repo", "pkg"), Page: p})

			return err
		},
		"ListPackageVersionAssets": func(p driver.Page) error {
			_, _, err := m.ListPackageVersionAssets(ctx, pkgRef("repo", "pkg"), "1", p)

			return err
		},
		"ListPackageGroups": func(p driver.Page) error { _, _, err := m.ListPackageGroups(ctx, dom, "", p); return err },
		"ListSubPackageGroups": func(p driver.Page) error {
			_, _, err := m.ListSubPackageGroups(ctx, dom, "/npm/*", p)

			return err
		},
		"ListAssociatedPackages": func(p driver.Page) error {
			_, _, err := m.ListAssociatedPackages(ctx, dom, "/npm/*", false, p)

			return err
		},
		"ListAllowedRepositoriesForGroup": func(p driver.Page) error {
			_, _, err := m.ListAllowedRepositoriesForGroup(ctx, dom, "/npm/*", driver.RestrictionPublish, p)

			return err
		},
	}

	for name, list := range lists {
		requireNoError(t, list(driver.Page{}))
		requireNoError(t, list(driver.Page{MaxResults: 1}))
		requireNoError(t, list(driver.Page{MaxResults: 1000}))

		for _, bad := range []driver.Page{{MaxResults: -1}, {MaxResults: 1001}, {NextToken: "garbage"}, {NextToken: "ca--5"}, {NextToken: "7"}} {
			if err := list(bad); err == nil {
				t.Errorf("%s accepted %+v", name, bad)
			} else {
				requireAPIError(t, err, driver.ExValidation)
			}
		}
	}
}

func TestPaginationWalksDomainsAndRepositories(t *testing.T) {
	m := newMock()

	for i := 0; i < 4; i++ {
		mustDomain(t, m, fmt.Sprintf("dom-%d", i))
	}

	for i := 0; i < 5; i++ {
		mustRepo(t, m, "dom-0", fmt.Sprintf("repo-%d", i))
	}

	count := func(list func(driver.Page) (int, string)) int {
		total, token := 0, ""

		for {
			n, next := list(driver.Page{MaxResults: 1, NextToken: token})
			total += n

			if next == "" {
				return total
			}

			token = next
		}
	}

	domains := count(func(p driver.Page) (int, string) {
		out, next, err := m.ListDomains(context.Background(), p)
		requireNoError(t, err)

		return len(out), next
	})

	repos := count(func(p driver.Page) (int, string) {
		out, next, err := m.ListRepositories(context.Background(), "", p)
		requireNoError(t, err)

		return len(out), next
	})

	if domains != 4 || repos != 5 {
		t.Fatalf("walked %d domains and %d repositories, want 4 and 5", domains, repos)
	}
}
