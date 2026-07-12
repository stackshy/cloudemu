// Real-SDK round-trip test: the live google.golang.org/api/cloudasset/v1
// REST client drives the in-memory handler end-to-end.

package cloudasset_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/cloudasset/v1"
	"google.golang.org/api/option"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
	dbdriver "github.com/stackshy/cloudemu/v2/services/database/driver"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

func TestSDKCloudAsset(t *testing.T) {
	ctx := context.Background()
	cloudP := cloudemu.NewGCP()
	const projectID = "my-test-project"

	// Seed inventory across services.
	require.NoError(t, cloudP.GCS.CreateBucket(ctx, "audit-logs"))
	require.NoError(t, cloudP.GCS.PutBucketTagging(ctx, "audit-logs",
		map[string]string{"env": "prod", "team": "security"}))

	require.NoError(t, cloudP.GCS.CreateBucket(ctx, "stage-logs"))
	require.NoError(t, cloudP.GCS.PutBucketTagging(ctx, "stage-logs",
		map[string]string{"env": "stage"}))

	require.NoError(t, cloudP.Firestore.CreateTable(ctx, dbdriver.TableConfig{Name: "events", PartitionKey: "pk"}))
	require.NoError(t, cloudP.Firestore.TagResource(ctx, "events", map[string]string{"env": "prod"}))

	_, err := cloudP.VPC.CreateVPC(ctx, netdriver.VPCConfig{
		CIDRBlock: "10.0.0.0/16", Tags: map[string]string{"env": "prod"},
	})
	require.NoError(t, err)

	srv := gcpserver.New(gcpserver.Drivers{
		Storage:           cloudP.GCS,
		Firestore:         cloudP.Firestore,
		Networking:        cloudP.VPC,
		ResourceDiscovery: cloudP.ResourceDiscovery,
		ProjectID:         projectID,
	})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	client, err := cloudasset.NewService(ctx,
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
	)
	require.NoError(t, err)

	scope := "projects/" + projectID

	t.Run("searchAllResources returns everything when filter is empty", func(t *testing.T) {
		out, err := client.V1.SearchAllResources(scope).Do()
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(out.Results), 4, "expect 2 buckets + 1 table + 1 vpc")
	})

	t.Run("searchAllResources filters by service", func(t *testing.T) {
		out, err := client.V1.SearchAllResources(scope).Query("service:storage.googleapis.com").Do()
		require.NoError(t, err)
		assert.Len(t, out.Results, 2)
		for _, r := range out.Results {
			assert.Equal(t, "storage.googleapis.com/Bucket", r.AssetType)
		}
	})

	t.Run("searchAllResources filters by label", func(t *testing.T) {
		out, err := client.V1.SearchAllResources(scope).Query("labels.env:prod").Do()
		require.NoError(t, err)
		// audit-logs bucket + events table + VPC — 3 prod resources.
		assert.Len(t, out.Results, 3)
	})

	t.Run("searchAllResources combines service + label", func(t *testing.T) {
		out, err := client.V1.SearchAllResources(scope).Query("service:storage.googleapis.com labels.env:prod").Do()
		require.NoError(t, err)
		assert.Len(t, out.Results, 1)
		assert.Equal(t, "audit-logs", out.Results[0].DisplayName)
	})

	t.Run("searchAllResources: contradiction returns empty", func(t *testing.T) {
		out, err := client.V1.SearchAllResources(scope).
			Query("service:storage.googleapis.com service:firestore.googleapis.com").Do()
		require.NoError(t, err)
		assert.Empty(t, out.Results)
	})

	t.Run("searchAllResources: conflicting label values returns empty", func(t *testing.T) {
		out, err := client.V1.SearchAllResources(scope).Query("labels.env:prod labels.env:stage").Do()
		require.NoError(t, err)
		assert.Empty(t, out.Results)
	})

	t.Run("searchAllIamPolicies returns empty (out of scope)", func(t *testing.T) {
		out, err := client.V1.SearchAllIamPolicies(scope).Do()
		require.NoError(t, err)
		assert.Empty(t, out.Results)
	})

	t.Run("assets.list returns full inventory", func(t *testing.T) {
		out, err := client.Assets.List(scope).Do()
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(out.Assets), 4)
	})

	t.Run("assets.list with assetTypes narrows results", func(t *testing.T) {
		out, err := client.Assets.List(scope).
			AssetTypes("firestore.googleapis.com/Database").Do()
		require.NoError(t, err)
		assert.Len(t, out.Assets, 1)
	})

	t.Run("assets.list with multiple assetTypes is any-of", func(t *testing.T) {
		out, err := client.Assets.List(scope).
			AssetTypes("firestore.googleapis.com/Database", "storage.googleapis.com/Bucket").Do()
		require.NoError(t, err)
		// 1 firestore + 2 buckets = 3
		assert.Len(t, out.Assets, 3)
	})

	t.Run("exportAssets returns sync operation with results inline", func(t *testing.T) {
		op, err := client.V1.ExportAssets(scope, &cloudasset.ExportAssetsRequest{}).Do()
		require.NoError(t, err)
		assert.True(t, op.Done)
		require.NotNil(t, op.Response)
		assert.Contains(t, string(op.Response), "audit-logs")
	})

	t.Run("returned resource names are GCP-shaped (//service/path)", func(t *testing.T) {
		out, err := client.V1.SearchAllResources(scope).
			Query("assetType:storage.googleapis.com/Bucket").Do()
		require.NoError(t, err)
		require.NotEmpty(t, out.Results)
		for _, r := range out.Results {
			assert.True(t, strings.HasPrefix(r.Name, "//storage.googleapis.com/"),
				"bucket name should start with //storage.googleapis.com/, got %q", r.Name)
		}
	})

	// ----- Feeds CRUD -----
	feedID := "audit-feed"
	feedName := scope + "/feeds/" + feedID

	t.Run("Feeds.Create", func(t *testing.T) {
		created, err := client.Feeds.Create(scope, &cloudasset.CreateFeedRequest{
			FeedId: feedID,
			Feed: &cloudasset.Feed{
				AssetTypes:  []string{"storage.googleapis.com/Bucket"},
				ContentType: "RESOURCE",
				FeedOutputConfig: &cloudasset.FeedOutputConfig{
					PubsubDestination: &cloudasset.PubsubDestination{
						Topic: "projects/" + projectID + "/topics/asset-changes",
					},
				},
			},
		}).Do()
		require.NoError(t, err)
		assert.Equal(t, feedName, created.Name)
	})

	t.Run("Feeds.Create duplicate fails with ALREADY_EXISTS", func(t *testing.T) {
		_, err := client.Feeds.Create(scope, &cloudasset.CreateFeedRequest{
			FeedId: feedID,
			Feed:   &cloudasset.Feed{},
		}).Do()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "ALREADY_EXISTS")
	})

	t.Run("Feeds.List includes the new feed", func(t *testing.T) {
		out, err := client.Feeds.List(scope).Do()
		require.NoError(t, err)
		var names []string
		for _, f := range out.Feeds {
			names = append(names, f.Name)
		}
		assert.Contains(t, names, feedName)
	})

	t.Run("Feeds.Get returns the stored feed", func(t *testing.T) {
		got, err := client.Feeds.Get(feedName).Do()
		require.NoError(t, err)
		assert.Equal(t, []string{"storage.googleapis.com/Bucket"}, got.AssetTypes)
	})

	t.Run("Feeds.Patch updates the feed", func(t *testing.T) {
		patched, err := client.Feeds.Patch(feedName, &cloudasset.UpdateFeedRequest{
			Feed: &cloudasset.Feed{
				AssetTypes: []string{
					"storage.googleapis.com/Bucket",
					"firestore.googleapis.com/Database",
				},
			},
		}).Do()
		require.NoError(t, err)
		assert.Len(t, patched.AssetTypes, 2)
	})

	t.Run("Feeds.Delete removes the feed", func(t *testing.T) {
		_, err := client.Feeds.Delete(feedName).Do()
		require.NoError(t, err)

		out, err := client.Feeds.List(scope).Do()
		require.NoError(t, err)
		var names []string
		for _, f := range out.Feeds {
			names = append(names, f.Name)
		}
		assert.NotContains(t, names, feedName)
	})

	t.Run("Feeds.Get on deleted feed returns NOT_FOUND", func(t *testing.T) {
		_, err := client.Feeds.Get(feedName).Do()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "NOT_FOUND")
	})
}

// TestEmptyProjectIDFallsBackToEngine confirms the Phase-3-style fallback:
// callers wiring ResourceDiscovery without ProjectID get the engine's own
// project ID, not a silently-empty handler.
func TestEmptyProjectIDFallsBackToEngine(t *testing.T) {
	ctx := context.Background()
	cloudP := cloudemu.NewGCP()

	require.NoError(t, cloudP.GCS.CreateBucket(ctx, "bkt"))

	srv := gcpserver.New(gcpserver.Drivers{
		Storage:           cloudP.GCS,
		ResourceDiscovery: cloudP.ResourceDiscovery,
		// ProjectID intentionally omitted.
	})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	client, err := cloudasset.NewService(ctx,
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
	)
	require.NoError(t, err)

	// Use the engine's accountID as scope; the handler must accept it.
	scope := "projects/" + cloudP.ResourceDiscovery.AccountID()
	out, err := client.V1.SearchAllResources(scope).Do()
	require.NoError(t, err)
	assert.NotEmpty(t, out.Results)
}

// TestSDKCloudAsset_BugFixes pins the three issues called out in the
// PR #198 review so they cannot silently regress.
func TestSDKCloudAsset_BugFixes(t *testing.T) {
	ctx := context.Background()
	cloudP := cloudemu.NewGCP()
	const projectID = "my-test-project"

	require.NoError(t, cloudP.GCS.CreateBucket(ctx, "bkt"))
	require.NoError(t, cloudP.GCS.PutBucketTagging(ctx, "bkt", map[string]string{"env": "prod"}))
	require.NoError(t, cloudP.Firestore.CreateTable(ctx, dbdriver.TableConfig{Name: "tbl", PartitionKey: "pk"}))

	srv := gcpserver.New(gcpserver.Drivers{
		Storage:           cloudP.GCS,
		Firestore:         cloudP.Firestore,
		ResourceDiscovery: cloudP.ResourceDiscovery,
		ProjectID:         projectID,
	})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	client, err := cloudasset.NewService(ctx,
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
	)
	require.NoError(t, err)

	scope := "projects/" + projectID

	t.Run("Bug 1: service vs assetType contradiction returns empty", func(t *testing.T) {
		out, err := client.V1.SearchAllResources(scope).
			Query("service:compute.googleapis.com assetType:storage.googleapis.com/Bucket").Do()
		require.NoError(t, err)
		assert.Empty(t, out.Results,
			"compute service + Bucket assetType is impossible — must yield zero")
	})

	t.Run("Bug 1: agreeing service+assetType still returns results", func(t *testing.T) {
		out, err := client.V1.SearchAllResources(scope).
			Query("service:storage.googleapis.com assetType:storage.googleapis.com/Bucket").Do()
		require.NoError(t, err)
		assert.Len(t, out.Results, 1)
	})

	t.Run("Bug 2: Operations.Get returns cached export result", func(t *testing.T) {
		// Trigger an export — caches the operation under its name.
		op, err := client.V1.ExportAssets(scope, &cloudasset.ExportAssetsRequest{}).Do()
		require.NoError(t, err)
		require.NotEmpty(t, op.Name, "operation must have a non-empty name to be pollable")

		// Now poll via Operations.Get. Before the fix this 404'd.
		polled, err := client.Operations.Get(op.Name).Do()
		require.NoError(t, err, "Operations.Get must find the cached export op")
		assert.True(t, polled.Done)
		assert.Equal(t, op.Name, polled.Name)
		assert.NotEmpty(t, polled.Response, "polled op must carry the response payload")
	})

	t.Run("Bug 2: Operations.Get on unknown name still 404s", func(t *testing.T) {
		_, err := client.Operations.Get("projects/" + projectID + "/operations/cloudemu-export-bogus").Do()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "NOT_FOUND")
	})

	t.Run("Bug 3: feed id containing '/' is rejected", func(t *testing.T) {
		// Construct a malformed feed name. The SDK's Get builds the URL
		// from the name as-is.
		_, err := client.Feeds.Get(scope + "/feeds/nested/path").Do()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "NOT_FOUND")
	})
}

// TestSDKCloudAsset_BatchGetAssetsHistory fills the coverage gap from the
// review — the endpoint was implemented but not exercised by any test.
func TestSDKCloudAsset_BatchGetAssetsHistory(t *testing.T) {
	ctx := context.Background()
	cloudP := cloudemu.NewGCP()
	const projectID = "history-test"

	require.NoError(t, cloudP.GCS.CreateBucket(ctx, "h-bkt"))

	srv := gcpserver.New(gcpserver.Drivers{
		Storage:           cloudP.GCS,
		ResourceDiscovery: cloudP.ResourceDiscovery,
		ProjectID:         projectID,
	})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	client, err := cloudasset.NewService(ctx,
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
	)
	require.NoError(t, err)

	out, err := client.V1.BatchGetAssetsHistory("projects/" + projectID).Do()
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(out.Assets), 1,
		"batchGetAssetsHistory should return the current snapshot wrapped in a temporal-asset entry")
	for _, a := range out.Assets {
		require.NotNil(t, a.Window, "each TemporalAsset must carry a window")
		assert.NotEmpty(t, a.Window.StartTime)
	}
}
