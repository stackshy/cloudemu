package codeartifact_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/services/codeartifact/driver"
)

func createGroup(t *testing.T, m interface {
	CreatePackageGroup(context.Context, *driver.CreatePackageGroupInput) (*driver.PackageGroup, error)
}, pattern string) *driver.PackageGroup {
	t.Helper()

	g, err := m.CreatePackageGroup(context.Background(), &driver.CreatePackageGroupInput{Domain: driver.DomainRef{Domain: "dom"}, Pattern: pattern})
	requireNoError(t, err)

	return g
}

func TestGroupPatternGrammar(t *testing.T) {
	good := []string{
		"/*", "/npm/*", "/maven/com.anycompany~", "/npm/space/*", "/npm/space/anycompany-ui~",
		"/maven/org.apache.logging.log4j/log4j-core$", "/pypi//requests$", "/npm//react$", "/npm//*", "/generic/ns/pkg$",
		"/nuget/*", "/swift/scope/name~", "/cargo//serde$", "/ruby//rails~",
	}

	bad := []string{
		"", "/", "npm/*", "/npm", "/npm/", "/docker/*", "/npm/space", "/npm/space~/x", "/npm/~", "/npm//$",
		"/npm/sp ace/*", "/maven//log4j$", "/maven//*", "/generic//pkg$", "/pypi/ns/requests$", "/nuget/ns~", "/npm/space/pkg",
		"/npm/space/pkg-~", "/maven/com.~", "/npm/a*b/c$", "/npm/a/b/c$", "/" + strings.Repeat("a", 520),
	}

	for _, p := range good {
		m := seeded(t, config.NewOptions())
		createGroup(t, m, p)
	}

	for _, p := range bad {
		m := seeded(t, config.NewOptions())

		_, err := m.CreatePackageGroup(context.Background(), &driver.CreatePackageGroupInput{Domain: driver.DomainRef{Domain: "dom"}, Pattern: p})
		if err == nil {
			t.Errorf("pattern %q accepted", p)

			continue
		}

		requireAPIError(t, err, driver.ExValidation)
	}
}

func TestMostSpecificMatchAndParent(t *testing.T) {
	m := seeded(t, config.NewOptions(), "repo")
	ctx := context.Background()
	dom := driver.DomainRef{Domain: "dom"}

	all := createGroup(t, m, "/*")
	npm := createGroup(t, m, "/npm/*")
	space := createGroup(t, m, "/npm/space/*")
	foo := createGroup(t, m, "/npm/space/foo~")
	exact := createGroup(t, m, "/npm/space/foo-core$")

	parents := map[string]string{
		all.Pattern: "", npm.Pattern: "/*", space.Pattern: "/npm/*", foo.Pattern: "/npm/space/*", exact.Pattern: "/npm/space/foo~",
	}

	for pattern, want := range parents {
		g, err := m.DescribePackageGroup(ctx, dom, pattern)
		requireNoError(t, err)

		got := ""
		if g.Parent != nil {
			got = g.Parent.Pattern
		}

		if got != want {
			t.Errorf("parent of %s = %q, want %q", pattern, got, want)
		}
	}

	cases := []struct {
		format, ns, name, want string
	}{
		{"npm", "", "react", "/npm/*"},
		{"npm", "space", "aui.components", "/npm/space/*"},
		{"npm", "space", "foo", "/npm/space/foo~"},
		{"npm", "space", "foo-bar", "/npm/space/foo~"},
		{"npm", "space", "food", "/npm/space/*"},
		{"npm", "space", "foo-core", "/npm/space/foo-core$"},
		{"pypi", "", "requests", "/*"},
	}

	for _, c := range cases {
		g, strength, err := m.GetAssociatedPackageGroup(ctx, dom, c.format, c.ns, c.name)
		requireNoError(t, err)

		if g.Pattern != c.want || strength != driver.AssociationStrong {
			t.Errorf("%s/%s/%s -> %s (%s), want %s strong", c.format, c.ns, c.name, g.Pattern, strength, c.want)
		}
	}
}

func TestStrongWeakAssociation(t *testing.T) {
	m := seeded(t, config.NewOptions(), "repo")
	ctx := context.Background()
	dom := driver.DomainRef{Domain: "dom"}

	createGroup(t, m, "/generic/ns/AsyncStorage$")
	createGroup(t, m, "/generic/ns/foo-bar$")

	cases := []struct {
		name, group, strength string
	}{
		{"AsyncStorage", "/generic/ns/AsyncStorage$", driver.AssociationStrong},
		{"asyncStorage", "/generic/ns/AsyncStorage$", driver.AssociationWeak},
		{"asyncstorage", "/generic/ns/AsyncStorage$", driver.AssociationWeak},
		{"foo-bar", "/generic/ns/foo-bar$", driver.AssociationStrong},
		{"foo.bar", "/generic/ns/foo-bar$", driver.AssociationWeak},
		{"foo__bar", "/generic/ns/foo-bar$", driver.AssociationWeak},
		{"foo-.bar", "/generic/ns/foo-bar$", driver.AssociationWeak},
	}

	for _, c := range cases {
		g, strength, err := m.GetAssociatedPackageGroup(ctx, dom, "generic", "ns", c.name)
		requireNoError(t, err)

		if g.Pattern != c.group || strength != c.strength {
			t.Errorf("%s -> %s (%s), want %s (%s)", c.name, g.Pattern, strength, c.group, c.strength)
		}
	}

	_, _, err := m.GetAssociatedPackageGroup(ctx, dom, "generic", "ns", "foobar")
	requireAPIError(t, err, driver.ExNotFound)
}
