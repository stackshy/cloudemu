package artifactregistry_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	ar "google.golang.org/api/artifactregistry/v1"
	"google.golang.org/api/option"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

// newARServiceFixedClock boots an Artifact Registry SDK client whose server uses
// a frozen clock, so every repository gets the same second-granular createTime /
// updateTime — the tie condition that exposed the non-deterministic sort.
func newARServiceFixedClock(t *testing.T) *ar.Service {
	t.Helper()

	clock := config.NewFakeClock(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
	cloud := cloudemu.NewGCP(config.WithClock(clock))
	srv := gcpserver.New(gcpserver.Drivers{ArtifactRegistry: cloud.ArtifactRegistry})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	svc, err := ar.NewService(context.Background(),
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("artifactregistry.NewService: %v", err)
	}

	return svc
}

// paginateAllRepoNames walks every page of repositories.list one at a time under
// the given orderBy, returning the names in page order.
func paginateAllRepoNames(t *testing.T, svc *ar.Service, orderBy string) []string {
	t.Helper()

	ctx := context.Background()

	var (
		names []string
		token string
	)

	const maxPages = 100

	for range maxPages {
		resp, err := svc.Projects.Locations.Repositories.List(testParent).
			OrderBy(orderBy).PageSize(1).PageToken(token).Context(ctx).Do()
		if err != nil {
			t.Fatalf("List orderBy=%q: %v", orderBy, err)
		}

		for _, repo := range resp.Repositories {
			names = append(names, repo.Name)
		}

		token = resp.NextPageToken
		if token == "" {
			return names
		}
	}

	t.Fatalf("orderBy=%q did not terminate within %d pages", orderBy, maxPages)

	return nil
}

// assertExactlyOnceAndStable checks that one-at-a-time pagination yields each of
// wantIDs exactly once (no dup/skip) and that the page order is identical across
// two independent list sweeps.
func assertExactlyOnceAndStable(t *testing.T, svc *ar.Service, orderBy string, wantIDs []string) {
	t.Helper()

	first := paginateAllRepoNames(t, svc, orderBy)

	seen := make(map[string]int, len(first))
	for _, n := range first {
		seen[n]++
	}

	if len(seen) != len(wantIDs) || len(first) != len(wantIDs) {
		t.Fatalf("orderBy=%q paged %d names (%d unique), want %d exactly once: %v",
			orderBy, len(first), len(seen), len(wantIDs), first)
	}

	for _, id := range wantIDs {
		if got := seen[testParent+"/repositories/"+id]; got != 1 {
			t.Fatalf("orderBy=%q: repo %q appeared %d times, want exactly 1", orderBy, id, got)
		}
	}

	second := paginateAllRepoNames(t, svc, orderBy)
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("orderBy=%q order not stable across calls: pos %d %q vs %q",
				orderBy, i, first[i], second[i])
		}
	}
}

// TestSDKArtifactRegistryOrderByTiesDeterministic guards the HIGH pagination bug:
// createTime/updateTime are only second-granular, so repos created in the same
// second tie on the sort key. Without a stable secondary key the map-backed list
// resolved ties differently each call, so offset paging duplicated/skipped repos.
// The name tiebreaker must make one-at-a-time paging return each repo exactly
// once, in an order that is identical across repeated identical requests.
func TestSDKArtifactRegistryOrderByTiesDeterministic(t *testing.T) {
	svc := newARServiceFixedClock(t)
	ctx := context.Background()

	ids := []string{"repo-a", "repo-b", "repo-c", "repo-d", "repo-e"}
	for _, id := range ids {
		if _, err := svc.Projects.Locations.Repositories.Create(testParent, &ar.Repository{Format: "DOCKER"}).
			RepositoryId(id).Context(ctx).Do(); err != nil {
			t.Fatalf("Create %s: %v", id, err)
		}
	}

	assertExactlyOnceAndStable(t, svc, "createTime", ids)
	assertExactlyOnceAndStable(t, svc, "updateTime", ids)
}
