package codeartifact_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/services/codeartifact/driver"
)

const raceWorkers = 50

func TestConcurrentCreateDomainExactlyOneWins(t *testing.T) {
	m := newMock()

	var wg sync.WaitGroup

	var wins, conflicts atomic.Int32

	for i := 0; i < raceWorkers; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			_, err := m.CreateDomain(context.Background(), &driver.CreateDomainInput{Name: "same-name"})

			var apiErr *driver.APIError

			switch {
			case err == nil:
				wins.Add(1)
			case errors.As(err, &apiErr) && apiErr.Exception == driver.ExConflict:
				conflicts.Add(1)
			}
		}()
	}

	wg.Wait()

	if wins.Load() != 1 || conflicts.Load() != raceWorkers-1 {
		t.Fatalf("wins=%d conflicts=%d, want 1/%d", wins.Load(), conflicts.Load(), raceWorkers-1)
	}
}

func TestConcurrentCreateRepositoryAndDeleteDomainNoOrphans(t *testing.T) {
	for round := 0; round < 40; round++ {
		m := newMock()
		mustDomain(t, m, "dom")

		var wg sync.WaitGroup

		wg.Add(2)

		go func() {
			defer wg.Done()

			_, _ = m.CreateRepository(context.Background(), &driver.CreateRepositoryInput{Domain: "dom", Repository: "repo"})
		}()

		go func() {
			defer wg.Done()

			_, _ = m.DeleteDomain(context.Background(), "dom", "")
		}()

		wg.Wait()

		_, domErr := m.DescribeDomain(context.Background(), "dom", "")
		repos, _, err := m.ListRepositories(context.Background(), "", driver.Page{})
		requireNoError(t, err)

		if domErr != nil && len(repos) != 0 {
			t.Fatalf("round %d: domain gone but %d orphan repositories remain", round, len(repos))
		}
	}
}

func TestQuotaCapsNeverExceededUnderRace(t *testing.T) {
	m := newMock()

	var wg sync.WaitGroup

	var ok atomic.Int32

	for i := 0; i < 40; i++ {
		wg.Add(1)

		go func(n int) {
			defer wg.Done()

			if _, err := m.CreateDomain(context.Background(), &driver.CreateDomainInput{Name: fmt.Sprintf("dom-%02d", n)}); err == nil {
				ok.Add(1)
			}
		}(i)
	}

	wg.Wait()

	const domainQuota = 10
	if ok.Load() != domainQuota {
		t.Fatalf("created %d domains, want exactly the quota %d", ok.Load(), domainQuota)
	}
}

func TestPublishVersusDeleteRepositoryNoOrphans(t *testing.T) {
	for round := 0; round < 30; round++ {
		m := seeded(t, config.NewOptions(), "repo")
		ref := pkgRef("repo", "pkg")

		var wg sync.WaitGroup

		wg.Add(2)

		go func() {
			defer wg.Done()

			data := []byte("data")
			_, _ = m.PublishPackageVersion(context.Background(), &driver.PublishInput{
				Ref: ref, Version: "1", AssetName: "f", SHA256: sha(data), Content: data,
			})
		}()

		go func() {
			defer wg.Done()

			_, _ = m.DeleteRepository(context.Background(), "dom", "", "repo")
		}()

		wg.Wait()

		if _, err := m.DescribeRepository(context.Background(), "dom", "", "repo"); err != nil {
			// The repository is gone: no package, version or asset may remain reachable.
			if _, descErr := m.DescribePackage(context.Background(), ref); descErr == nil {
				t.Fatalf("round %d: package survived its repository", round)
			}

			d, descErr := m.DescribeDomain(context.Background(), "dom", "")
			requireNoError(t, descErr)

			if d.AssetSizeBytes != 0 {
				t.Fatalf("round %d: %d orphan asset bytes", round, d.AssetSizeBytes)
			}
		}
	}
}

func TestReadsReturnDeepCopies(t *testing.T) {
	m := richState(t)
	ctx := context.Background()
	ref := pkgRef("repo", "pkg")

	got, err := m.GetPackageVersionAsset(ctx, ref, "1", "f.bin", "")
	requireNoError(t, err)

	got.Content[0] ^= 0xff
	got.Hashes[driver.HashSHA256] = "tampered"

	again, err := m.GetPackageVersionAsset(ctx, ref, "1", "f.bin", "")
	requireNoError(t, err)

	if string(again.Content) != "snapshot bytes" || again.Hashes[driver.HashSHA256] == "tampered" {
		t.Fatal("mutating a returned asset changed stored state")
	}

	repo, err := m.DescribeRepository(ctx, "dom", "", "repo")
	requireNoError(t, err)

	repo.Tags = map[string]string{"x": "y"}
	repo.Upstreams = append(repo.Upstreams, driver.UpstreamRef{RepositoryName: "ghost"})

	repo2, err := m.DescribeRepository(ctx, "dom", "", "repo")
	requireNoError(t, err)

	if len(repo2.Tags) != 0 || len(repo2.Upstreams) != 0 {
		t.Fatalf("mutating a returned repository changed stored state: %+v", repo2)
	}

	grp, err := m.DescribePackageGroup(ctx, driver.DomainRef{Domain: "dom"}, "/generic/ns/*")
	requireNoError(t, err)

	grp.Restrictions[driver.RestrictionPublish] = driver.GroupRestriction{Mode: driver.ModeBlock}

	grp2, err := m.DescribePackageGroup(ctx, driver.DomainRef{Domain: "dom"}, "/generic/ns/*")
	requireNoError(t, err)

	if grp2.Restrictions[driver.RestrictionPublish].Mode != driver.ModeAllowSpecificRepositories {
		t.Fatal("mutating a returned group changed stored state")
	}
}

func TestConcurrentMutatorsVersusReaders(t *testing.T) {
	m := richState(t)
	ctx := context.Background()
	ref := pkgRef("repo", "pkg")

	var wg sync.WaitGroup

	for i := 0; i < raceWorkers; i++ {
		wg.Add(1)

		go func(n int) {
			defer wg.Done()

			switch n % 5 {
			case 0:
				_, _ = m.UpdatePackageVersionsStatus(ctx, &driver.UpdateStatusInput{
					VersionsInput: driver.VersionsInput{Ref: ref, Versions: []string{"1"}}, TargetStatus: []string{"Archived", "Unlisted", "Published"}[n%3],
				})
			case 1:
				_ = m.TagResource(ctx, "arn:aws:codeartifact:us-east-1:123456789012:domain/dom", map[string]string{fmt.Sprintf("k%d", n): "v"})
			case 2:
				_, _ = m.DescribeDomain(ctx, "dom", "")
				_, _, _ = m.ListPackageVersionAssets(ctx, ref, "1", driver.Page{})
			case 3:
				_, _, _ = m.ListPackageGroups(ctx, driver.DomainRef{Domain: "dom"}, "", driver.Page{})
				_, _, _ = m.ListRepositories(ctx, "", driver.Page{})
			default:
				_, _ = m.CopyPackageVersions(ctx, &driver.CopyInput{
					Ref: ref, SourceRepository: "repo", DestinationRepository: "other", Versions: []string{"1"}, AllowOverwrite: true,
				})
			}
		}(i)
	}

	wg.Wait()

	d, err := m.DescribeDomain(ctx, "dom", "")
	requireNoError(t, err)

	if len(d.Tags) != raceWorkers/5 {
		t.Fatalf("tags after %d concurrent tag calls = %d, want %d (no lost updates)", raceWorkers/5, len(d.Tags), raceWorkers/5)
	}
}
