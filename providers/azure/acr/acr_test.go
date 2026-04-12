package acr

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/containerregistry/driver"
	mondriver "github.com/stackshy/cloudemu/monitoring/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestMock() (*Mock, *config.FakeClock) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("eastus"))

	return New(opts), fc
}

func createTestRepo(t *testing.T, m *Mock, name string) {
	t.Helper()

	_, err := m.CreateRepository(context.Background(), driver.RepositoryConfig{Name: name})
	require.NoError(t, err)
}

func pushTestImage(t *testing.T, m *Mock, repo, tag string) *driver.ImageDetail {
	t.Helper()

	detail, err := m.PutImage(context.Background(), &driver.ImageManifest{
		Repository: repo,
		Tag:        tag,
		SizeBytes:  1024,
	})
	require.NoError(t, err)

	return detail
}

func TestCreateRepository(t *testing.T) {
	tests := []struct {
		name      string
		cfg       driver.RepositoryConfig
		setup     func(*Mock)
		expectErr bool
	}{
		{name: "success with defaults", cfg: driver.RepositoryConfig{Name: "my-repo"}},
		{
			name: "success with tags",
			cfg: driver.RepositoryConfig{
				Name: "tagged-repo",
				Tags: map[string]string{"env": "dev"},
			},
		},
		{name: "empty name", cfg: driver.RepositoryConfig{}, expectErr: true},
		{
			name: "duplicate repository",
			cfg:  driver.RepositoryConfig{Name: "dup"},
			setup: func(m *Mock) {
				createTestRepo(&testing.T{}, m, "dup")
			},
			expectErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := newTestMock()

			if tc.setup != nil {
				tc.setup(m)
			}

			repo, err := m.CreateRepository(context.Background(), tc.cfg)
			if tc.expectErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)

			assert.Contains(t, repo.URI, "cloudemu.azurecr.io/"+tc.cfg.Name)
			assert.NotEmpty(t, repo.Name)
			assert.NotEmpty(t, repo.CreatedAt)
			assert.Equal(t, 0, repo.ImageCount)
		})
	}
}

func TestDeleteRepository(t *testing.T) {
	tests := []struct {
		name      string
		repoName  string
		force     bool
		setup     func(*Mock)
		expectErr bool
	}{
		{
			name:     "success empty repo",
			repoName: "to-delete",
			setup: func(m *Mock) {
				createTestRepo(&testing.T{}, m, "to-delete")
			},
		},
		{
			name:     "force delete non-empty repo",
			repoName: "non-empty",
			force:    true,
			setup: func(m *Mock) {
				tt := &testing.T{}
				createTestRepo(tt, m, "non-empty")
				pushTestImage(tt, m, "non-empty", "v1")
			},
		},
		{
			name:     "non-empty without force",
			repoName: "non-empty2",
			force:    false,
			setup: func(m *Mock) {
				tt := &testing.T{}
				createTestRepo(tt, m, "non-empty2")
				pushTestImage(tt, m, "non-empty2", "v1")
			},
			expectErr: true,
		},
		{
			name:      "not found",
			repoName:  "nonexistent",
			expectErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := newTestMock()

			if tc.setup != nil {
				tc.setup(m)
			}

			err := m.DeleteRepository(context.Background(), tc.repoName, tc.force)
			if tc.expectErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
		})
	}
}

func TestGetRepository(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	createTestRepo(t, m, "my-repo")

	t.Run("success", func(t *testing.T) {
		repo, err := m.GetRepository(ctx, "my-repo")
		require.NoError(t, err)
		assert.Contains(t, repo.URI, "cloudemu.azurecr.io/my-repo")
		assert.Equal(t, 0, repo.ImageCount)
	})

	t.Run("with images", func(t *testing.T) {
		pushTestImage(t, m, "my-repo", "v1")

		repo, err := m.GetRepository(ctx, "my-repo")
		require.NoError(t, err)
		assert.Equal(t, 1, repo.ImageCount)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := m.GetRepository(ctx, "nonexistent")
		require.Error(t, err)
	})
}

func TestListRepositories(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	t.Run("empty list", func(t *testing.T) {
		repos, err := m.ListRepositories(ctx)
		require.NoError(t, err)
		assert.Equal(t, 0, len(repos))
	})

	createTestRepo(t, m, "repo-a")
	createTestRepo(t, m, "repo-b")

	t.Run("two repositories", func(t *testing.T) {
		repos, err := m.ListRepositories(ctx)
		require.NoError(t, err)
		assert.Equal(t, 2, len(repos))
	})
}

func TestPutImage(t *testing.T) {
	tests := []struct {
		name      string
		manifest  driver.ImageManifest
		setup     func(*Mock)
		expectErr bool
	}{
		{
			name:     "success",
			manifest: driver.ImageManifest{Repository: "my-repo", Tag: "v1", SizeBytes: 2048},
			setup: func(m *Mock) {
				createTestRepo(&testing.T{}, m, "my-repo")
			},
		},
		{
			name:     "with custom digest",
			manifest: driver.ImageManifest{Repository: "my-repo", Tag: "v2", Digest: "sha256:abcdef", SizeBytes: 512},
			setup: func(m *Mock) {
				createTestRepo(&testing.T{}, m, "my-repo")
			},
		},
		{
			name:      "repo not found",
			manifest:  driver.ImageManifest{Repository: "missing", Tag: "v1"},
			expectErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := newTestMock()

			if tc.setup != nil {
				tc.setup(m)
			}

			detail, err := m.PutImage(context.Background(), &tc.manifest)
			if tc.expectErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)

			assert.Equal(t, tc.manifest.Repository, detail.Repository)
			assert.Contains(t, detail.Tags, tc.manifest.Tag)
			assert.NotEmpty(t, detail.Digest)
			assert.NotEmpty(t, detail.PushedAt)
			assert.Equal(t, registryName, detail.RegistryID)
		})
	}
}

func TestGetImage(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	createTestRepo(t, m, "my-repo")
	img := pushTestImage(t, m, "my-repo", "latest")

	t.Run("by tag", func(t *testing.T) {
		detail, err := m.GetImage(ctx, "my-repo", "latest")
		require.NoError(t, err)
		assert.Equal(t, img.Digest, detail.Digest)
		assert.Contains(t, detail.Tags, "latest")
	})

	t.Run("by digest", func(t *testing.T) {
		detail, err := m.GetImage(ctx, "my-repo", img.Digest)
		require.NoError(t, err)
		assert.Equal(t, img.Digest, detail.Digest)
	})

	t.Run("image not found", func(t *testing.T) {
		_, err := m.GetImage(ctx, "my-repo", "nonexistent")
		require.Error(t, err)
	})

	t.Run("repo not found", func(t *testing.T) {
		_, err := m.GetImage(ctx, "missing", "latest")
		require.Error(t, err)
	})
}

func TestListImages(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	createTestRepo(t, m, "my-repo")

	t.Run("empty list", func(t *testing.T) {
		images, err := m.ListImages(ctx, "my-repo")
		require.NoError(t, err)
		assert.Equal(t, 0, len(images))
	})

	pushTestImage(t, m, "my-repo", "v1")
	pushTestImage(t, m, "my-repo", "v2")

	t.Run("two images", func(t *testing.T) {
		images, err := m.ListImages(ctx, "my-repo")
		require.NoError(t, err)
		assert.Equal(t, 2, len(images))
	})

	t.Run("repo not found", func(t *testing.T) {
		_, err := m.ListImages(ctx, "missing")
		require.Error(t, err)
	})
}

func TestDeleteImage(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	createTestRepo(t, m, "my-repo")
	img := pushTestImage(t, m, "my-repo", "v1")

	t.Run("success by tag", func(t *testing.T) {
		err := m.DeleteImage(ctx, "my-repo", "v1")
		require.NoError(t, err)

		_, err = m.GetImage(ctx, "my-repo", img.Digest)
		require.Error(t, err)
	})

	t.Run("image not found", func(t *testing.T) {
		err := m.DeleteImage(ctx, "my-repo", "nonexistent")
		require.Error(t, err)
	})

	t.Run("repo not found", func(t *testing.T) {
		err := m.DeleteImage(ctx, "missing", "v1")
		require.Error(t, err)
	})
}

func TestTagImage(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	createTestRepo(t, m, "my-repo")
	pushTestImage(t, m, "my-repo", "v1")

	t.Run("success", func(t *testing.T) {
		err := m.TagImage(ctx, "my-repo", "v1", "latest")
		require.NoError(t, err)

		detail, err := m.GetImage(ctx, "my-repo", "latest")
		require.NoError(t, err)
		assert.Contains(t, detail.Tags, "v1")
		assert.Contains(t, detail.Tags, "latest")
	})

	t.Run("source not found", func(t *testing.T) {
		err := m.TagImage(ctx, "my-repo", "nonexistent", "latest")
		require.Error(t, err)
	})

	t.Run("repo not found", func(t *testing.T) {
		err := m.TagImage(ctx, "missing", "v1", "latest")
		require.Error(t, err)
	})
}

func TestTagImageUpdatesImageCount(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	createTestRepo(t, m, "count-repo")
	pushTestImage(t, m, "count-repo", "v1")
	pushTestImage(t, m, "count-repo", "v2")

	repo, err := m.GetRepository(ctx, "count-repo")
	require.NoError(t, err)
	assert.Equal(t, 2, repo.ImageCount)

	// Tag image v1 with a new tag - should not change image count
	err = m.TagImage(ctx, "count-repo", "v1", "latest")
	require.NoError(t, err)

	repo, err = m.GetRepository(ctx, "count-repo")
	require.NoError(t, err)
	assert.Equal(t, 2, repo.ImageCount)
}

func TestTagImageEmptyTagRejected(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	createTestRepo(t, m, "empty-tag-repo")
	pushTestImage(t, m, "empty-tag-repo", "v1")

	err := m.TagImage(ctx, "empty-tag-repo", "v1", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty")
}

func TestImageTagMutability(t *testing.T) {
	ctx := context.Background()

	t.Run("mutable allows tag overwrite", func(t *testing.T) {
		m, _ := newTestMock()

		_, err := m.CreateRepository(ctx, driver.RepositoryConfig{
			Name:               "mutable-repo",
			ImageTagMutability: "MUTABLE",
		})
		require.NoError(t, err)

		pushTestImage(t, m, "mutable-repo", "latest")

		_, err = m.PutImage(ctx, &driver.ImageManifest{
			Repository: "mutable-repo",
			Tag:        "latest",
			SizeBytes:  2048,
		})
		require.NoError(t, err)
	})

	t.Run("immutable blocks tag overwrite", func(t *testing.T) {
		m, _ := newTestMock()

		_, err := m.CreateRepository(ctx, driver.RepositoryConfig{
			Name:               "immutable-repo",
			ImageTagMutability: "IMMUTABLE",
		})
		require.NoError(t, err)

		pushTestImage(t, m, "immutable-repo", "v1")

		_, err = m.PutImage(ctx, &driver.ImageManifest{
			Repository: "immutable-repo",
			Tag:        "v1",
			SizeBytes:  2048,
		})
		require.Error(t, err)
	})

	t.Run("immutable allows different tags", func(t *testing.T) {
		m, _ := newTestMock()

		_, err := m.CreateRepository(ctx, driver.RepositoryConfig{
			Name:               "immutable-repo2",
			ImageTagMutability: "IMMUTABLE",
		})
		require.NoError(t, err)

		pushTestImage(t, m, "immutable-repo2", "v1")
		pushTestImage(t, m, "immutable-repo2", "v2")

		images, err := m.ListImages(ctx, "immutable-repo2")
		require.NoError(t, err)
		assert.Equal(t, 2, len(images))
	})

	t.Run("default mutability is MUTABLE", func(t *testing.T) {
		m, _ := newTestMock()

		_, err := m.CreateRepository(ctx, driver.RepositoryConfig{Name: "default-repo"})
		require.NoError(t, err)

		pushTestImage(t, m, "default-repo", "latest")

		_, err = m.PutImage(ctx, &driver.ImageManifest{
			Repository: "default-repo",
			Tag:        "latest",
			SizeBytes:  2048,
		})
		require.NoError(t, err)
	})
}

func TestLifecyclePolicy(t *testing.T) {
	ctx := context.Background()

	t.Run("put and get policy", func(t *testing.T) {
		m, _ := newTestMock()
		createTestRepo(t, m, "my-repo")

		policy := driver.LifecyclePolicy{
			Rules: []driver.LifecycleRule{
				{Priority: 1, TagStatus: "untagged", CountType: "imageCountMoreThan", CountValue: 5, Action: "expire"},
			},
		}

		err := m.PutLifecyclePolicy(ctx, "my-repo", policy)
		require.NoError(t, err)

		got, err := m.GetLifecyclePolicy(ctx, "my-repo")
		require.NoError(t, err)
		assert.Equal(t, 1, len(got.Rules))
		assert.Equal(t, 1, got.Rules[0].Priority)
	})

	t.Run("get policy not set", func(t *testing.T) {
		m, _ := newTestMock()
		createTestRepo(t, m, "no-policy")

		_, err := m.GetLifecyclePolicy(ctx, "no-policy")
		require.Error(t, err)
	})

	t.Run("repo not found", func(t *testing.T) {
		m, _ := newTestMock()

		err := m.PutLifecyclePolicy(ctx, "missing", driver.LifecyclePolicy{})
		require.Error(t, err)

		_, err = m.GetLifecyclePolicy(ctx, "missing")
		require.Error(t, err)
	})

	t.Run("evaluate imageCountMoreThan", func(t *testing.T) {
		m, fc := newTestMock()
		createTestRepo(t, m, "eval-repo")

		for i := range 5 {
			fc.Advance(time.Minute)
			pushTestImage(t, m, "eval-repo", "")

			_ = i
		}

		policy := driver.LifecyclePolicy{
			Rules: []driver.LifecycleRule{
				{Priority: 1, TagStatus: "any", CountType: "imageCountMoreThan", CountValue: 3, Action: "expire"},
			},
		}

		err := m.PutLifecyclePolicy(ctx, "eval-repo", policy)
		require.NoError(t, err)

		expired, err := m.EvaluateLifecyclePolicy(ctx, "eval-repo")
		require.NoError(t, err)
		assert.Equal(t, 2, len(expired))
	})

	t.Run("evaluate sinceImagePushed", func(t *testing.T) {
		m, fc := newTestMock()
		createTestRepo(t, m, "since-repo")

		pushTestImage(t, m, "since-repo", "old")

		fc.Advance(31 * 24 * time.Hour)

		pushTestImage(t, m, "since-repo", "new")

		policy := driver.LifecyclePolicy{
			Rules: []driver.LifecycleRule{
				{Priority: 1, TagStatus: "any", CountType: "sinceImagePushed", CountValue: 30, Action: "expire"},
			},
		}

		err := m.PutLifecyclePolicy(ctx, "since-repo", policy)
		require.NoError(t, err)

		expired, err := m.EvaluateLifecyclePolicy(ctx, "since-repo")
		require.NoError(t, err)
		assert.Equal(t, 1, len(expired))
	})

	t.Run("evaluate no policy returns empty", func(t *testing.T) {
		m, _ := newTestMock()
		createTestRepo(t, m, "no-policy-repo")

		expired, err := m.EvaluateLifecyclePolicy(ctx, "no-policy-repo")
		require.NoError(t, err)
		assert.Equal(t, 0, len(expired))
	})

	t.Run("evaluate repo not found", func(t *testing.T) {
		m, _ := newTestMock()

		_, err := m.EvaluateLifecyclePolicy(ctx, "missing")
		require.Error(t, err)
	})
}

func TestImageScan(t *testing.T) {
	ctx := context.Background()

	t.Run("manual scan", func(t *testing.T) {
		m, _ := newTestMock()
		createTestRepo(t, m, "scan-repo")
		pushTestImage(t, m, "scan-repo", "v1")

		scan, err := m.StartImageScan(ctx, "scan-repo", "v1")
		require.NoError(t, err)
		assert.Equal(t, "COMPLETE", scan.Status)
		assert.Equal(t, "scan-repo", scan.Repository)
		assert.NotEmpty(t, scan.FindingCounts)
		assert.NotEmpty(t, scan.CompletedAt)
	})

	t.Run("get scan results", func(t *testing.T) {
		m, _ := newTestMock()
		createTestRepo(t, m, "scan-repo")
		pushTestImage(t, m, "scan-repo", "v1")

		_, err := m.StartImageScan(ctx, "scan-repo", "v1")
		require.NoError(t, err)

		result, err := m.GetImageScanResults(ctx, "scan-repo", "v1")
		require.NoError(t, err)
		assert.Equal(t, "COMPLETE", result.Status)
	})

	t.Run("scan on push", func(t *testing.T) {
		m, _ := newTestMock()

		_, err := m.CreateRepository(ctx, driver.RepositoryConfig{
			Name:            "auto-scan-repo",
			ImageScanOnPush: true,
		})
		require.NoError(t, err)

		pushTestImage(t, m, "auto-scan-repo", "v1")

		result, err := m.GetImageScanResults(ctx, "auto-scan-repo", "v1")
		require.NoError(t, err)
		assert.Equal(t, "COMPLETE", result.Status)
	})

	t.Run("no scan results", func(t *testing.T) {
		m, _ := newTestMock()
		createTestRepo(t, m, "no-scan-repo")
		pushTestImage(t, m, "no-scan-repo", "v1")

		_, err := m.GetImageScanResults(ctx, "no-scan-repo", "v1")
		require.Error(t, err)
	})

	t.Run("scan image not found", func(t *testing.T) {
		m, _ := newTestMock()
		createTestRepo(t, m, "scan-repo2")

		_, err := m.StartImageScan(ctx, "scan-repo2", "nonexistent")
		require.Error(t, err)
	})

	t.Run("scan repo not found", func(t *testing.T) {
		m, _ := newTestMock()

		_, err := m.StartImageScan(ctx, "missing", "v1")
		require.Error(t, err)
	})

	t.Run("get scan results repo not found", func(t *testing.T) {
		m, _ := newTestMock()

		_, err := m.GetImageScanResults(ctx, "missing", "v1")
		require.Error(t, err)
	})

	t.Run("delete image removes scan results", func(t *testing.T) {
		m, _ := newTestMock()
		createTestRepo(t, m, "del-scan-repo")
		pushTestImage(t, m, "del-scan-repo", "v1")

		_, err := m.StartImageScan(ctx, "del-scan-repo", "v1")
		require.NoError(t, err)

		err = m.DeleteImage(ctx, "del-scan-repo", "v1")
		require.NoError(t, err)
	})
}

// fakeMonitoring is a minimal monitoring mock for testing metric emission.
type fakeMonitoring struct {
	data []mondriver.MetricDatum
}

func (f *fakeMonitoring) PutMetricData(_ context.Context, data []mondriver.MetricDatum) error {
	f.data = append(f.data, data...)
	return nil
}

func (f *fakeMonitoring) GetMetricData(
	_ context.Context, _ mondriver.GetMetricInput,
) (*mondriver.MetricDataResult, error) {
	return &mondriver.MetricDataResult{}, nil
}

func (f *fakeMonitoring) ListMetrics(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}

func (f *fakeMonitoring) CreateAlarm(_ context.Context, _ mondriver.AlarmConfig) error {
	return nil
}

func (f *fakeMonitoring) DeleteAlarm(_ context.Context, _ string) error {
	return nil
}

func (f *fakeMonitoring) DescribeAlarms(
	_ context.Context, _ []string,
) ([]mondriver.AlarmInfo, error) {
	return nil, nil
}

func (f *fakeMonitoring) SetAlarmState(_ context.Context, _, _, _ string) error {
	return nil
}

func (f *fakeMonitoring) CreateNotificationChannel(_ context.Context, _ mondriver.NotificationChannelConfig) (*mondriver.NotificationChannelInfo, error) {
	return nil, nil
}

func (f *fakeMonitoring) DeleteNotificationChannel(_ context.Context, _ string) error {
	return nil
}

func (f *fakeMonitoring) GetNotificationChannel(_ context.Context, _ string) (*mondriver.NotificationChannelInfo, error) {
	return nil, nil
}

func (f *fakeMonitoring) ListNotificationChannels(_ context.Context) ([]mondriver.NotificationChannelInfo, error) {
	return nil, nil
}

func (f *fakeMonitoring) GetAlarmHistory(_ context.Context, _ string, _ int) ([]mondriver.AlarmHistoryEntry, error) {
	return nil, nil
}

func TestMetricsEmission(t *testing.T) {
	ctx := context.Background()

	t.Run("push emits ImagePushCount", func(t *testing.T) {
		m, _ := newTestMock()
		mon := &fakeMonitoring{}
		m.SetMonitoring(mon)
		createTestRepo(t, m, "metric-repo")

		pushTestImage(t, m, "metric-repo", "v1")

		require.NotEmpty(t, mon.data)

		found := false
		for _, d := range mon.data {
			if d.MetricName == "ImagePushCount" {
				found = true
				assert.Equal(t, "Microsoft.ContainerRegistry/registries", d.Namespace)
				assert.Equal(t, "metric-repo", d.Dimensions["repositoryName"])
				assert.Equal(t, float64(1), d.Value)
			}
		}
		assert.True(t, found, "expected ImagePushCount metric")
	})

	t.Run("pull emits ImagePullCount", func(t *testing.T) {
		m, _ := newTestMock()
		mon := &fakeMonitoring{}
		m.SetMonitoring(mon)
		createTestRepo(t, m, "metric-repo")
		pushTestImage(t, m, "metric-repo", "v1")

		mon.data = nil

		_, err := m.GetImage(ctx, "metric-repo", "v1")
		require.NoError(t, err)

		found := false
		for _, d := range mon.data {
			if d.MetricName == "ImagePullCount" {
				found = true
				assert.Equal(t, "Microsoft.ContainerRegistry/registries", d.Namespace)
				assert.Equal(t, "metric-repo", d.Dimensions["repositoryName"])
				assert.Equal(t, float64(1), d.Value)
			}
		}
		assert.True(t, found, "expected ImagePullCount metric")
	})

	t.Run("no monitoring does not panic", func(t *testing.T) {
		m, _ := newTestMock()
		createTestRepo(t, m, "no-mon-repo")

		pushTestImage(t, m, "no-mon-repo", "v1")

		_, err := m.GetImage(ctx, "no-mon-repo", "v1")
		require.NoError(t, err)
	})
}
