package containerregistry

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/containerregistry/driver"
	"github.com/stackshy/cloudemu/inject"
	"github.com/stackshy/cloudemu/metrics"
	"github.com/stackshy/cloudemu/providers/aws/ecr"
	"github.com/stackshy/cloudemu/ratelimit"
	"github.com/stackshy/cloudemu/recorder"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestDriver() (driver.ContainerRegistry, *config.FakeClock) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"))

	return ecr.New(opts), fc
}

func newTestContainerRegistry(opts ...Option) (*ContainerRegistry, *config.FakeClock) {
	d, fc := newTestDriver()
	return NewContainerRegistry(d, opts...), fc
}

func setupRepoWithImage(t *testing.T, cr *ContainerRegistry) {
	t.Helper()

	ctx := context.Background()

	_, err := cr.CreateRepository(ctx, driver.RepositoryConfig{Name: "test-repo"})
	require.NoError(t, err)

	_, err = cr.PutImage(ctx, &driver.ImageManifest{
		Repository: "test-repo",
		Tag:        "latest",
		Digest:     "sha256:abc123",
		SizeBytes:  1024,
	})
	require.NoError(t, err)
}

func TestNewContainerRegistry(t *testing.T) {
	cr, _ := newTestContainerRegistry()

	require.NotNil(t, cr)
	require.NotNil(t, cr.driver)
}

func TestCreateRepositoryPortable(t *testing.T) {
	cr, _ := newTestContainerRegistry()
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, err := cr.CreateRepository(ctx, driver.RepositoryConfig{Name: "my-repo"})
		require.NoError(t, err)
		assert.Equal(t, "my-repo", repo.Name)
	})

	t.Run("empty name error", func(t *testing.T) {
		_, err := cr.CreateRepository(ctx, driver.RepositoryConfig{})
		require.Error(t, err)
	})
}

func TestDeleteRepositoryPortable(t *testing.T) {
	cr, _ := newTestContainerRegistry()
	ctx := context.Background()

	_, err := cr.CreateRepository(ctx, driver.RepositoryConfig{Name: "del-repo"})
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		delErr := cr.DeleteRepository(ctx, "del-repo", false)
		require.NoError(t, delErr)
	})

	t.Run("not found", func(t *testing.T) {
		delErr := cr.DeleteRepository(ctx, "nonexistent", false)
		require.Error(t, delErr)
	})
}

func TestGetRepositoryPortable(t *testing.T) {
	cr, _ := newTestContainerRegistry()
	ctx := context.Background()

	_, err := cr.CreateRepository(ctx, driver.RepositoryConfig{Name: "get-repo"})
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		repo, getErr := cr.GetRepository(ctx, "get-repo")
		require.NoError(t, getErr)
		assert.Equal(t, "get-repo", repo.Name)
	})

	t.Run("not found", func(t *testing.T) {
		_, getErr := cr.GetRepository(ctx, "nonexistent")
		require.Error(t, getErr)
	})
}

func TestListRepositoriesPortable(t *testing.T) {
	cr, _ := newTestContainerRegistry()
	ctx := context.Background()

	repos, err := cr.ListRepositories(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, len(repos))

	_, err = cr.CreateRepository(ctx, driver.RepositoryConfig{Name: "a"})
	require.NoError(t, err)

	_, err = cr.CreateRepository(ctx, driver.RepositoryConfig{Name: "b"})
	require.NoError(t, err)

	repos, err = cr.ListRepositories(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, len(repos))
}

func TestPutGetDeleteImagePortable(t *testing.T) {
	cr, _ := newTestContainerRegistry()
	ctx := context.Background()

	setupRepoWithImage(t, cr)

	t.Run("get existing image", func(t *testing.T) {
		img, err := cr.GetImage(ctx, "test-repo", "latest")
		require.NoError(t, err)
		assert.Equal(t, "test-repo", img.Repository)
	})

	t.Run("list images", func(t *testing.T) {
		imgs, err := cr.ListImages(ctx, "test-repo")
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(imgs), 1)
	})

	t.Run("delete image", func(t *testing.T) {
		err := cr.DeleteImage(ctx, "test-repo", "latest")
		require.NoError(t, err)
	})
}

func TestTagImagePortable(t *testing.T) {
	cr, _ := newTestContainerRegistry()
	ctx := context.Background()

	setupRepoWithImage(t, cr)

	err := cr.TagImage(ctx, "test-repo", "latest", "v1.0")
	require.NoError(t, err)

	img, err := cr.GetImage(ctx, "test-repo", "v1.0")
	require.NoError(t, err)
	assert.Equal(t, "test-repo", img.Repository)
}

func TestWithRecorder(t *testing.T) {
	rec := recorder.New()
	cr, _ := newTestContainerRegistry(WithRecorder(rec))
	ctx := context.Background()

	_, err := cr.CreateRepository(ctx, driver.RepositoryConfig{Name: "rec-repo"})
	require.NoError(t, err)

	_, err = cr.GetRepository(ctx, "rec-repo")
	require.NoError(t, err)

	_, err = cr.ListRepositories(ctx)
	require.NoError(t, err)

	totalCalls := rec.CallCount()
	assert.GreaterOrEqual(t, totalCalls, 3)

	createCalls := rec.CallCountFor("containerregistry", "CreateRepository")
	assert.Equal(t, 1, createCalls)

	getCalls := rec.CallCountFor("containerregistry", "GetRepository")
	assert.Equal(t, 1, getCalls)

	listCalls := rec.CallCountFor("containerregistry", "ListRepositories")
	assert.Equal(t, 1, listCalls)
}

func TestWithRecorderOnError(t *testing.T) {
	rec := recorder.New()
	cr, _ := newTestContainerRegistry(WithRecorder(rec))
	ctx := context.Background()

	_, _ = cr.GetRepository(ctx, "nonexistent")

	totalCalls := rec.CallCount()
	assert.Equal(t, 1, totalCalls)

	last := rec.LastCall()
	require.NotNil(t, last, "expected a recorded call")
	assert.NotNil(t, last.Error, "expected recorded call to have an error")
}

func TestWithMetrics(t *testing.T) {
	mc := metrics.NewCollector()
	cr, _ := newTestContainerRegistry(WithMetrics(mc))
	ctx := context.Background()

	_, err := cr.CreateRepository(ctx, driver.RepositoryConfig{Name: "met-repo"})
	require.NoError(t, err)

	_, err = cr.GetRepository(ctx, "met-repo")
	require.NoError(t, err)

	_, err = cr.ListRepositories(ctx)
	require.NoError(t, err)

	q := metrics.NewQuery(mc)

	callsCount := q.ByName("calls_total").Count()
	assert.GreaterOrEqual(t, callsCount, 3)

	durCount := q.ByName("call_duration").Count()
	assert.GreaterOrEqual(t, durCount, 3)
}

func TestWithMetricsOnError(t *testing.T) {
	mc := metrics.NewCollector()
	cr, _ := newTestContainerRegistry(WithMetrics(mc))
	ctx := context.Background()

	_, _ = cr.GetRepository(ctx, "nonexistent")

	q := metrics.NewQuery(mc)

	errCount := q.ByName("errors_total").Count()
	assert.Equal(t, 1, errCount)
}

func TestWithErrorInjection(t *testing.T) {
	inj := inject.NewInjector()
	cr, _ := newTestContainerRegistry(WithErrorInjection(inj))
	ctx := context.Background()

	injectedErr := fmt.Errorf("injected failure")
	inj.Set("containerregistry", "CreateRepository", injectedErr, inject.Always{})

	_, err := cr.CreateRepository(ctx, driver.RepositoryConfig{Name: "fail-repo"})
	require.Error(t, err)
	assert.Equal(t, injectedErr, err)
}

func TestWithErrorInjectionRecorded(t *testing.T) {
	rec := recorder.New()
	inj := inject.NewInjector()
	cr, _ := newTestContainerRegistry(WithErrorInjection(inj), WithRecorder(rec))
	ctx := context.Background()

	injectedErr := fmt.Errorf("boom")
	inj.Set("containerregistry", "GetRepository", injectedErr, inject.Always{})

	_, err := cr.CreateRepository(ctx, driver.RepositoryConfig{Name: "inj-repo"})
	require.NoError(t, err)

	_, err = cr.GetRepository(ctx, "inj-repo")
	require.Error(t, err)

	getCalls := rec.CallsFor("containerregistry", "GetRepository")
	assert.Equal(t, 1, len(getCalls))
	assert.NotNil(t, getCalls[0].Error, "expected recorded GetRepository call to have an error")
}

func TestWithErrorInjectionRemoved(t *testing.T) {
	inj := inject.NewInjector()
	cr, _ := newTestContainerRegistry(WithErrorInjection(inj))
	ctx := context.Background()

	injectedErr := fmt.Errorf("fail")
	inj.Set("containerregistry", "CreateRepository", injectedErr, inject.Always{})

	_, err := cr.CreateRepository(ctx, driver.RepositoryConfig{Name: "test"})
	require.Error(t, err)

	inj.Remove("containerregistry", "CreateRepository")

	_, err = cr.CreateRepository(ctx, driver.RepositoryConfig{Name: "test"})
	require.NoError(t, err)
}

func TestWithRateLimiter(t *testing.T) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"))
	d := ecr.New(opts)
	limiter := ratelimit.New(1, 1, fc)
	cr := NewContainerRegistry(d, WithRateLimiter(limiter))
	ctx := context.Background()

	_, err := cr.CreateRepository(ctx, driver.RepositoryConfig{Name: "rl-repo"})
	require.NoError(t, err)

	_, err = cr.GetRepository(ctx, "rl-repo")
	require.Error(t, err, "expected rate limit error on second call without time advance")
}

func TestWithLatency(t *testing.T) {
	latency := 1 * time.Millisecond
	cr, _ := newTestContainerRegistry(WithLatency(latency))
	ctx := context.Background()

	_, err := cr.CreateRepository(ctx, driver.RepositoryConfig{Name: "lat-repo"})
	require.NoError(t, err)

	repo, err := cr.GetRepository(ctx, "lat-repo")
	require.NoError(t, err)
	assert.Equal(t, "lat-repo", repo.Name)
}

func TestAllOptionsComposed(t *testing.T) {
	rec := recorder.New()
	mc := metrics.NewCollector()
	inj := inject.NewInjector()
	latency := 1 * time.Millisecond

	cr, _ := newTestContainerRegistry(
		WithRecorder(rec),
		WithMetrics(mc),
		WithErrorInjection(inj),
		WithLatency(latency),
	)
	ctx := context.Background()

	_, err := cr.CreateRepository(ctx, driver.RepositoryConfig{Name: "all-opts"})
	require.NoError(t, err)

	_, err = cr.GetRepository(ctx, "all-opts")
	require.NoError(t, err)

	assert.Equal(t, 2, rec.CallCount())

	q := metrics.NewQuery(mc)
	assert.Equal(t, 2, q.ByName("calls_total").Count())
}

func TestPortableGetImageError(t *testing.T) {
	cr, _ := newTestContainerRegistry()
	ctx := context.Background()

	_, err := cr.GetImage(ctx, "no-repo", "latest")
	require.Error(t, err)
}

func TestPortableListImagesError(t *testing.T) {
	cr, _ := newTestContainerRegistry()
	ctx := context.Background()

	_, err := cr.ListImages(ctx, "no-repo")
	require.Error(t, err)
}

func TestPortableDeleteImageError(t *testing.T) {
	cr, _ := newTestContainerRegistry()
	ctx := context.Background()

	err := cr.DeleteImage(ctx, "no-repo", "latest")
	require.Error(t, err)
}
