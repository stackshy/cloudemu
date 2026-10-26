package codeartifact_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/services/codeartifact/driver"
)

// raceN runs fn from n goroutines at once and waits for all of them.
func raceN(n int, fn func(i int)) {
	var wg sync.WaitGroup

	start := make(chan struct{})

	for i := 0; i < n; i++ {
		wg.Add(1)

		go func(i int) {
			defer wg.Done()

			<-start
			fn(i)
		}(i)
	}

	close(start)
	wg.Wait()
}

func TestRepositoryCapHoldsUnderRace(t *testing.T) {
	m := newMock()
	mustDomain(t, m, "dom")

	for i := 0; i < 990; i++ {
		mustRepo(t, m, "dom", fmt.Sprintf("repo-%04d", i))
	}

	var ok atomic.Int32

	raceN(40, func(i int) {
		_, err := m.CreateRepository(context.Background(), &driver.CreateRepositoryInput{Domain: "dom", Repository: fmt.Sprintf("late-%02d", i)})
		if err == nil {
			ok.Add(1)
		}
	})

	if ok.Load() != 10 {
		t.Fatalf("created %d repositories past 990, want exactly 10 (cap 1000)", ok.Load())
	}

	d, err := m.DescribeDomain(context.Background(), "dom", "")
	requireNoError(t, err)

	if d.RepositoryCount != 1000 {
		t.Fatalf("repositoryCount = %d", d.RepositoryCount)
	}
}

func TestTagCapHoldsUnderRace(t *testing.T) {
	m := newMock()
	d := mustDomain(t, m, "dom")

	var ok atomic.Int32

	raceN(60, func(i int) {
		if err := m.TagResource(context.Background(), d.Arn, tagSet(10, i*10)); err == nil {
			ok.Add(1)
		}
	})

	tags, err := m.ListTagsForResource(context.Background(), d.Arn)
	requireNoError(t, err)

	if len(tags) != 200 || ok.Load() != 20 {
		t.Fatalf("tags = %d from %d successful calls, want exactly 200 from 20", len(tags), ok.Load())
	}
}

func TestUpstreamDependentDeleteRace(t *testing.T) {
	for round := 0; round < 30; round++ {
		m := newMock()
		mustDomain(t, m, "dom")
		mustRepo(t, m, "dom", "base")

		var created atomic.Bool

		raceN(2, func(i int) {
			if i == 0 {
				_, err := m.CreateRepository(context.Background(), &driver.CreateRepositoryInput{
					Domain: "dom", Repository: "down", Upstreams: []driver.UpstreamRef{{RepositoryName: "base"}},
				})
				created.Store(err == nil)

				return
			}

			_, _ = m.DeleteRepository(context.Background(), "dom", "", "base")
		})

		down, err := m.DescribeRepository(context.Background(), "dom", "", "down")
		if err != nil {
			continue
		}

		// "down" exists: its upstream must still exist, or the graph dangles.
		for _, u := range down.Upstreams {
			if _, err = m.DescribeRepository(context.Background(), "dom", "", u.RepositoryName); err != nil {
				t.Fatalf("round %d: repository down has a dangling upstream %s", round, u.RepositoryName)
			}
		}
	}
}

func TestAssetCapHoldsUnderRace(t *testing.T) {
	m := seeded(t, config.NewOptions(), "repo")
	ref := pkgRef("repo", "pkg")

	var ok atomic.Int32

	raceN(400, func(i int) {
		data := []byte(fmt.Sprintf("asset %d", i))

		_, err := m.PublishPackageVersion(context.Background(), &driver.PublishInput{
			Ref: ref, Version: "1", AssetName: fmt.Sprintf("a%03d", i), SHA256: sha(data), Content: data, Unfinished: true,
		})
		if err == nil {
			ok.Add(1)
		}
	})

	list, _, err := m.ListPackageVersionAssets(context.Background(), ref, "1", driver.Page{MaxResults: 1000})
	requireNoError(t, err)

	if len(list.Assets) != 350 || ok.Load() != 350 {
		t.Fatalf("stored %d assets from %d successful publishes, want exactly 350", len(list.Assets), ok.Load())
	}
}

func TestSameAssetPublishedConcurrentlyIsIdempotent(t *testing.T) {
	m := seeded(t, config.NewOptions(), "repo")
	ref := pkgRef("repo", "pkg")
	data := []byte("identical")

	var failures atomic.Int32

	raceN(50, func(int) {
		_, err := m.PublishPackageVersion(context.Background(), &driver.PublishInput{
			Ref: ref, Version: "1", AssetName: "f", SHA256: sha(data), Content: data, Unfinished: true,
		})
		if err != nil {
			failures.Add(1)
		}
	})

	if failures.Load() != 0 {
		t.Fatalf("%d concurrent identical publishes failed; an identical asset is a no-op", failures.Load())
	}

	list, _, err := m.ListPackageVersionAssets(context.Background(), ref, "1", driver.Page{})
	requireNoError(t, err)

	if len(list.Assets) != 1 {
		t.Fatalf("assets = %d, want 1", len(list.Assets))
	}
}

func TestPolicyRevisionCompareAndSetUnderRace(t *testing.T) {
	m := newMock()
	mustDomain(t, m, "dom")
	ref := driver.DomainRef{Domain: "dom"}

	first, err := m.PutDomainPermissionsPolicy(context.Background(), ref, "", testPolicy)
	requireNoError(t, err)

	var winners atomic.Int32

	raceN(40, func(int) {
		if _, putErr := m.PutDomainPermissionsPolicy(context.Background(), ref, first.Revision, testPolicy); putErr == nil {
			winners.Add(1)
		}
	})

	if winners.Load() != 1 {
		t.Fatalf("%d puts with the same revision succeeded, want exactly 1", winners.Load())
	}
}

func TestSamePackageGroupCreatedConcurrently(t *testing.T) {
	m := seeded(t, config.NewOptions())

	var wins, conflicts atomic.Int32

	raceN(40, func(int) {
		_, err := m.CreatePackageGroup(context.Background(), &driver.CreatePackageGroupInput{Domain: driver.DomainRef{Domain: "dom"}, Pattern: "/npm/*"})

		var api *driver.APIError

		switch {
		case err == nil:
			wins.Add(1)
		case asAPIError(err, &api) && api.Exception == driver.ExConflict:
			conflicts.Add(1)
		}
	})

	if wins.Load() != 1 || conflicts.Load() != 39 {
		t.Fatalf("wins=%d conflicts=%d, want 1 and 39", wins.Load(), conflicts.Load())
	}
}

func TestDuplicateVersionsInOneRequestAreReportedOnce(t *testing.T) {
	m := seeded(t, config.NewOptions(), "src", "dst")
	ctx := context.Background()
	ref := pkgRef("src", "pkg")
	publishAsset(t, m, ref, "1", "f", []byte("x"), false)

	cp, err := m.CopyPackageVersions(ctx, &driver.CopyInput{Ref: ref, SourceRepository: "src", DestinationRepository: "dst", Versions: []string{"1", "1"}})
	requireNoError(t, err)

	if len(cp.Successful) != 1 || len(cp.Failed) != 0 {
		t.Fatalf("copy outcome = %+v / %+v, want one success and no failure", cp.Successful, cp.Failed)
	}

	del, err := m.DeletePackageVersions(ctx, &driver.VersionsInput{Ref: ref, Versions: []string{"1", "1"}})
	requireNoError(t, err)

	if len(del.Successful) != 1 || len(del.Failed) != 0 {
		t.Fatalf("delete outcome = %+v / %+v, want one success and no failure", del.Successful, del.Failed)
	}
}

func TestListPackagesOrderIsFormatNamespaceName(t *testing.T) {
	m := seeded(t, config.NewOptions(), "repo")
	ctx := context.Background()

	for _, ns := range []string{"a.b", "a-b", "ac"} {
		ref := pkgRef("repo", "pkg")
		ref.Namespace = ns

		publishAsset(t, m, ref, "1", "f", []byte(ns), false)
	}

	pkgs, _, err := m.ListPackages(ctx, &driver.ListPackagesInput{Repository: driver.RepositoryRef{Domain: "dom", Repository: "repo"}})
	requireNoError(t, err)

	got := []string{pkgs[0].Namespace, pkgs[1].Namespace, pkgs[2].Namespace}
	if got[0] != "a-b" || got[1] != "a.b" || got[2] != "ac" {
		t.Fatalf("namespaces in list order = %v, want [a-b a.b ac]", got)
	}
}

func TestVersionsListNewestPublishedFirstAndOriginFilters(t *testing.T) {
	clk := config.NewFakeClock(time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC))
	m := seeded(t, config.NewOptions(config.WithClock(clk)), "repo")
	ctx := context.Background()
	ref := pkgRef("repo", "pkg")

	for _, v := range []string{"1", "2", "3"} {
		publishAsset(t, m, ref, v, "f", []byte(v), false)
		clk.Advance(time.Minute)
	}

	list, err := m.ListPackageVersions(ctx, &driver.ListPackageVersionsInput{Ref: ref, SortBy: driver.SortByPublishedTime})
	requireNoError(t, err)

	if len(list.Versions) != 3 || list.Versions[0].Version != "3" || list.Versions[2].Version != "1" || list.DefaultDisplayVersion != "3" {
		t.Fatalf("versions = %+v default=%q, want newest first", list.Versions, list.DefaultDisplayVersion)
	}

	for origin, want := range map[string]int{driver.OriginTypeInternal: 3, driver.OriginTypeExternal: 0, driver.OriginTypeUnknown: 0} {
		got, listErr := m.ListPackageVersions(ctx, &driver.ListPackageVersionsInput{Ref: ref, OriginType: origin})
		requireNoError(t, listErr)

		if len(got.Versions) != want {
			t.Fatalf("originType %s: %d versions, want %d", origin, len(got.Versions), want)
		}
	}
}

func TestDeleteRepositoryEmitsNoVersionEvents(t *testing.T) {
	m, pub := wired(t, "repo")
	publishAsset(t, m, pkgRef("repo", "pkg"), "1", "f", []byte("x"), false)

	before := len(pub.all())

	_, err := m.DeleteRepository(context.Background(), "dom", "", "repo")
	requireNoError(t, err)

	if after := len(pub.all()); after != before {
		t.Fatalf("deleting a repository emitted %d events, want none", after-before)
	}
}

func TestNewDomainHasNoPackageGroup(t *testing.T) {
	m := newMock()
	mustDomain(t, m, "dom")

	groups, _, err := m.ListPackageGroups(context.Background(), driver.DomainRef{Domain: "dom"}, "", driver.Page{})
	requireNoError(t, err)

	if len(groups) != 0 {
		t.Fatalf("a new domain has %d package groups, want none", len(groups))
	}
}

func asAPIError(err error, target **driver.APIError) bool { return errors.As(err, target) }
