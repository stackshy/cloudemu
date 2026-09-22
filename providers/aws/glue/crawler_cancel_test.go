package glue_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	glueprovider "github.com/stackshy/cloudemu/v2/providers/aws/glue"
	"github.com/stackshy/cloudemu/v2/services/glue/driver"
)

func requireCrawlerNotRunning(t *testing.T, err error) {
	t.Helper()

	var apiErr *driver.APIError
	if !errors.As(err, &apiErr) || apiErr.Exception != driver.ExCrawlerNotRunning {
		t.Fatalf("err = %v, want %s", err, driver.ExCrawlerNotRunning)
	}
}

// TestStopCrawlerCancelsJustStartedCrawl covers the "start, then cancel a bad
// crawl" pattern: a StopCrawler right after StartCrawler cancels the run even
// though runs settle synchronously, while a stop with no run in flight (never
// started, already cancelled, or after the run would have finished) raises
// CrawlerNotRunningException.
func TestStopCrawlerCancelsJustStartedCrawl(t *testing.T) {
	ctx := context.Background()
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	m := glueprovider.New(config.NewOptions(config.WithClock(fc)))

	if err := m.CreateCrawler(ctx, driver.Crawler{Name: "c", Role: "r", DatabaseName: "db"}); err != nil {
		t.Fatalf("CreateCrawler: %v", err)
	}

	requireCrawlerNotRunning(t, m.StopCrawler(ctx, "c"))

	if err := m.StartCrawler(ctx, "c"); err != nil {
		t.Fatalf("StartCrawler: %v", err)
	}

	if err := m.StopCrawler(ctx, "c"); err != nil {
		t.Fatalf("StopCrawler right after StartCrawler: %v", err)
	}

	c, err := m.GetCrawler(ctx, "c")
	if err != nil {
		t.Fatalf("GetCrawler: %v", err)
	}

	if c.State != driver.CrawlerReady || c.LastCrawlStatus != driver.CrawlCancelled {
		t.Fatalf("after stop: state=%q lastCrawl=%q, want READY/CANCELLED", c.State, c.LastCrawlStatus)
	}

	requireCrawlerNotRunning(t, m.StopCrawler(ctx, "c"))

	if err = m.StartCrawler(ctx, "c"); err != nil {
		t.Fatalf("StartCrawler (second run): %v", err)
	}

	fc.Advance(2 * time.Minute)

	requireCrawlerNotRunning(t, m.StopCrawler(ctx, "c"))

	c, err = m.GetCrawler(ctx, "c")
	if err != nil || c.LastCrawlStatus != driver.JobRunSucceeded {
		t.Fatalf("finished run: err=%v lastCrawl=%q, want SUCCEEDED", err, c.LastCrawlStatus)
	}
}
