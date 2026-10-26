package codeartifact_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/services/codeartifact/driver"
)

func TestPackageGroupCRUD(t *testing.T) {
	m := seeded(t, config.NewOptions(), "repo")
	ctx := context.Background()
	dom := driver.DomainRef{Domain: "dom"}

	created, err := m.CreatePackageGroup(ctx, &driver.CreatePackageGroupInput{
		Domain: dom, Pattern: "/npm/*", ContactInfo: "a@b.c", Description: "npm packages", Tags: map[string]string{"k": "v"},
	})
	requireNoError(t, err)

	if created.Arn == "" || created.DomainName != "dom" || created.CreatedTime.IsZero() || created.Parent != nil || created.ContactInfo != "a@b.c" {
		t.Fatalf("created = %+v", created)
	}

	for _, rt := range []string{driver.RestrictionPublish, driver.RestrictionExternalUpstream, driver.RestrictionInternalUpstream} {
		r := created.Restrictions[rt]
		if r.Mode != driver.ModeInherit || r.EffectiveMode != driver.ModeAllow || r.InheritedFrom != nil {
			t.Fatalf("restriction %s = %+v", rt, r)
		}
	}

	again, err := m.DescribePackageGroup(ctx, dom, "/npm/*")
	requireNoError(t, err)

	if again.Arn != created.Arn || !again.CreatedTime.Equal(created.CreatedTime) {
		t.Fatal("describe drifted from create")
	}

	_, err = m.CreatePackageGroup(ctx, &driver.CreatePackageGroupInput{Domain: dom, Pattern: "/npm/*"})
	requireAPIError(t, err, driver.ExConflict)

	// Patterns are case-sensitive: differing case is a different group.
	_, err = m.CreatePackageGroup(ctx, &driver.CreatePackageGroupInput{Domain: dom, Pattern: "/npm//AsyncStorage$"})
	requireNoError(t, err)

	_, err = m.CreatePackageGroup(ctx, &driver.CreatePackageGroupInput{Domain: dom, Pattern: "/npm//asyncStorage$"})
	requireNoError(t, err)

	newDesc := "updated"

	upd, err := m.UpdatePackageGroup(ctx, &driver.UpdatePackageGroupInput{Domain: dom, Pattern: "/npm/*", Description: &newDesc})
	requireNoError(t, err)

	if upd.Description != "updated" || upd.ContactInfo != "a@b.c" {
		t.Fatalf("update = %+v", upd)
	}

	del, err := m.DeletePackageGroup(ctx, dom, "/npm/*")
	requireNoError(t, err)

	if del.Pattern != "/npm/*" {
		t.Fatalf("deleted = %+v", del)
	}

	_, err = m.DescribePackageGroup(ctx, dom, "/npm/*")
	requireAPIError(t, err, driver.ExNotFound)

	_, err = m.DeletePackageGroup(ctx, dom, "/npm/*")
	requireAPIError(t, err, driver.ExNotFound)

	_, err = m.CreatePackageGroup(ctx, &driver.CreatePackageGroupInput{Domain: driver.DomainRef{Domain: "ghost"}, Pattern: "/npm/*"})
	requireAPIError(t, err, driver.ExNotFound)

	long := string(make([]byte, 1001))

	_, err = m.CreatePackageGroup(ctx, &driver.CreatePackageGroupInput{Domain: dom, Pattern: "/pypi/*", ContactInfo: long})
	requireAPIError(t, err, driver.ExValidation)
}

func TestGroupOriginModesAndInheritance(t *testing.T) {
	m := seeded(t, config.NewOptions(), "repo-a", "repo-b")
	ctx := context.Background()
	dom := driver.DomainRef{Domain: "dom"}

	createGroup(t, m, "/npm/*")
	createGroup(t, m, "/npm/space/*")

	out, err := m.UpdatePackageGroupOriginConfiguration(ctx, &driver.UpdateGroupOriginInput{
		Domain: dom, Pattern: "/npm/*",
		Restrictions: map[string]string{
			driver.RestrictionPublish: driver.ModeBlock, driver.RestrictionInternalUpstream: driver.ModeAllowSpecificRepositories,
		},
		AddAllowed: []driver.AllowedRepository{
			{RepositoryName: "repo-a", OriginRestrictionType: driver.RestrictionInternalUpstream},
			{RepositoryName: "repo-b", OriginRestrictionType: driver.RestrictionInternalUpstream},
			{RepositoryName: "repo-a", OriginRestrictionType: driver.RestrictionInternalUpstream},
		},
	})
	requireNoError(t, err)

	added := out.Updates[driver.RestrictionInternalUpstream][driver.AllowedRepositoryAdded]
	if len(added) != 2 || added[0] != "repo-a" || added[1] != "repo-b" {
		t.Fatalf("updates = %+v", out.Updates)
	}

	if got := out.Group.Restrictions[driver.RestrictionInternalUpstream]; got.RepositoriesCount != 2 || got.Mode != driver.ModeAllowSpecificRepositories {
		t.Fatalf("internal upstream = %+v", got)
	}

	child, err := m.DescribePackageGroup(ctx, dom, "/npm/space/*")
	requireNoError(t, err)

	pub := child.Restrictions[driver.RestrictionPublish]
	if pub.Mode != driver.ModeInherit || pub.EffectiveMode != driver.ModeBlock || pub.InheritedFrom == nil || pub.InheritedFrom.Pattern != "/npm/*" {
		t.Fatalf("inherited publish = %+v", pub)
	}

	ext := child.Restrictions[driver.RestrictionExternalUpstream]
	if ext.EffectiveMode != driver.ModeAllow || ext.InheritedFrom != nil {
		t.Fatalf("no-parent default = %+v", ext)
	}

	list, _, err := m.ListAllowedRepositoriesForGroup(ctx, dom, "/npm/*", driver.RestrictionInternalUpstream, driver.Page{})
	requireNoError(t, err)

	if len(list) != 2 {
		t.Fatalf("allowed = %v", list)
	}

	rm, err := m.UpdatePackageGroupOriginConfiguration(ctx, &driver.UpdateGroupOriginInput{
		Domain: dom, Pattern: "/npm/*",
		RemoveAllowed: []driver.AllowedRepository{
			{RepositoryName: "repo-a", OriginRestrictionType: driver.RestrictionInternalUpstream},
			{RepositoryName: "never-added", OriginRestrictionType: driver.RestrictionInternalUpstream},
		},
	})
	requireNoError(t, err)

	removed := rm.Updates[driver.RestrictionInternalUpstream][driver.AllowedRepositoryRemoved]
	if len(removed) != 1 || removed[0] != "repo-a" {
		t.Fatalf("removed = %+v", rm.Updates)
	}

	// Switching away from ALLOW_SPECIFIC_REPOSITORIES clears the list.
	_, err = m.UpdatePackageGroupOriginConfiguration(ctx, &driver.UpdateGroupOriginInput{
		Domain: dom, Pattern: "/npm/*", Restrictions: map[string]string{driver.RestrictionInternalUpstream: driver.ModeAllow},
	})
	requireNoError(t, err)

	list, _, err = m.ListAllowedRepositoriesForGroup(ctx, dom, "/npm/*", driver.RestrictionInternalUpstream, driver.Page{})
	requireNoError(t, err)

	if len(list) != 0 {
		t.Fatalf("list after switching mode = %v", list)
	}
}

func TestGroupOriginUpdateErrorsChangeNothing(t *testing.T) {
	m := seeded(t, config.NewOptions(), "repo-a")
	ctx := context.Background()
	dom := driver.DomainRef{Domain: "dom"}

	createGroup(t, m, "/npm/*")

	bad := []driver.UpdateGroupOriginInput{
		{Restrictions: map[string]string{"NOPE": driver.ModeAllow}},
		{Restrictions: map[string]string{driver.RestrictionPublish: "MAYBE"}},
		{AddAllowed: []driver.AllowedRepository{{RepositoryName: "repo-a", OriginRestrictionType: "NOPE"}}},
		{AddAllowed: []driver.AllowedRepository{{RepositoryName: "repo-a", OriginRestrictionType: driver.RestrictionPublish}}},
		{Restrictions: map[string]string{driver.RestrictionPublish: driver.ModeAllowSpecificRepositories},
			AddAllowed: []driver.AllowedRepository{{RepositoryName: "ghost-repo", OriginRestrictionType: driver.RestrictionPublish}}},
	}

	wantNotFound := map[int]bool{4: true}

	for i := range bad {
		bad[i].Domain, bad[i].Pattern = dom, "/npm/*"

		_, err := m.UpdatePackageGroupOriginConfiguration(ctx, &bad[i])
		if wantNotFound[i] {
			requireAPIError(t, err, driver.ExNotFound)
		} else {
			requireAPIError(t, err, driver.ExValidation)
		}
	}

	g, err := m.DescribePackageGroup(ctx, dom, "/npm/*")
	requireNoError(t, err)

	if g.Restrictions[driver.RestrictionPublish].Mode != driver.ModeInherit {
		t.Fatalf("rejected update changed the group: %+v", g.Restrictions[driver.RestrictionPublish])
	}

	_, _, err = m.ListAllowedRepositoriesForGroup(ctx, dom, "/npm/*", "NOPE", driver.Page{})
	requireAPIError(t, err, driver.ExValidation)

	_, err = m.UpdatePackageGroupOriginConfiguration(ctx, &driver.UpdateGroupOriginInput{Domain: dom, Pattern: "/pypi/*"})
	requireAPIError(t, err, driver.ExNotFound)
}

func TestDeleteGroupReparentsChildren(t *testing.T) {
	m := seeded(t, config.NewOptions())
	ctx := context.Background()
	dom := driver.DomainRef{Domain: "dom"}

	createGroup(t, m, "/npm/*")
	createGroup(t, m, "/npm/space/*")
	createGroup(t, m, "/npm/space/foo$")

	_, err := m.UpdatePackageGroupOriginConfiguration(ctx, &driver.UpdateGroupOriginInput{
		Domain: dom, Pattern: "/npm/*", Restrictions: map[string]string{driver.RestrictionPublish: driver.ModeBlock},
	})
	requireNoError(t, err)

	_, err = m.UpdatePackageGroupOriginConfiguration(ctx, &driver.UpdateGroupOriginInput{
		Domain: dom, Pattern: "/npm/space/*", Restrictions: map[string]string{driver.RestrictionPublish: driver.ModeAllow},
	})
	requireNoError(t, err)

	leaf, err := m.DescribePackageGroup(ctx, dom, "/npm/space/foo$")
	requireNoError(t, err)

	if got := leaf.Restrictions[driver.RestrictionPublish]; got.EffectiveMode != driver.ModeAllow || got.InheritedFrom.Pattern != "/npm/space/*" {
		t.Fatalf("before delete = %+v", got)
	}

	_, err = m.DeletePackageGroup(ctx, dom, "/npm/space/*")
	requireNoError(t, err)

	leaf, err = m.DescribePackageGroup(ctx, dom, "/npm/space/foo$")
	requireNoError(t, err)

	if leaf.Parent == nil || leaf.Parent.Pattern != "/npm/*" {
		t.Fatalf("leaf parent = %+v, want /npm/*", leaf.Parent)
	}

	if got := leaf.Restrictions[driver.RestrictionPublish]; got.EffectiveMode != driver.ModeBlock || got.InheritedFrom.Pattern != "/npm/*" {
		t.Fatalf("after delete = %+v", got)
	}
}

func TestListGroupsSubGroupsAndAssociated(t *testing.T) {
	m := seeded(t, config.NewOptions(), "repo")
	ctx := context.Background()
	dom := driver.DomainRef{Domain: "dom"}

	publishAsset(t, m, pkgRef("repo", "plain"), "1", "f", []byte("p"), false)
	publishAsset(t, m, pkgRef("repo", "special"), "1", "f", []byte("s"), false)
	publishAsset(t, m, pkgRef("repo", "Special"), "1", "f", []byte("S"), false)

	createGroup(t, m, "/generic/*")
	createGroup(t, m, "/generic/ns/*")
	createGroup(t, m, "/generic/ns/special$")
	createGroup(t, m, "/npm/*")

	all, next, err := m.ListPackageGroups(ctx, dom, "", driver.Page{})
	requireNoError(t, err)

	if next != "" || len(all) != 4 || all[0].Pattern != "/generic/*" || all[3].Pattern != "/npm/*" {
		t.Fatalf("groups = %d next=%q", len(all), next)
	}

	pre, _, err := m.ListPackageGroups(ctx, dom, "/generic/ns", driver.Page{})
	requireNoError(t, err)

	if len(pre) != 2 {
		t.Fatalf("prefix list = %d", len(pre))
	}

	subs, _, err := m.ListSubPackageGroups(ctx, dom, "/generic/ns/*", driver.Page{})
	requireNoError(t, err)

	if len(subs) != 1 || subs[0].Pattern != "/generic/ns/special$" {
		t.Fatalf("sub groups = %+v", subs)
	}

	assoc, _, err := m.ListAssociatedPackages(ctx, dom, "/generic/ns/special$", false, driver.Page{})
	requireNoError(t, err)

	if len(assoc) != 2 || assoc[0].Package != "Special" || assoc[0].AssociationType != driver.AssociationWeak ||
		assoc[1].Package != "special" || assoc[1].AssociationType != driver.AssociationStrong {
		t.Fatalf("associated = %+v", assoc)
	}

	rest, _, err := m.ListAssociatedPackages(ctx, dom, "/generic/ns/*", false, driver.Page{})
	requireNoError(t, err)

	if len(rest) != 1 || rest[0].Package != "plain" {
		t.Fatalf("associated with parent = %+v", rest)
	}

	_, _, err = m.ListAssociatedPackages(ctx, dom, "/generic/other/*", false, driver.Page{})
	requireAPIError(t, err, driver.ExNotFound)

	preview, _, err := m.ListAssociatedPackages(ctx, dom, "/generic/ns/plain~", true, driver.Page{})
	requireNoError(t, err)

	if len(preview) != 1 || preview[0].Package != "plain" {
		t.Fatalf("preview = %+v", preview)
	}
}

func TestGroupPagination(t *testing.T) {
	m := seeded(t, config.NewOptions())

	for i := 0; i < 5; i++ {
		createGroup(t, m, fmt.Sprintf("/npm/scope%d/*", i))
	}

	var seen []string

	token := ""

	for {
		groups, next, err := m.ListPackageGroups(context.Background(), driver.DomainRef{Domain: "dom"}, "", driver.Page{MaxResults: 2, NextToken: token})
		requireNoError(t, err)

		for _, g := range groups {
			seen = append(seen, g.Pattern)
		}

		if next == "" {
			break
		}

		token = next
	}

	if len(seen) != 5 || seen[0] != "/npm/scope0/*" || seen[4] != "/npm/scope4/*" {
		t.Fatalf("walk = %v", seen)
	}
}

func TestDeleteRepositoryDropsAllowedListEntry(t *testing.T) {
	m := seeded(t, config.NewOptions(), "repo-a", "repo-b")
	ctx := context.Background()
	dom := driver.DomainRef{Domain: "dom"}

	createGroup(t, m, "/npm/*")

	_, err := m.UpdatePackageGroupOriginConfiguration(ctx, &driver.UpdateGroupOriginInput{
		Domain: dom, Pattern: "/npm/*", Restrictions: map[string]string{driver.RestrictionPublish: driver.ModeAllowSpecificRepositories},
		AddAllowed: []driver.AllowedRepository{
			{RepositoryName: "repo-a", OriginRestrictionType: driver.RestrictionPublish},
			{RepositoryName: "repo-b", OriginRestrictionType: driver.RestrictionPublish},
		},
	})
	requireNoError(t, err)

	_, err = m.DeleteRepository(ctx, "dom", "", "repo-a")
	requireNoError(t, err)

	list, _, err := m.ListAllowedRepositoriesForGroup(ctx, dom, "/npm/*", driver.RestrictionPublish, driver.Page{})
	requireNoError(t, err)

	if len(list) != 1 || list[0] != "repo-b" {
		t.Fatalf("allowed list = %v", list)
	}
}

func TestTagsOnPackageGroups(t *testing.T) {
	m := seeded(t, config.NewOptions())
	ctx := context.Background()

	g := createGroup(t, m, "/npm/space/*")

	requireNoError(t, m.TagResource(ctx, g.Arn, map[string]string{"team": "a"}))

	tags, err := m.ListTagsForResource(ctx, g.Arn)
	requireNoError(t, err)

	if tags["team"] != "a" {
		t.Fatalf("tags = %v", tags)
	}

	requireNoError(t, m.UntagResource(ctx, g.Arn, []string{"team"}))

	err = m.TagResource(ctx, "arn:aws:codeartifact:us-east-1:123456789012:package-group/dom/npm/ghost/*", map[string]string{"a": "b"})
	requireAPIError(t, err, driver.ExNotFound)

	err = m.TagResource(ctx, "arn:aws:codeartifact:us-east-1:123456789012:package-group/nodomainslash", map[string]string{"a": "b"})
	requireAPIError(t, err, driver.ExValidation)
}
