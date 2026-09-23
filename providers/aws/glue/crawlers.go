package glue

import (
	"context"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/services/glue/driver"
)

// crawlCancelWindow is how long after StartCrawler the just-started crawl can
// still be canceled with StopCrawler. Runs settle synchronously (the crawler is
// READY again as soon as StartCrawler returns), but a real crawl takes at least
// this long, so the common "start, then cancel a bad crawl" pattern must still
// succeed instead of raising CrawlerNotRunningException.
const crawlCancelWindow = time.Minute

// crawlerData is a crawler plus its own lock. cancelableUntil is the end of the
// most recent run's cancel window (zero when there is no cancelable run), and
// runStartedAt the instant that run started. A non-zero cancelableUntil also
// marks the run's "Glue Crawler State Change" Succeeded event as not yet
// published. See flushCrawl.
type crawlerData struct {
	crawler         driver.Crawler
	cancelableUntil time.Time
	runStartedAt    time.Time
	mu              sync.RWMutex
}

// takeFinishedCrawl claims the Succeeded event of the crawler's most recent
// run once that run can no longer be canceled (its cancel window has closed),
// or unconditionally with force (a new run supersedes it). It reports the run's
// start and completion instants, and ok=false when there is nothing to publish
// (no run, a canceled run, an already-published run, or a still-open window).
func (m *Mock) takeFinishedCrawl(cd *crawlerData, force bool) (started, completed time.Time, ok bool) {
	cd.mu.Lock()
	defer cd.mu.Unlock()

	if cd.cancelableUntil.IsZero() {
		return time.Time{}, time.Time{}, false
	}

	now := m.now()
	if !force && now.Before(cd.cancelableUntil) {
		return time.Time{}, time.Time{}, false
	}

	completed = cd.cancelableUntil
	if now.Before(completed) {
		completed = now
	}

	started = cd.runStartedAt
	cd.cancelableUntil = time.Time{}

	return started, completed, true
}

// flushCrawl publishes the crawler's pending Succeeded event if its run has
// finished. The emulator settles crawls lazily (no timers, like
// internal/settle): the event goes out at the first crawler operation after
// the cancel window closes. It must be called without cd.mu held.
func (m *Mock) flushCrawl(ctx context.Context, cd *crawlerData, name string, force bool) {
	if started, completed, ok := m.takeFinishedCrawl(cd, force); ok {
		m.emitCrawlSucceeded(ctx, name, started, completed)
	}
}

// flushAllCrawls settles every crawler's finished run (used by the list reads).
func (m *Mock) flushAllCrawls(ctx context.Context) {
	for name, cd := range m.crawlers.All() {
		m.flushCrawl(ctx, cd, name, false)
	}
}

// CreateCrawler creates a crawler in the READY state, atomically.
//
//nolint:gocritic // hugeParam: taken by value to match the driver interface / copy semantics
func (m *Mock) CreateCrawler(ctx context.Context, c driver.Crawler) error {
	if !validName(c.Name) {
		return invalidInput("crawler name %q is invalid", c.Name)
	}

	now := m.now()
	c.State = driver.CrawlerReady
	c.CreationTime = now
	c.LastUpdated = now
	tags := c.Tags
	c.Tags = nil
	stored := copyCrawler(c)

	if !m.crawlers.SetIfAbsent(c.Name, &crawlerData{crawler: stored}) {
		return alreadyExists("Crawler already exists: %s", c.Name)
	}

	if len(tags) > 0 {
		// Tags on a crawler live in the tag store under its ARN, so GetTags (what
		// Terraform reads) returns them; GetCrawler intentionally omits tags.
		_ = m.TagResource(ctx, m.arn("crawler/"+c.Name), tags)
	}

	return nil
}

func (m *Mock) getCrawlerData(name string) (*crawlerData, error) {
	if !validName(name) {
		return nil, invalidInput("crawler name %q is invalid", name)
	}

	cd, ok := m.crawlers.Get(name)
	if !ok {
		return nil, entityNotFound("Crawler not found: %s", name)
	}

	return cd, nil
}

// GetCrawler returns a deep copy of a crawler.
func (m *Mock) GetCrawler(ctx context.Context, name string) (*driver.Crawler, error) {
	cd, err := m.getCrawlerData(name)
	if err != nil {
		return nil, err
	}

	m.flushCrawl(ctx, cd, name, false)

	cd.mu.RLock()
	defer cd.mu.RUnlock()

	out := copyCrawler(cd.crawler)

	return &out, nil
}

// UpdateCrawler replaces a crawler's mutable fields. A crawler that is running
// cannot be updated (ConcurrentModificationException).
//
//nolint:gocritic // hugeParam: taken by value to match the driver interface / copy semantics
func (m *Mock) UpdateCrawler(_ context.Context, name string, c driver.Crawler) error {
	cd, err := m.getCrawlerData(name)
	if err != nil {
		return err
	}

	cd.mu.Lock()
	defer cd.mu.Unlock()

	if cd.crawler.State == driver.CrawlerRunning {
		return concurrentModification("Crawler %s is running and cannot be updated", name)
	}

	created := cd.crawler.CreationTime
	state := cd.crawler.State
	c.Name = name
	c.State = state
	c.CreationTime = created
	c.LastUpdated = m.now()
	cd.crawler = copyCrawler(c)

	return nil
}

// DeleteCrawler removes a crawler unless it is running.
func (m *Mock) DeleteCrawler(_ context.Context, name string) error {
	cd, err := m.getCrawlerData(name)
	if err != nil {
		return err
	}

	cd.mu.Lock()
	defer cd.mu.Unlock()

	if cd.crawler.State == driver.CrawlerRunning {
		return concurrentModification("Crawler %s is running and cannot be deleted", name)
	}

	m.crawlers.Delete(name)

	return nil
}

// GetCrawlers lists crawlers with pagination.
//
//nolint:dupl // near-identical list/batch body per resource; separate is clearer than reflection
func (m *Mock) GetCrawlers(ctx context.Context, page driver.TablePagination) ([]driver.Crawler, string, error) {
	m.flushAllCrawls(ctx)

	keys := sortedKeys(m.crawlers.Keys())
	all := make([]driver.Crawler, 0, len(keys))

	for _, key := range keys {
		cd, ok := m.crawlers.Get(key)
		if !ok {
			continue
		}

		cd.mu.RLock()
		all = append(all, copyCrawler(cd.crawler))
		cd.mu.RUnlock()
	}

	return paginate(all, page)
}

// ListCrawlers returns crawler names with pagination.
//
//nolint:gocritic // unnamedResult: thin pass-through to paginate; names add no clarity
func (m *Mock) ListCrawlers(ctx context.Context, page driver.TablePagination) ([]string, string, error) {
	m.flushAllCrawls(ctx)

	return paginate(sortedKeys(m.crawlers.Keys()), page)
}

// StartCrawler runs a crawler; the emulator has no data source to crawl, so the
// run settles immediately (state returns to READY, LastCrawlStatus SUCCEEDED).
//
// It publishes "Glue Crawler State Change" Started at once, but Succeeded only
// once the run's cancel window closes (see flushCrawl): real Glue documents
// only Started/Succeeded/Failed crawler events, so a crawl canceled by
// StopCrawler publishes no terminal event, and a Succeeded must never precede
// that cancellation.
func (m *Mock) StartCrawler(ctx context.Context, name string) error {
	cd, err := m.getCrawlerData(name)
	if err != nil {
		return err
	}

	// A previous run still inside its window is superseded by this one, so it
	// completed: publish its Succeeded before the new run's Started.
	m.flushCrawl(ctx, cd, name, true)

	at, err := m.settleCrawl(cd, name)
	if err != nil {
		return err
	}

	m.emitCrawlStarted(ctx, name, at)

	return nil
}

// settleCrawl is StartCrawler's locked core: it runs the crawl to completion
// and returns the instant it ran.
func (m *Mock) settleCrawl(cd *crawlerData, name string) (time.Time, error) {
	cd.mu.Lock()
	defer cd.mu.Unlock()

	if cd.crawler.State == driver.CrawlerRunning {
		return time.Time{}, concurrentModification("Crawler %s is already running", name)
	}

	now := m.now()
	cd.crawler.State = driver.CrawlerReady
	cd.crawler.LastCrawlStatus = driver.JobRunSucceeded
	cd.crawler.LastUpdated = now
	cd.cancelableUntil = now.Add(crawlCancelWindow)
	cd.runStartedAt = now

	return now, nil
}

// StopCrawler stops a running crawler. Runs settle synchronously, so a stop
// issued within crawlCancelWindow of StartCrawler cancels that just-started
// crawl (LastCrawl status canceled, crawler READY), as it would in real Glue.
// Stopping a crawler with no run in flight raises CrawlerNotRunningException.
//
// A canceled crawl publishes no further event (its pending Succeeded is
// dropped); a run whose window already closed is settled, its Succeeded
// published, before the stop is rejected.
func (m *Mock) StopCrawler(ctx context.Context, name string) error {
	cd, err := m.getCrawlerData(name)
	if err != nil {
		return err
	}

	m.flushCrawl(ctx, cd, name, false)

	cd.mu.Lock()
	defer cd.mu.Unlock()

	now := m.now()
	inFlight := cd.crawler.State == driver.CrawlerRunning || now.Before(cd.cancelableUntil)

	if !inFlight {
		return crawlerNotRunning("Crawler %s is not running", name)
	}

	cd.crawler.State = driver.CrawlerReady
	cd.crawler.LastCrawlStatus = driver.CrawlCancelled
	cd.crawler.LastUpdated = now
	cd.cancelableUntil = time.Time{}

	return nil
}

// BatchGetCrawlers returns the found crawlers and the names that did not exist.
//
//nolint:dupl // near-identical CRUD/batch bodies per resource; separate is clearer than reflection
func (m *Mock) BatchGetCrawlers(_ context.Context, names []string) ([]driver.Crawler, []string, error) {
	if len(names) > maxBatchGet {
		return nil, nil, invalidInput("cannot request more than %d crawlers", maxBatchGet)
	}

	found := make([]driver.Crawler, 0, len(names))

	var notFound []string

	for _, n := range names {
		c, err := m.GetCrawler(context.Background(), n)
		if err != nil {
			notFound = append(notFound, n)

			continue
		}

		found = append(found, *c)
	}

	return found, notFound, nil
}
