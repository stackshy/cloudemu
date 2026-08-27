package ecr_test

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsecr "github.com/aws/aws-sdk-go-v2/service/ecr"
	ecrtypes "github.com/aws/aws-sdk-go-v2/service/ecr/types"
)

// TestSDKECRSameManifestTwoTags pushes one manifest under two tags (no explicit
// digest) and asserts real ECR semantics: ONE image, both tags, one 64-hex
// sha256 digest.
func TestSDKECRSameManifestTwoTags(t *testing.T) {
	client := newECRClient(t)
	ctx := context.Background()

	mustCreateRepo(ctx, t, client, "imgs")

	first := mustPutImage(ctx, t, client, "imgs", sampleManifest, "v1")
	second := mustPutImage(ctx, t, client, "imgs", sampleManifest, "v2")

	// Content-addressed: identical manifest -> identical digest across tags.
	digest := aws.ToString(first.Image.ImageId.ImageDigest)
	if digest != aws.ToString(second.Image.ImageId.ImageDigest) {
		t.Fatalf("digest changed across tags: %q vs %q", digest,
			aws.ToString(second.Image.ImageId.ImageDigest))
	}

	if !strings.HasPrefix(digest, "sha256:") || len(strings.TrimPrefix(digest, "sha256:")) != 64 {
		t.Fatalf("digest is not a full sha256: %q", digest)
	}

	described, err := client.DescribeImages(ctx, &awsecr.DescribeImagesInput{
		RepositoryName: aws.String("imgs"),
	})
	if err != nil {
		t.Fatalf("DescribeImages: %v", err)
	}

	if len(described.ImageDetails) != 1 {
		t.Fatalf("want 1 image, got %d: %+v", len(described.ImageDetails), described.ImageDetails)
	}

	tags := described.ImageDetails[0].ImageTags
	if !contains(tags, "v1") || !contains(tags, "v2") || len(tags) != 2 {
		t.Fatalf("want tags [v1 v2], got %+v", tags)
	}
}

// TestSDKECRBatchDeleteByTagUntags verifies that deleting by tag on a
// multi-tagged manifest untags only that tag (echoing digest+tag), while a
// later delete of the last tag removes the manifest.
func TestSDKECRBatchDeleteByTagUntags(t *testing.T) {
	client := newECRClient(t)
	ctx := context.Background()

	mustCreateRepo(ctx, t, client, "imgs")
	mustPutImage(ctx, t, client, "imgs", sampleManifest, "v1")
	put := mustPutImage(ctx, t, client, "imgs", sampleManifest, "v2")
	digest := aws.ToString(put.Image.ImageId.ImageDigest)

	del, err := client.BatchDeleteImage(ctx, &awsecr.BatchDeleteImageInput{
		RepositoryName: aws.String("imgs"),
		ImageIds:       []ecrtypes.ImageIdentifier{{ImageTag: aws.String("v1")}},
	})
	if err != nil {
		t.Fatalf("BatchDeleteImage(v1): %v", err)
	}

	if len(del.Failures) != 0 || len(del.ImageIds) != 1 {
		t.Fatalf("want one deleted id, got ids=%+v failures=%+v", del.ImageIds, del.Failures)
	}

	// Real ECR echoes both the tag removed and the manifest digest.
	if aws.ToString(del.ImageIds[0].ImageTag) != "v1" ||
		aws.ToString(del.ImageIds[0].ImageDigest) != digest {
		t.Fatalf("delete echo missing digest+tag: %+v", del.ImageIds[0])
	}

	// The manifest survives, tagged only [v2].
	described, err := client.DescribeImages(ctx, &awsecr.DescribeImagesInput{
		RepositoryName: aws.String("imgs"),
	})
	if err != nil {
		t.Fatalf("DescribeImages: %v", err)
	}

	if len(described.ImageDetails) != 1 ||
		len(described.ImageDetails[0].ImageTags) != 1 ||
		!contains(described.ImageDetails[0].ImageTags, "v2") {
		t.Fatalf("want single image tagged [v2], got %+v", described.ImageDetails)
	}

	// Deleting the last tag removes the manifest entirely.
	if _, err := client.BatchDeleteImage(ctx, &awsecr.BatchDeleteImageInput{
		RepositoryName: aws.String("imgs"),
		ImageIds:       []ecrtypes.ImageIdentifier{{ImageTag: aws.String("v2")}},
	}); err != nil {
		t.Fatalf("BatchDeleteImage(v2): %v", err)
	}

	listed, err := client.ListImages(ctx, &awsecr.ListImagesInput{RepositoryName: aws.String("imgs")})
	if err != nil {
		t.Fatalf("ListImages: %v", err)
	}

	if len(listed.ImageIds) != 0 {
		t.Fatalf("want empty repository, got %+v", listed.ImageIds)
	}
}

// TestSDKECRListImagesTagStatus verifies the tagStatus filter selects the
// correct subset for TAGGED / UNTAGGED / ANY.
func TestSDKECRListImagesTagStatus(t *testing.T) {
	client := newECRClient(t)
	ctx := context.Background()

	mustCreateRepo(ctx, t, client, "mix")
	mustPutImage(ctx, t, client, "mix", sampleManifest, "tagged")
	// Distinct manifest content, pushed with no tag -> an untagged image.
	mustPutImage(ctx, t, client, "mix", sampleManifest+" ", "")

	cases := []struct {
		status ecrtypes.TagStatus
		want   int
	}{
		{ecrtypes.TagStatusTagged, 1},
		{ecrtypes.TagStatusUntagged, 1},
		{ecrtypes.TagStatusAny, 2},
	}

	for _, tc := range cases {
		out, err := client.ListImages(ctx, &awsecr.ListImagesInput{
			RepositoryName: aws.String("mix"),
			Filter:         &ecrtypes.ListImagesFilter{TagStatus: tc.status},
		})
		if err != nil {
			t.Fatalf("ListImages(%s): %v", tc.status, err)
		}

		if len(out.ImageIds) != tc.want {
			t.Fatalf("tagStatus=%s: want %d ids, got %d (%+v)", tc.status, tc.want, len(out.ImageIds), out.ImageIds)
		}
	}
}

func mustCreateRepo(ctx context.Context, t *testing.T, client *awsecr.Client, name string) {
	t.Helper()

	if _, err := client.CreateRepository(ctx, &awsecr.CreateRepositoryInput{
		RepositoryName: aws.String(name),
	}); err != nil {
		t.Fatalf("CreateRepository(%s): %v", name, err)
	}
}

func mustPutImage(
	ctx context.Context, t *testing.T, client *awsecr.Client, repo, manifest, tag string,
) *awsecr.PutImageOutput {
	t.Helper()

	in := &awsecr.PutImageInput{
		RepositoryName: aws.String(repo),
		ImageManifest:  aws.String(manifest),
	}
	if tag != "" {
		in.ImageTag = aws.String(tag)
	}

	out, err := client.PutImage(ctx, in)
	if err != nil {
		t.Fatalf("PutImage(%s:%s): %v", repo, tag, err)
	}

	return out
}
