package glue_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsglue "github.com/aws/aws-sdk-go-v2/service/glue"
	gluetypes "github.com/aws/aws-sdk-go-v2/service/glue/types"
)

// TestSDKStopCrawlerRightAfterStart asserts the "cancel a bad crawl" pattern
// works over the wire: StopCrawler immediately after StartCrawler succeeds and
// GetCrawler reports the crawler READY with LastCrawl.Status CANCELLED.
func TestSDKStopCrawlerRightAfterStart(t *testing.T) {
	ctx := context.Background()
	c := newGlueClient(t)

	if _, err := c.CreateCrawler(ctx, &awsglue.CreateCrawlerInput{
		Name: aws.String("cancel-me"), Role: aws.String("r"), DatabaseName: aws.String("db"),
		Targets: &gluetypes.CrawlerTargets{
			S3Targets: []gluetypes.S3Target{{Path: aws.String("s3://bucket/prefix")}},
		},
	}); err != nil {
		t.Fatalf("CreateCrawler: %v", err)
	}

	if _, err := c.StartCrawler(ctx, &awsglue.StartCrawlerInput{Name: aws.String("cancel-me")}); err != nil {
		t.Fatalf("StartCrawler: %v", err)
	}

	if _, err := c.StopCrawler(ctx, &awsglue.StopCrawlerInput{Name: aws.String("cancel-me")}); err != nil {
		t.Fatalf("StopCrawler right after StartCrawler: %v", err)
	}

	got, err := c.GetCrawler(ctx, &awsglue.GetCrawlerInput{Name: aws.String("cancel-me")})
	if err != nil {
		t.Fatalf("GetCrawler: %v", err)
	}

	if got.Crawler.State != gluetypes.CrawlerStateReady {
		t.Fatalf("state = %q, want READY", got.Crawler.State)
	}

	if got.Crawler.LastCrawl == nil || got.Crawler.LastCrawl.Status != gluetypes.LastCrawlStatusCancelled {
		t.Fatalf("LastCrawl = %+v, want Status CANCELLED", got.Crawler.LastCrawl)
	}

	var apiErr *gluetypes.CrawlerNotRunningException
	if _, err = c.StopCrawler(ctx, &awsglue.StopCrawlerInput{Name: aws.String("cancel-me")}); !errors.As(err, &apiErr) {
		t.Fatalf("second StopCrawler err = %v, want CrawlerNotRunningException", err)
	}
}
