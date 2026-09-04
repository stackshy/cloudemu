package ecr_test

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsecr "github.com/aws/aws-sdk-go-v2/service/ecr"
	ecrtypes "github.com/aws/aws-sdk-go-v2/service/ecr/types"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// newECRClientWithClock is newECRClient with a caller-controlled clock, so
// age-based lifecycle rules (sinceImagePushed) can be tested deterministically.
func newECRClientWithClock(t *testing.T, clock config.Clock) *awsecr.Client {
	t.Helper()

	cloud := cloudemu.NewAWS(config.WithClock(clock))
	srv := awsserver.New(awsserver.Drivers{ECR: cloud.ECR})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return awsecr.NewFromConfig(cfg, func(o *awsecr.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})
}

// countRulePolicyDoc keeps only the 2 most recently pushed tagged images.
const countRulePolicyDoc = `{"rules":[{"rulePriority":1,"description":"keep 2",` +
	`"selection":{"tagStatus":"tagged","countType":"imageCountMoreThan","countNumber":2},` +
	`"action":{"type":"expire"}}]}`

// TestSDKECRLifecyclePolicyPreviewCountRule exercises the real end-to-end flow:
// PutLifecyclePolicy with a count rule, push more images than the rule keeps,
// then StartLifecyclePolicyPreview + GetLifecyclePolicyPreview must report the
// oldest excess image as expiring, with its digest, tags, and the priority of
// the rule that matched it.
func TestSDKECRLifecyclePolicyPreviewCountRule(t *testing.T) {
	// A FakeClock and an advance between each push give the 3 images distinct,
	// ordered PushedAt timestamps, so which one is "oldest" is unambiguous
	// (real-clock pushes can land in the same second and tie).
	fc := config.NewFakeClock(time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC))
	client := newECRClientWithClock(t, fc)
	ctx := context.Background()

	mustCreateRepo(ctx, t, client, "preview-count-repo")

	if _, err := client.PutLifecyclePolicy(ctx, &awsecr.PutLifecyclePolicyInput{
		RepositoryName:      aws.String("preview-count-repo"),
		LifecyclePolicyText: aws.String(countRulePolicyDoc),
	}); err != nil {
		t.Fatalf("PutLifecyclePolicy: %v", err)
	}

	oldest := mustPutImage(ctx, t, client, "preview-count-repo", sampleManifest+"v1", "v1")
	fc.Advance(time.Minute)
	mustPutImage(ctx, t, client, "preview-count-repo", sampleManifest+"v2", "v2")
	fc.Advance(time.Minute)
	mustPutImage(ctx, t, client, "preview-count-repo", sampleManifest+"v3", "v3")

	start, err := client.StartLifecyclePolicyPreview(ctx, &awsecr.StartLifecyclePolicyPreviewInput{
		RepositoryName: aws.String("preview-count-repo"),
	})
	if err != nil {
		t.Fatalf("StartLifecyclePolicyPreview: %v", err)
	}

	if start.Status != ecrtypes.LifecyclePolicyPreviewStatusComplete {
		t.Fatalf("Start status = %v, want COMPLETE", start.Status)
	}

	got, err := client.GetLifecyclePolicyPreview(ctx, &awsecr.GetLifecyclePolicyPreviewInput{
		RepositoryName: aws.String("preview-count-repo"),
	})
	if err != nil {
		t.Fatalf("GetLifecyclePolicyPreview: %v", err)
	}

	if got.Status != ecrtypes.LifecyclePolicyPreviewStatusComplete {
		t.Fatalf("Get status = %v, want COMPLETE", got.Status)
	}

	if aws.ToInt32(got.Summary.ExpiringImageTotalCount) != 1 {
		t.Fatalf("expiringImageTotalCount = %d, want 1", aws.ToInt32(got.Summary.ExpiringImageTotalCount))
	}

	if len(got.PreviewResults) != 1 {
		t.Fatalf("previewResults = %d entries, want 1", len(got.PreviewResults))
	}

	result := got.PreviewResults[0]

	if aws.ToString(result.ImageDigest) != aws.ToString(oldest.Image.ImageId.ImageDigest) {
		t.Fatalf("expiring digest = %s, want the oldest image %s",
			aws.ToString(result.ImageDigest), aws.ToString(oldest.Image.ImageId.ImageDigest))
	}

	if len(result.ImageTags) != 1 || result.ImageTags[0] != "v1" {
		t.Fatalf("expiring imageTags = %v, want [v1]", result.ImageTags)
	}

	if aws.ToInt32(result.AppliedRulePriority) != 1 {
		t.Fatalf("appliedRulePriority = %d, want 1", aws.ToInt32(result.AppliedRulePriority))
	}

	if result.Action == nil || result.Action.Type != ecrtypes.ImageActionTypeExpire {
		t.Fatalf("action = %v, want EXPIRE", result.Action)
	}
}

// TestSDKECRLifecyclePolicyPreviewAgeRule exercises a sinceImagePushed rule on
// a FakeClock: images pushed more than the threshold ago must be reported as
// expiring; a recently pushed image must not.
func TestSDKECRLifecyclePolicyPreviewAgeRule(t *testing.T) {
	fc := config.NewFakeClock(time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC))
	client := newECRClientWithClock(t, fc)
	ctx := context.Background()

	mustCreateRepo(ctx, t, client, "preview-age-repo")

	const agePolicyDoc = `{"rules":[{"rulePriority":1,"description":"expire old",` +
		`"selection":{"tagStatus":"any","countType":"sinceImagePushed","countNumber":30},` +
		`"action":{"type":"expire"}}]}`

	if _, err := client.PutLifecyclePolicy(ctx, &awsecr.PutLifecyclePolicyInput{
		RepositoryName:      aws.String("preview-age-repo"),
		LifecyclePolicyText: aws.String(agePolicyDoc),
	}); err != nil {
		t.Fatalf("PutLifecyclePolicy: %v", err)
	}

	mustPutImage(ctx, t, client, "preview-age-repo", sampleManifest+"old", "old")
	fc.Advance(31 * 24 * time.Hour)
	mustPutImage(ctx, t, client, "preview-age-repo", sampleManifest+"new", "new")

	if _, err := client.StartLifecyclePolicyPreview(ctx, &awsecr.StartLifecyclePolicyPreviewInput{
		RepositoryName: aws.String("preview-age-repo"),
	}); err != nil {
		t.Fatalf("StartLifecyclePolicyPreview: %v", err)
	}

	got, err := client.GetLifecyclePolicyPreview(ctx, &awsecr.GetLifecyclePolicyPreviewInput{
		RepositoryName: aws.String("preview-age-repo"),
	})
	if err != nil {
		t.Fatalf("GetLifecyclePolicyPreview: %v", err)
	}

	if len(got.PreviewResults) != 1 {
		t.Fatalf("previewResults = %d entries, want 1", len(got.PreviewResults))
	}

	if got.PreviewResults[0].ImageTags[0] != "old" {
		t.Fatalf("expiring image tag = %v, want [old]", got.PreviewResults[0].ImageTags)
	}
}

// TestSDKECRLifecyclePolicyPreviewOverride exercises StartLifecyclePolicyPreview's
// optional lifecyclePolicyText: a repository with NO stored policy can still be
// previewed by supplying an ad-hoc policy, and doing so does not persist it (a
// subsequent GetLifecyclePolicy still reports LifecyclePolicyNotFoundException).
func TestSDKECRLifecyclePolicyPreviewOverride(t *testing.T) {
	client := newECRClient(t)
	ctx := context.Background()

	mustCreateRepo(ctx, t, client, "preview-override-repo")

	mustPutImage(ctx, t, client, "preview-override-repo", sampleManifest+"a", "a")
	mustPutImage(ctx, t, client, "preview-override-repo", sampleManifest+"b", "b")
	mustPutImage(ctx, t, client, "preview-override-repo", sampleManifest+"c", "c")

	start, err := client.StartLifecyclePolicyPreview(ctx, &awsecr.StartLifecyclePolicyPreviewInput{
		RepositoryName:      aws.String("preview-override-repo"),
		LifecyclePolicyText: aws.String(countRulePolicyDoc),
	})
	if err != nil {
		t.Fatalf("StartLifecyclePolicyPreview with override: %v", err)
	}

	if aws.ToString(start.LifecyclePolicyText) != countRulePolicyDoc {
		t.Fatalf("Start echoed lifecyclePolicyText = %q, want the override", aws.ToString(start.LifecyclePolicyText))
	}

	got, err := client.GetLifecyclePolicyPreview(ctx, &awsecr.GetLifecyclePolicyPreviewInput{
		RepositoryName: aws.String("preview-override-repo"),
	})
	if err != nil {
		t.Fatalf("GetLifecyclePolicyPreview: %v", err)
	}

	if len(got.PreviewResults) != 1 {
		t.Fatalf("previewResults = %d entries, want 1", len(got.PreviewResults))
	}

	_, err = client.GetLifecyclePolicy(ctx, &awsecr.GetLifecyclePolicyInput{
		RepositoryName: aws.String("preview-override-repo"),
	})

	var lcNotFound *ecrtypes.LifecyclePolicyNotFoundException
	if !errors.As(err, &lcNotFound) {
		t.Fatalf("GetLifecyclePolicy after override preview: want LifecyclePolicyNotFoundException, got %v", err)
	}
}

// TestSDKECRLifecyclePolicyPreviewNoPolicy asserts that previewing a repository
// with no stored policy and no override reports LifecyclePolicyNotFoundException.
func TestSDKECRLifecyclePolicyPreviewNoPolicy(t *testing.T) {
	client := newECRClient(t)
	ctx := context.Background()

	mustCreateRepo(ctx, t, client, "preview-no-policy-repo")

	_, err := client.StartLifecyclePolicyPreview(ctx, &awsecr.StartLifecyclePolicyPreviewInput{
		RepositoryName: aws.String("preview-no-policy-repo"),
	})

	var lcNotFound *ecrtypes.LifecyclePolicyNotFoundException
	if !errors.As(err, &lcNotFound) {
		t.Fatalf("Start with no policy: want LifecyclePolicyNotFoundException, got %v", err)
	}
}

// TestSDKECRLifecyclePolicyPreviewMissingRepo asserts Start/Get on a repository
// that does not exist both report RepositoryNotFoundException.
func TestSDKECRLifecyclePolicyPreviewMissingRepo(t *testing.T) {
	client := newECRClient(t)
	ctx := context.Background()

	_, err := client.StartLifecyclePolicyPreview(ctx, &awsecr.StartLifecyclePolicyPreviewInput{
		RepositoryName: aws.String("ghost-preview-repo"),
	})

	var repoNotFound *ecrtypes.RepositoryNotFoundException
	if !errors.As(err, &repoNotFound) {
		t.Fatalf("Start on missing repo: want RepositoryNotFoundException, got %v", err)
	}

	_, err = client.GetLifecyclePolicyPreview(ctx, &awsecr.GetLifecyclePolicyPreviewInput{
		RepositoryName: aws.String("ghost-preview-repo"),
	})
	if !errors.As(err, &repoNotFound) {
		t.Fatalf("Get on missing repo: want RepositoryNotFoundException, got %v", err)
	}
}

// TestSDKECRGetLifecyclePolicyPreviewWithoutStart asserts that Get before any
// Start call reports LifecyclePolicyPreviewNotFoundException, not a stale or
// empty success response.
func TestSDKECRGetLifecyclePolicyPreviewWithoutStart(t *testing.T) {
	client := newECRClient(t)
	ctx := context.Background()

	mustCreateRepo(ctx, t, client, "preview-unstarted-repo")

	if _, err := client.PutLifecyclePolicy(ctx, &awsecr.PutLifecyclePolicyInput{
		RepositoryName:      aws.String("preview-unstarted-repo"),
		LifecyclePolicyText: aws.String(countRulePolicyDoc),
	}); err != nil {
		t.Fatalf("PutLifecyclePolicy: %v", err)
	}

	_, err := client.GetLifecyclePolicyPreview(ctx, &awsecr.GetLifecyclePolicyPreviewInput{
		RepositoryName: aws.String("preview-unstarted-repo"),
	})

	var previewNotFound *ecrtypes.LifecyclePolicyPreviewNotFoundException
	if !errors.As(err, &previewNotFound) {
		t.Fatalf("Get without Start: want LifecyclePolicyPreviewNotFoundException, got %v", err)
	}
}

// TestSDKECRLifecyclePolicyPreviewStaleAfterDelete guards against a
// delete-then-recreate confusing the wire handler's Start/Get preview cache
// (keyed by repository name): deleting the repository must invalidate any
// cached preview, so a repository recreated with the same name starts with no
// preview rather than replaying the deleted repository's stale result.
func TestSDKECRLifecyclePolicyPreviewStaleAfterDelete(t *testing.T) {
	client := newECRClient(t)
	ctx := context.Background()

	const repoName = "preview-stale-repo"

	mustCreateRepo(ctx, t, client, repoName)

	if _, err := client.PutLifecyclePolicy(ctx, &awsecr.PutLifecyclePolicyInput{
		RepositoryName:      aws.String(repoName),
		LifecyclePolicyText: aws.String(countRulePolicyDoc),
	}); err != nil {
		t.Fatalf("PutLifecyclePolicy: %v", err)
	}

	for i := range 3 {
		mustPutImage(ctx, t, client, repoName, fmt.Sprintf("%s-%d", sampleManifest, i), fmt.Sprintf("v%d", i))
	}

	if _, err := client.StartLifecyclePolicyPreview(ctx, &awsecr.StartLifecyclePolicyPreviewInput{
		RepositoryName: aws.String(repoName),
	}); err != nil {
		t.Fatalf("StartLifecyclePolicyPreview: %v", err)
	}

	if _, err := client.DeleteRepository(ctx, &awsecr.DeleteRepositoryInput{
		RepositoryName: aws.String(repoName),
		Force:          true,
	}); err != nil {
		t.Fatalf("DeleteRepository: %v", err)
	}

	mustCreateRepo(ctx, t, client, repoName)

	_, err := client.GetLifecyclePolicyPreview(ctx, &awsecr.GetLifecyclePolicyPreviewInput{
		RepositoryName: aws.String(repoName),
	})

	var previewNotFound *ecrtypes.LifecyclePolicyPreviewNotFoundException
	if !errors.As(err, &previewNotFound) {
		t.Fatalf("Get after delete+recreate: want LifecyclePolicyPreviewNotFoundException (no stale replay), got %v", err)
	}
}
