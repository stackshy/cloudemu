package artifactregistry

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/containerregistry/driver"
	mondriver "github.com/stackshy/cloudemu/monitoring/driver"
	"github.com/stackshy/cloudemu/providers/gcp/cloudmonitoring"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestMock() (*Mock, *config.FakeClock) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-central1"), config.WithProjectID("test-project"))

	return New(opts), fc
}

func newTestMockWithMonitoring() (*Mock, *cloudmonitoring.Mock, *config.FakeClock) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-central1"), config.WithProjectID("test-project"))

	mon := cloudmonitoring.New(opts)
	m := New(opts)
	m.SetMonitoring(mon)

	return m, mon, fc
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
		checkFn   func(*testing.T, *driver.Repository)
	}{
		{
			name: "success with defaults",
			cfg:  driver.RepositoryConfig{Name: "my-repo"},
			checkFn: func(t *testing.T, repo *driver.Repository) {
				t.Helper()
				assert.Contains(t, repo.URI, "us-central1-docker.pkg.dev/test-project/my-repo")
				assert.Contains(t, repo.Name, "my-repo")
				assert.Equal(t, 0, repo.ImageCount)
				assert.NotEmpty(t, repo.CreatedAt)
			},
		},
		{
			name: "success with tags",
			cfg:  driver.RepositoryConfig{Name: "tagged-repo", Tags: map[string]string{"env": "prod"}},
			checkFn: func(t *testing.T, repo *driver.Repository) {
				t.Helper()
				assert.Equal(t, "prod", repo.Tags["env"])
			},
		},
		{
			name:      "empty name",
			cfg:       driver.RepositoryConfig{},
			expectErr: true,
		},
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

			if tc.checkFn != nil {
				tc.checkFn(t, repo)
			}
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
			repoName: "empty-repo",
			setup: func(m *Mock) {
				createTestRepo(&testing.T{}, m, "empty-repo")
			},
		},
		{
			name:     "non-empty repo without force fails",
			repoName: "full-repo",
			force:    false,
			setup: func(m *Mock) {
				createTestRepo(&testing.T{}, m, "full-repo")
				pushTestImage(&testing.T{}, m, "full-repo", "v1")
			},
			expectErr: true,
		},
		{
			name:     "non-empty repo with force succeeds",
			repoName: "full-repo",
			force:    true,
			setup: func(m *Mock) {
				createTestRepo(&testing.T{}, m, "full-repo")
				pushTestImage(&testing.T{}, m, "full-repo", "v1")
			},
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

			_, getErr := m.GetRepository(context.Background(), tc.repoName)
			require.Error(t, getErr)
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
		assert.Contains(t, repo.Name, "my-repo")
		assert.Equal(t, 0, repo.ImageCount)
	})

	t.Run("image count updated", func(t *testing.T) {
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
		checkFn   func(*testing.T, *driver.ImageDetail)
	}{
		{
			name: "success with tag",
			manifest: driver.ImageManifest{
				Repository: "my-repo",
				Tag:        "v1.0",
				SizeBytes:  2048,
			},
			setup: func(m *Mock) {
				createTestRepo(&testing.T{}, m, "my-repo")
			},
			checkFn: func(t *testing.T, detail *driver.ImageDetail) {
				t.Helper()
				assert.Equal(t, "my-repo", detail.Repository)
				assert.Equal(t, []string{"v1.0"}, detail.Tags)
				assert.Equal(t, int64(2048), detail.SizeBytes)
				assert.NotEmpty(t, detail.Digest)
				assert.NotEmpty(t, detail.PushedAt)
				assert.Equal(t, defaultMediaType, detail.MediaType)
			},
		},
		{
			name: "success with custom digest",
			manifest: driver.ImageManifest{
				Repository: "my-repo",
				Tag:        "latest",
				Digest:     "sha256:abc123",
				SizeBytes:  512,
			},
			setup: func(m *Mock) {
				createTestRepo(&testing.T{}, m, "my-repo")
			},
			checkFn: func(t *testing.T, detail *driver.ImageDetail) {
				t.Helper()
				assert.Equal(t, "sha256:abc123", detail.Digest)
			},
		},
		{
			name: "success with custom media type",
			manifest: driver.ImageManifest{
				Repository: "my-repo",
				Tag:        "v2",
				MediaType:  "application/vnd.oci.image.manifest.v1+json",
				SizeBytes:  256,
			},
			setup: func(m *Mock) {
				createTestRepo(&testing.T{}, m, "my-repo")
			},
			checkFn: func(t *testing.T, detail *driver.ImageDetail) {
				t.Helper()
				assert.Equal(t, "application/vnd.oci.image.manifest.v1+json", detail.MediaType)
			},
		},
		{
			name: "repository not found",
			manifest: driver.ImageManifest{
				Repository: "nonexistent",
				Tag:        "v1",
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

			detail, err := m.PutImage(context.Background(), &tc.manifest)
			if tc.expectErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)

			if tc.checkFn != nil {
				tc.checkFn(t, detail)
			}
		})
	}
}

func TestGetImage(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	createTestRepo(t, m, "my-repo")
	img := pushTestImage(t, m, "my-repo", "v1")

	t.Run("by tag", func(t *testing.T) {
		detail, err := m.GetImage(ctx, "my-repo", "v1")
		require.NoError(t, err)
		assert.Equal(t, img.Digest, detail.Digest)
		assert.Contains(t, detail.Tags, "v1")
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

	t.Run("repository not found", func(t *testing.T) {
		_, err := m.GetImage(ctx, "nonexistent", "v1")
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

	t.Run("repository not found", func(t *testing.T) {
		_, err := m.ListImages(ctx, "nonexistent")
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

		_, getErr := m.GetImage(ctx, "my-repo", img.Digest)
		require.Error(t, getErr)
	})

	t.Run("success by digest", func(t *testing.T) {
		img2 := pushTestImage(t, m, "my-repo", "v2")

		err := m.DeleteImage(ctx, "my-repo", img2.Digest)
		require.NoError(t, err)
	})

	t.Run("image not found", func(t *testing.T) {
		err := m.DeleteImage(ctx, "my-repo", "nonexistent")
		require.Error(t, err)
	})

	t.Run("repository not found", func(t *testing.T) {
		err := m.DeleteImage(ctx, "nonexistent", "v1")
		require.Error(t, err)
	})
}

func TestTagImage(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	createTestRepo(t, m, "my-repo")
	img := pushTestImage(t, m, "my-repo", "v1")

	t.Run("add new tag", func(t *testing.T) {
		err := m.TagImage(ctx, "my-repo", "v1", "latest")
		require.NoError(t, err)

		detail, getErr := m.GetImage(ctx, "my-repo", "latest")
		require.NoError(t, getErr)
		assert.Equal(t, img.Digest, detail.Digest)
		assert.Contains(t, detail.Tags, "v1")
		assert.Contains(t, detail.Tags, "latest")
	})

	t.Run("move tag to another image", func(t *testing.T) {
		img2 := pushTestImage(t, m, "my-repo", "v2")

		err := m.TagImage(ctx, "my-repo", "v2", "latest")
		require.NoError(t, err)

		detail, getErr := m.GetImage(ctx, "my-repo", "latest")
		require.NoError(t, getErr)
		assert.Equal(t, img2.Digest, detail.Digest)
	})

	t.Run("image not found", func(t *testing.T) {
		err := m.TagImage(ctx, "my-repo", "nonexistent", "new-tag")
		require.Error(t, err)
	})

	t.Run("repository not found", func(t *testing.T) {
		err := m.TagImage(ctx, "nonexistent", "v1", "new-tag")
		require.Error(t, err)
	})
}

func TestImageTagMutability(t *testing.T) {
	tests := []struct {
		name      string
		mutSetting string
		pushTag   string
		retagTag  string
		expectErr bool
	}{
		{
			name:       "mutable allows overwrite",
			mutSetting: "MUTABLE",
			pushTag:    "v1",
			retagTag:   "v1",
			expectErr:  false,
		},
		{
			name:       "immutable blocks overwrite on push",
			mutSetting: "IMMUTABLE",
			pushTag:    "v1",
			retagTag:   "v1",
			expectErr:  true,
		},
		{
			name:       "immutable allows new tag",
			mutSetting: "IMMUTABLE",
			pushTag:    "v1",
			retagTag:   "v2",
			expectErr:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := newTestMock()
			ctx := context.Background()

			_, err := m.CreateRepository(ctx, driver.RepositoryConfig{
				Name:               "immut-repo",
				ImageTagMutability: tc.mutSetting,
			})
			require.NoError(t, err)

			pushTestImage(t, m, "immut-repo", tc.pushTag)

			_, pushErr := m.PutImage(ctx, &driver.ImageManifest{
				Repository: "immut-repo",
				Tag:        tc.retagTag,
				SizeBytes:  512,
			})

			if tc.expectErr {
				require.Error(t, pushErr)
			} else {
				require.NoError(t, pushErr)
			}
		})
	}
}

func TestLifecyclePolicy(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	createTestRepo(t, m, "my-repo")

	t.Run("no policy returns not found", func(t *testing.T) {
		_, err := m.GetLifecyclePolicy(ctx, "my-repo")
		require.Error(t, err)
	})

	t.Run("put and get policy", func(t *testing.T) {
		policy := driver.LifecyclePolicy{
			Rules: []driver.LifecycleRule{
				{
					Priority:    1,
					Description: "expire old untagged images",
					TagStatus:   "untagged",
					CountType:   "imageCountMoreThan",
					CountValue:  2,
					Action:      "expire",
				},
			},
		}

		err := m.PutLifecyclePolicy(ctx, "my-repo", policy)
		require.NoError(t, err)

		got, getErr := m.GetLifecyclePolicy(ctx, "my-repo")
		require.NoError(t, getErr)
		assert.Equal(t, 1, len(got.Rules))
		assert.Equal(t, "expire old untagged images", got.Rules[0].Description)
	})

	t.Run("evaluate count-based policy", func(t *testing.T) {
		m2, fc2 := newTestMock()
		createTestRepo(t, m2, "count-repo")

		// Push 4 tagged images with unique tags
		for i := range 4 {
			fc2.Advance(time.Minute)
			pushTestImage(t, m2, "count-repo", fmt.Sprintf("v%d", i))
		}

		policyErr := m2.PutLifecyclePolicy(ctx, "count-repo", driver.LifecyclePolicy{
			Rules: []driver.LifecycleRule{
				{
					Priority:   1,
					TagStatus:  "any",
					CountType:  "imageCountMoreThan",
					CountValue: 2,
					Action:     "expire",
				},
			},
		})
		require.NoError(t, policyErr)

		expired, evalErr := m2.EvaluateLifecyclePolicy(ctx, "count-repo")
		require.NoError(t, evalErr)
		assert.Equal(t, 2, len(expired))
	})

	t.Run("evaluate time-based policy", func(t *testing.T) {
		m2, fc2 := newTestMock()
		createTestRepo(t, m2, "time-repo")

		pushTestImage(t, m2, "time-repo", "old-tag")

		fc2.Advance(31 * 24 * time.Hour) // advance 31 days

		err := m2.PutLifecyclePolicy(ctx, "time-repo", driver.LifecyclePolicy{
			Rules: []driver.LifecycleRule{
				{
					Priority:   1,
					TagStatus:  "any",
					CountType:  "sinceImagePushed",
					CountValue: 30,
					Action:     "expire",
				},
			},
		})
		require.NoError(t, err)

		expired, evalErr := m2.EvaluateLifecyclePolicy(ctx, "time-repo")
		require.NoError(t, evalErr)
		assert.Equal(t, 1, len(expired))
	})

	t.Run("repository not found", func(t *testing.T) {
		err := m.PutLifecyclePolicy(ctx, "nonexistent", driver.LifecyclePolicy{})
		require.Error(t, err)

		_, getErr := m.GetLifecyclePolicy(ctx, "nonexistent")
		require.Error(t, getErr)

		_, evalErr := m.EvaluateLifecyclePolicy(ctx, "nonexistent")
		require.Error(t, evalErr)
	})
}

func TestImageScan(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	createTestRepo(t, m, "my-repo")
	pushTestImage(t, m, "my-repo", "v1")

	t.Run("start scan", func(t *testing.T) {
		result, err := m.StartImageScan(ctx, "my-repo", "v1")
		require.NoError(t, err)
		assert.Equal(t, scanStatusComplete, result.Status)
		assert.NotEmpty(t, result.Digest)
		assert.NotEmpty(t, result.CompletedAt)
		assert.NotNil(t, result.FindingCounts)
	})

	t.Run("get scan results", func(t *testing.T) {
		result, err := m.GetImageScanResults(ctx, "my-repo", "v1")
		require.NoError(t, err)
		assert.Equal(t, scanStatusComplete, result.Status)
		assert.Contains(t, result.FindingCounts, "CRITICAL")
		assert.Contains(t, result.FindingCounts, "HIGH")
		assert.Contains(t, result.FindingCounts, "MEDIUM")
		assert.Contains(t, result.FindingCounts, "LOW")
		assert.Contains(t, result.FindingCounts, "INFORMATIONAL")
	})

	t.Run("scan on push enabled", func(t *testing.T) {
		_, repoErr := m.CreateRepository(ctx, driver.RepositoryConfig{
			Name:            "scan-repo",
			ImageScanOnPush: true,
		})
		require.NoError(t, repoErr)

		pushTestImage(t, m, "scan-repo", "auto-scan")

		result, err := m.GetImageScanResults(ctx, "scan-repo", "auto-scan")
		require.NoError(t, err)
		assert.Equal(t, scanStatusComplete, result.Status)
	})

	t.Run("no scan results", func(t *testing.T) {
		_, repoErr := m.CreateRepository(ctx, driver.RepositoryConfig{Name: "no-scan-repo"})
		require.NoError(t, repoErr)

		pushTestImage(t, m, "no-scan-repo", "v1")

		_, err := m.GetImageScanResults(ctx, "no-scan-repo", "v1")
		require.Error(t, err)
	})

	t.Run("image not found", func(t *testing.T) {
		_, err := m.StartImageScan(ctx, "my-repo", "nonexistent")
		require.Error(t, err)
	})

	t.Run("repository not found", func(t *testing.T) {
		_, err := m.StartImageScan(ctx, "nonexistent", "v1")
		require.Error(t, err)
	})
}

func TestMetricsEmission(t *testing.T) {
	m, mon, _ := newTestMockWithMonitoring()
	ctx := context.Background()

	createTestRepo(t, m, "my-repo")

	t.Run("push emits metric", func(t *testing.T) {
		pushTestImage(t, m, "my-repo", "v1")

		metrics, err := mon.GetMetricData(ctx, mondriver.GetMetricInput{
			Namespace:  "artifactregistry.googleapis.com",
			MetricName: "push_request_count",
			Dimensions: map[string]string{"repository_name": "my-repo"},
			StartTime:  time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			EndTime:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			Period:     60,
			Stat:       "Sum",
		})
		require.NoError(t, err)
		assert.Greater(t, len(metrics.Values), 0)
	})

	t.Run("pull emits metric", func(t *testing.T) {
		_, err := m.GetImage(ctx, "my-repo", "v1")
		require.NoError(t, err)

		metrics, getErr := mon.GetMetricData(ctx, mondriver.GetMetricInput{
			Namespace:  "artifactregistry.googleapis.com",
			MetricName: "pull_request_count",
			Dimensions: map[string]string{"repository_name": "my-repo"},
			StartTime:  time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			EndTime:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			Period:     60,
			Stat:       "Sum",
		})
		require.NoError(t, getErr)
		assert.Greater(t, len(metrics.Values), 0)
	})
}
