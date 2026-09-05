package cloudfront_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awscf "github.com/aws/aws-sdk-go-v2/service/cloudfront"
	cftypes "github.com/aws/aws-sdk-go-v2/service/cloudfront/types"
	"github.com/aws/smithy-go"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

func newCloudFrontClient(t *testing.T) *awscf.Client {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.DriversFrom(cloud))

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return awscf.NewFromConfig(cfg, func(o *awscf.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})
}

// sampleDistConfig builds a minimal-but-realistic distribution config with an
// S3 origin and a legacy ForwardedValues default cache behavior.
func sampleDistConfig(ref, comment string, enabled bool) *cftypes.DistributionConfig {
	return &cftypes.DistributionConfig{
		CallerReference: aws.String(ref),
		Comment:         aws.String(comment),
		Enabled:         aws.Bool(enabled),
		PriceClass:      cftypes.PriceClassPriceClassAll,
		HttpVersion:     cftypes.HttpVersionHttp2,
		IsIPV6Enabled:   aws.Bool(true),
		Origins: &cftypes.Origins{
			Quantity: aws.Int32(1),
			Items: []cftypes.Origin{{
				Id:             aws.String("s3-origin"),
				DomainName:     aws.String("example-bucket.s3.amazonaws.com"),
				S3OriginConfig: &cftypes.S3OriginConfig{OriginAccessIdentity: aws.String("")},
			}},
		},
		DefaultCacheBehavior: &cftypes.DefaultCacheBehavior{
			TargetOriginId:       aws.String("s3-origin"),
			ViewerProtocolPolicy: cftypes.ViewerProtocolPolicyAllowAll,
			MinTTL:               aws.Int64(0),
			ForwardedValues: &cftypes.ForwardedValues{
				QueryString: aws.Bool(false),
				Cookies:     &cftypes.CookiePreference{Forward: cftypes.ItemSelectionNone},
			},
		},
	}
}

func apiErrorCode(t *testing.T, err error) string {
	t.Helper()

	var ae smithy.APIError
	if !errors.As(err, &ae) {
		t.Fatalf("error is not an smithy.APIError: %v", err)
	}

	return ae.ErrorCode()
}

// TestCloudFrontLifecycle exercises the full real-SDK flow: create → get
// (Deployed + ETag + domain) → get-config → update (disable, If-Match) → delete
// (If-Match), plus the delete-before-disable and stale-ETag guards.
func TestCloudFrontLifecycle(t *testing.T) {
	ctx := context.Background()
	c := newCloudFrontClient(t)

	created, err := c.CreateDistribution(ctx, &awscf.CreateDistributionInput{
		DistributionConfig: sampleDistConfig("tf-ref-1", "initial", true),
	})
	if err != nil {
		t.Fatalf("CreateDistribution: %v", err)
	}

	dist := created.Distribution
	if aws.ToString(dist.Status) != "Deployed" {
		t.Errorf("status = %q, want Deployed (Terraform blocks on this)", aws.ToString(dist.Status))
	}

	if aws.ToString(created.ETag) == "" {
		t.Error("create response missing ETag header")
	}

	id := aws.ToString(dist.Id)
	if len(id) != 14 || id[0] != 'E' {
		t.Errorf("id = %q, want 14-char E-prefixed", id)
	}

	// GetDistribution: Deployed, ETag, .cloudfront.net domain, config round-trip.
	got, err := c.GetDistribution(ctx, &awscf.GetDistributionInput{Id: dist.Id})
	if err != nil {
		t.Fatalf("GetDistribution: %v", err)
	}

	if aws.ToString(got.ETag) == "" {
		t.Error("get response missing ETag header")
	}

	gcfg := got.Distribution.DistributionConfig
	if aws.ToString(gcfg.Comment) != "initial" {
		t.Errorf("comment = %q, want initial", aws.ToString(gcfg.Comment))
	}

	if gcfg.PriceClass != cftypes.PriceClassPriceClassAll {
		t.Errorf("priceClass = %q, want PriceClass_All", gcfg.PriceClass)
	}

	if aws.ToInt32(gcfg.Origins.Quantity) != 1 ||
		aws.ToString(gcfg.Origins.Items[0].Id) != "s3-origin" {
		t.Errorf("origins not round-tripped: %+v", gcfg.Origins)
	}

	if aws.ToString(gcfg.DefaultCacheBehavior.TargetOriginId) != "s3-origin" {
		t.Errorf("default cache behavior not round-tripped: %+v", gcfg.DefaultCacheBehavior)
	}

	// GetDistributionConfig returns config + ETag.
	gc, err := c.GetDistributionConfig(ctx, &awscf.GetDistributionConfigInput{Id: dist.Id})
	if err != nil {
		t.Fatalf("GetDistributionConfig: %v", err)
	}

	if !aws.ToBool(gc.DistributionConfig.Enabled) {
		t.Error("config should be enabled")
	}

	// Deleting an enabled distribution fails.
	_, err = c.DeleteDistribution(ctx, &awscf.DeleteDistributionInput{Id: dist.Id, IfMatch: gc.ETag})
	if code := apiErrorCode(t, err); code != "DistributionNotDisabled" {
		t.Fatalf("delete-enabled code = %q, want DistributionNotDisabled", code)
	}

	// A stale ETag on update is rejected.
	disabled := sampleDistConfig("tf-ref-1", "initial", false)
	_, err = c.UpdateDistribution(ctx, &awscf.UpdateDistributionInput{
		Id: dist.Id, IfMatch: aws.String("STALEETAG0000"), DistributionConfig: disabled,
	})
	if code := apiErrorCode(t, err); code != "PreconditionFailed" {
		t.Fatalf("stale-etag code = %q, want PreconditionFailed", code)
	}

	// Disable with the current ETag.
	updated, err := c.UpdateDistribution(ctx, &awscf.UpdateDistributionInput{
		Id: dist.Id, IfMatch: gc.ETag, DistributionConfig: disabled,
	})
	if err != nil {
		t.Fatalf("UpdateDistribution: %v", err)
	}

	if aws.ToString(updated.ETag) == aws.ToString(gc.ETag) {
		t.Error("ETag did not rotate on update")
	}

	// Delete with the rotated ETag.
	if _, err := c.DeleteDistribution(ctx, &awscf.DeleteDistributionInput{
		Id: dist.Id, IfMatch: updated.ETag,
	}); err != nil {
		t.Fatalf("DeleteDistribution: %v", err)
	}

	// Gone.
	_, err = c.GetDistribution(ctx, &awscf.GetDistributionInput{Id: dist.Id})
	if code := apiErrorCode(t, err); code != "NoSuchDistribution" {
		t.Fatalf("get-after-delete code = %q, want NoSuchDistribution", code)
	}
}

// TestCloudFrontMinimalConfigNormalized is the regression guard for the
// terraform-provider-aws crash: a minimal config that omits the server-side
// default blocks (Aliases, OriginGroups, CacheBehaviors, CustomErrorResponses,
// Logging, Restrictions, ViewerCertificate, and the DefaultCacheBehavior
// TrustedSigners/TrustedKeyGroups/AllowedMethods/Function+Lambda associations)
// must read back COMPLETE. Real CloudFront always returns those blocks, and the
// provider dereferences them without a nil check — a missing block segfaults it.
func TestCloudFrontMinimalConfigNormalized(t *testing.T) {
	ctx := context.Background()
	c := newCloudFrontClient(t)

	created, err := c.CreateDistribution(ctx, &awscf.CreateDistributionInput{
		DistributionConfig: sampleDistConfig("norm-ref", "normalize", true),
	})
	if err != nil {
		t.Fatalf("CreateDistribution: %v", err)
	}

	got, err := c.GetDistribution(ctx, &awscf.GetDistributionInput{Id: created.Distribution.Id})
	if err != nil {
		t.Fatalf("GetDistribution: %v", err)
	}

	cfg := got.Distribution.DistributionConfig

	if cfg.Aliases == nil || aws.ToInt32(cfg.Aliases.Quantity) != 0 {
		t.Errorf("Aliases not defaulted: %+v", cfg.Aliases)
	}

	if cfg.OriginGroups == nil || aws.ToInt32(cfg.OriginGroups.Quantity) != 0 {
		t.Errorf("OriginGroups not defaulted (the provider crash): %+v", cfg.OriginGroups)
	}

	if cfg.CacheBehaviors == nil || aws.ToInt32(cfg.CacheBehaviors.Quantity) != 0 {
		t.Errorf("CacheBehaviors not defaulted: %+v", cfg.CacheBehaviors)
	}

	if cfg.CustomErrorResponses == nil || aws.ToInt32(cfg.CustomErrorResponses.Quantity) != 0 {
		t.Errorf("CustomErrorResponses not defaulted: %+v", cfg.CustomErrorResponses)
	}

	if cfg.Restrictions == nil || cfg.Restrictions.GeoRestriction == nil ||
		cfg.Restrictions.GeoRestriction.RestrictionType != cftypes.GeoRestrictionTypeNone {
		t.Errorf("Restrictions not defaulted to none: %+v", cfg.Restrictions)
	}

	if cfg.ViewerCertificate == nil || !aws.ToBool(cfg.ViewerCertificate.CloudFrontDefaultCertificate) {
		t.Errorf("ViewerCertificate not defaulted: %+v", cfg.ViewerCertificate)
	}

	if cfg.PriceClass != cftypes.PriceClassPriceClassAll {
		t.Errorf("PriceClass = %q, want PriceClass_All", cfg.PriceClass)
	}

	if cfg.HttpVersion != cftypes.HttpVersionHttp2 {
		t.Errorf("HttpVersion = %q, want http2", cfg.HttpVersion)
	}

	if !aws.ToBool(cfg.IsIPV6Enabled) {
		t.Error("IsIPV6Enabled not preserved as true")
	}

	dcb := cfg.DefaultCacheBehavior
	if dcb.TrustedSigners == nil || aws.ToBool(dcb.TrustedSigners.Enabled) ||
		aws.ToInt32(dcb.TrustedSigners.Quantity) != 0 {
		t.Errorf("DCB TrustedSigners not defaulted disabled/0: %+v", dcb.TrustedSigners)
	}

	if dcb.TrustedKeyGroups == nil || aws.ToBool(dcb.TrustedKeyGroups.Enabled) ||
		aws.ToInt32(dcb.TrustedKeyGroups.Quantity) != 0 {
		t.Errorf("DCB TrustedKeyGroups not defaulted disabled/0: %+v", dcb.TrustedKeyGroups)
	}

	if dcb.AllowedMethods == nil || dcb.AllowedMethods.CachedMethods == nil {
		t.Errorf("DCB AllowedMethods/CachedMethods not defaulted: %+v", dcb.AllowedMethods)
	}

	if dcb.LambdaFunctionAssociations == nil || aws.ToInt32(dcb.LambdaFunctionAssociations.Quantity) != 0 {
		t.Errorf("DCB LambdaFunctionAssociations not defaulted: %+v", dcb.LambdaFunctionAssociations)
	}

	if dcb.FunctionAssociations == nil || aws.ToInt32(dcb.FunctionAssociations.Quantity) != 0 {
		t.Errorf("DCB FunctionAssociations not defaulted: %+v", dcb.FunctionAssociations)
	}

	// Caller values must be preserved exactly, not overwritten by defaults.
	if aws.ToString(dcb.TargetOriginId) != "s3-origin" {
		t.Errorf("caller TargetOriginId lost: %q", aws.ToString(dcb.TargetOriginId))
	}

	if dcb.ForwardedValues == nil || aws.ToBool(dcb.ForwardedValues.QueryString) {
		t.Errorf("caller ForwardedValues.QueryString lost: %+v", dcb.ForwardedValues)
	}
}

func TestCloudFrontDuplicateCallerReference(t *testing.T) {
	ctx := context.Background()
	c := newCloudFrontClient(t)

	if _, err := c.CreateDistribution(ctx, &awscf.CreateDistributionInput{
		DistributionConfig: sampleDistConfig("dup-ref", "one", true),
	}); err != nil {
		t.Fatalf("first create: %v", err)
	}

	_, err := c.CreateDistribution(ctx, &awscf.CreateDistributionInput{
		DistributionConfig: sampleDistConfig("dup-ref", "two", true),
	})
	if code := apiErrorCode(t, err); code != "DistributionAlreadyExists" {
		t.Fatalf("dup code = %q, want DistributionAlreadyExists", code)
	}
}

func TestCloudFrontListDistributions(t *testing.T) {
	ctx := context.Background()
	c := newCloudFrontClient(t)

	for _, ref := range []string{"l-1", "l-2"} {
		if _, err := c.CreateDistribution(ctx, &awscf.CreateDistributionInput{
			DistributionConfig: sampleDistConfig(ref, ref, true),
		}); err != nil {
			t.Fatalf("create %s: %v", ref, err)
		}
	}

	out, err := c.ListDistributions(ctx, &awscf.ListDistributionsInput{})
	if err != nil {
		t.Fatalf("ListDistributions: %v", err)
	}

	if aws.ToInt32(out.DistributionList.Quantity) != 2 || len(out.DistributionList.Items) != 2 {
		t.Fatalf("list quantity = %d, items = %d, want 2/2",
			aws.ToInt32(out.DistributionList.Quantity), len(out.DistributionList.Items))
	}

	if aws.ToString(out.DistributionList.Items[0].DomainName) == "" {
		t.Error("summary missing domain name")
	}
}

func TestCloudFrontInvalidation(t *testing.T) {
	ctx := context.Background()
	c := newCloudFrontClient(t)

	created, err := c.CreateDistribution(ctx, &awscf.CreateDistributionInput{
		DistributionConfig: sampleDistConfig("inv-ref", "inv", true),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	id := created.Distribution.Id

	inv, err := c.CreateInvalidation(ctx, &awscf.CreateInvalidationInput{
		DistributionId: id,
		InvalidationBatch: &cftypes.InvalidationBatch{
			CallerReference: aws.String("inv-batch-1"),
			Paths: &cftypes.Paths{
				Quantity: aws.Int32(1),
				Items:    []string{"/*"},
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateInvalidation: %v", err)
	}

	if aws.ToString(inv.Invalidation.Status) != "Completed" {
		t.Errorf("invalidation status = %q, want Completed", aws.ToString(inv.Invalidation.Status))
	}

	got, err := c.GetInvalidation(ctx, &awscf.GetInvalidationInput{
		DistributionId: id, Id: inv.Invalidation.Id,
	})
	if err != nil {
		t.Fatalf("GetInvalidation: %v", err)
	}

	if aws.ToInt32(got.Invalidation.InvalidationBatch.Paths.Quantity) != 1 {
		t.Errorf("invalidation paths not round-tripped: %+v", got.Invalidation.InvalidationBatch)
	}
}

func TestCloudFrontTags(t *testing.T) {
	ctx := context.Background()
	c := newCloudFrontClient(t)

	created, err := c.CreateDistributionWithTags(ctx, &awscf.CreateDistributionWithTagsInput{
		DistributionConfigWithTags: &cftypes.DistributionConfigWithTags{
			DistributionConfig: sampleDistConfig("tag-ref", "tagged", true),
			Tags: &cftypes.Tags{Items: []cftypes.Tag{
				{Key: aws.String("env"), Value: aws.String("prod")},
			}},
		},
	})
	if err != nil {
		t.Fatalf("CreateDistributionWithTags: %v", err)
	}

	arn := created.Distribution.ARN

	out, err := c.ListTagsForResource(ctx, &awscf.ListTagsForResourceInput{Resource: arn})
	if err != nil {
		t.Fatalf("ListTagsForResource: %v", err)
	}

	if len(out.Tags.Items) != 1 || aws.ToString(out.Tags.Items[0].Key) != "env" {
		t.Fatalf("tags = %+v, want env=prod", out.Tags.Items)
	}

	if _, err := c.TagResource(ctx, &awscf.TagResourceInput{
		Resource: arn,
		Tags:     &cftypes.Tags{Items: []cftypes.Tag{{Key: aws.String("team"), Value: aws.String("web")}}},
	}); err != nil {
		t.Fatalf("TagResource: %v", err)
	}

	if _, err := c.UntagResource(ctx, &awscf.UntagResourceInput{
		Resource: arn,
		TagKeys:  &cftypes.TagKeys{Items: []string{"env"}},
	}); err != nil {
		t.Fatalf("UntagResource: %v", err)
	}

	out, err = c.ListTagsForResource(ctx, &awscf.ListTagsForResourceInput{Resource: arn})
	if err != nil {
		t.Fatalf("ListTagsForResource after untag: %v", err)
	}

	if len(out.Tags.Items) != 1 || aws.ToString(out.Tags.Items[0].Key) != "team" {
		t.Fatalf("tags after untag = %+v, want only team=web", out.Tags.Items)
	}
}
