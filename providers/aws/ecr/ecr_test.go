package ecr

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/cloudwatch"
	"github.com/stackshy/cloudemu/v2/services/containerregistry/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestMock() (*Mock, *config.FakeClock) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"))

	return New(opts), fc
}

func createTestRepo(t *testing.T, m *Mock, name string) {
	t.Helper()

	_, err := m.CreateRepository(context.Background(), driver.RepositoryConfig{Name: name})
	require.NoError(t, err)
}

func pushTestImage(t *testing.T, m *Mock, repo, tag string) *driver.ImageDetail {
	t.Helper()

	// Distinct tags carry distinct manifest content so they content-address to
	// distinct digests (distinct images), matching how real images differ.
	detail, err := m.PutImage(context.Background(), &driver.ImageManifest{
		Repository: repo,
		Tag:        tag,
		SizeBytes:  1024,
		Manifest:   fmt.Sprintf(`{"repo":%q,"tag":%q}`, repo, tag),
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
		{
			name: "success",
			cfg:  driver.RepositoryConfig{Name: "my-repo"},
		},
		{
			name:      "empty name",
			cfg:       driver.RepositoryConfig{},
			expectErr: true,
		},
		{
			name: "duplicate",
			cfg:  driver.RepositoryConfig{Name: "dup"},
			setup: func(m *Mock) {
				createTestRepo(&testing.T{}, m, "dup")
			},
			expectErr: true,
		},
		{
			name: "with tags",
			cfg:  driver.RepositoryConfig{Name: "tagged-repo", Tags: map[string]string{"env": "test"}},
		},
		{
			name: "with immutable tag mutability",
			cfg:  driver.RepositoryConfig{Name: "immutable-repo", ImageTagMutability: "IMMUTABLE"},
		},
		{
			name: "with scan on push",
			cfg:  driver.RepositoryConfig{Name: "scan-repo", ImageScanOnPush: true},
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
			assert.Equal(t, tc.cfg.Name, repo.Name)
			assert.NotEmpty(t, repo.URI)
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
			repoName: "empty-repo",
			setup: func(m *Mock) {
				createTestRepo(&testing.T{}, m, "empty-repo")
			},
		},
		{
			name:      "not found",
			repoName:  "nonexistent",
			expectErr: true,
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
			name:     "force delete with images",
			repoName: "full-repo",
			force:    true,
			setup: func(m *Mock) {
				createTestRepo(&testing.T{}, m, "full-repo")
				pushTestImage(&testing.T{}, m, "full-repo", "v1")
			},
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
		assert.Equal(t, "my-repo", repo.Name)
		assert.Equal(t, 0, repo.ImageCount)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := m.GetRepository(ctx, "nonexistent")
		require.Error(t, err)
	})

	t.Run("image count reflects pushed images", func(t *testing.T) {
		pushTestImage(t, m, "my-repo", "v1")
		pushTestImage(t, m, "my-repo", "v2")

		repo, err := m.GetRepository(ctx, "my-repo")
		require.NoError(t, err)
		assert.Equal(t, 2, repo.ImageCount)
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
			name:     "success",
			manifest: driver.ImageManifest{Repository: "my-repo", Tag: "latest", SizeBytes: 2048},
			setup: func(m *Mock) {
				createTestRepo(&testing.T{}, m, "my-repo")
			},
			checkFn: func(t *testing.T, d *driver.ImageDetail) {
				t.Helper()
				assert.Equal(t, "my-repo", d.Repository)
				assert.NotEmpty(t, d.Digest)
				assert.Contains(t, d.Tags, "latest")
				assert.Equal(t, int64(2048), d.SizeBytes)
				assert.NotEmpty(t, d.PushedAt)
				assert.Equal(t, defaultMediaType, d.MediaType)
			},
		},
		{
			name: "with custom tag",
			manifest: driver.ImageManifest{
				Repository: "my-repo", Tag: "v1.0.0", SizeBytes: 512,
			},
			setup: func(m *Mock) {
				createTestRepo(&testing.T{}, m, "my-repo")
			},
			checkFn: func(t *testing.T, d *driver.ImageDetail) {
				t.Helper()
				assert.Contains(t, d.Tags, "v1.0.0")
			},
		},
		{
			name: "with layers",
			manifest: driver.ImageManifest{
				Repository: "my-repo", Tag: "layered", SizeBytes: 4096,
				Layers: []driver.LayerInfo{
					{Digest: "sha256:aaa", SizeBytes: 1024, MediaType: "application/vnd.docker.image.rootfs.diff.tar.gzip"},
					{Digest: "sha256:bbb", SizeBytes: 3072, MediaType: "application/vnd.docker.image.rootfs.diff.tar.gzip"},
				},
			},
			setup: func(m *Mock) {
				createTestRepo(&testing.T{}, m, "my-repo")
			},
			checkFn: func(t *testing.T, d *driver.ImageDetail) {
				t.Helper()
				assert.NotEmpty(t, d.Digest)
			},
		},
		{
			name:      "repo not found",
			manifest:  driver.ImageManifest{Repository: "nonexistent", Tag: "latest"},
			expectErr: true,
		},
		{
			name: "with explicit digest",
			manifest: driver.ImageManifest{
				Repository: "my-repo", Tag: "pinned",
				Digest: "sha256:abcdef1234567890", SizeBytes: 100,
			},
			setup: func(m *Mock) {
				createTestRepo(&testing.T{}, m, "my-repo")
			},
			checkFn: func(t *testing.T, d *driver.ImageDetail) {
				t.Helper()
				assert.Equal(t, "sha256:abcdef1234567890", d.Digest)
			},
		},
		{
			name: "with custom media type",
			manifest: driver.ImageManifest{
				Repository: "my-repo", Tag: "oci",
				MediaType: "application/vnd.oci.image.manifest.v1+json", SizeBytes: 256,
			},
			setup: func(m *Mock) {
				createTestRepo(&testing.T{}, m, "my-repo")
			},
			checkFn: func(t *testing.T, d *driver.ImageDetail) {
				t.Helper()
				assert.Equal(t, "application/vnd.oci.image.manifest.v1+json", d.MediaType)
			},
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

	t.Run("not found", func(t *testing.T) {
		_, err := m.GetImage(ctx, "my-repo", "nonexistent")
		require.Error(t, err)
	})

	t.Run("repo not found", func(t *testing.T) {
		_, err := m.GetImage(ctx, "nonexistent", "v1")
		require.Error(t, err)
	})
}

func TestListImages(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	createTestRepo(t, m, "my-repo")

	t.Run("empty repo", func(t *testing.T) {
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
		_, err := m.ListImages(ctx, "nonexistent")
		require.Error(t, err)
	})
}

func TestDeleteImage(t *testing.T) {
	tests := []struct {
		name      string
		repo      string
		ref       string
		setup     func(*Mock) string
		expectErr bool
	}{
		{
			name: "success by tag",
			repo: "my-repo",
			ref:  "v1",
			setup: func(m *Mock) string {
				createTestRepo(&testing.T{}, m, "my-repo")
				pushTestImage(&testing.T{}, m, "my-repo", "v1")
				return ""
			},
		},
		{
			name: "success by digest",
			repo: "my-repo",
			setup: func(m *Mock) string {
				createTestRepo(&testing.T{}, m, "my-repo")
				img := pushTestImage(&testing.T{}, m, "my-repo", "v1")
				return img.Digest
			},
		},
		{
			name: "not found",
			repo: "my-repo",
			ref:  "nonexistent",
			setup: func(m *Mock) string {
				createTestRepo(&testing.T{}, m, "my-repo")
				return ""
			},
			expectErr: true,
		},
		{
			name:      "repo not found",
			repo:      "nonexistent",
			ref:       "v1",
			expectErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := newTestMock()

			ref := tc.ref
			if tc.setup != nil {
				if r := tc.setup(m); r != "" {
					ref = r
				}
			}

			err := m.DeleteImage(context.Background(), tc.repo, ref)
			if tc.expectErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)

			_, getErr := m.GetImage(context.Background(), tc.repo, ref)
			require.Error(t, getErr)
		})
	}
}

func TestTagImage(t *testing.T) {
	tests := []struct {
		name      string
		repo      string
		sourceRef string
		targetTag string
		setup     func(*Mock)
		expectErr bool
	}{
		{
			name:      "success",
			repo:      "my-repo",
			sourceRef: "v1",
			targetTag: "latest",
			setup: func(m *Mock) {
				createTestRepo(&testing.T{}, m, "my-repo")
				pushTestImage(&testing.T{}, m, "my-repo", "v1")
			},
		},
		{
			name:      "source not found",
			repo:      "my-repo",
			sourceRef: "nonexistent",
			targetTag: "latest",
			setup: func(m *Mock) {
				createTestRepo(&testing.T{}, m, "my-repo")
			},
			expectErr: true,
		},
		{
			name:      "repo not found",
			repo:      "nonexistent",
			sourceRef: "v1",
			targetTag: "latest",
			expectErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := newTestMock()

			if tc.setup != nil {
				tc.setup(m)
			}

			err := m.TagImage(context.Background(), tc.repo, tc.sourceRef, tc.targetTag)
			if tc.expectErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)

			detail, getErr := m.GetImage(context.Background(), tc.repo, tc.targetTag)
			require.NoError(t, getErr)
			assert.Contains(t, detail.Tags, tc.targetTag)
		})
	}
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
	tests := []struct {
		name       string
		mutability string
		expectErr  bool
	}{
		{
			name:       "MUTABLE allows duplicate tags",
			mutability: "MUTABLE",
			expectErr:  false,
		},
		{
			name:       "IMMUTABLE rejects duplicate tags",
			mutability: "IMMUTABLE",
			expectErr:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := newTestMock()
			ctx := context.Background()

			_, err := m.CreateRepository(ctx, driver.RepositoryConfig{
				Name:               "my-repo",
				ImageTagMutability: tc.mutability,
			})
			require.NoError(t, err)

			_, err = m.PutImage(ctx, &driver.ImageManifest{
				Repository: "my-repo", Tag: "v1", SizeBytes: 100,
			})
			require.NoError(t, err)

			_, err = m.PutImage(ctx, &driver.ImageManifest{
				Repository: "my-repo", Tag: "v1", SizeBytes: 200,
			})

			if tc.expectErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestLifecyclePolicy(t *testing.T) {
	m, fc := newTestMock()
	ctx := context.Background()

	createTestRepo(t, m, "my-repo")

	t.Run("get policy before set returns error", func(t *testing.T) {
		_, err := m.GetLifecyclePolicy(ctx, "my-repo")
		require.Error(t, err)
	})

	policy := driver.LifecyclePolicy{
		Rules: []driver.LifecycleRule{
			{
				Priority:    1,
				Description: "keep only 2 tagged images",
				TagStatus:   "tagged",
				TagPattern:  "*",
				CountType:   "imageCountMoreThan",
				CountValue:  2,
				Action:      "expire",
			},
		},
	}

	t.Run("put lifecycle policy", func(t *testing.T) {
		err := m.PutLifecyclePolicy(ctx, "my-repo", policy)
		require.NoError(t, err)
	})

	t.Run("get lifecycle policy", func(t *testing.T) {
		got, err := m.GetLifecyclePolicy(ctx, "my-repo")
		require.NoError(t, err)
		assert.Equal(t, 1, len(got.Rules))
		assert.Equal(t, "imageCountMoreThan", got.Rules[0].CountType)
		assert.Equal(t, 2, got.Rules[0].CountValue)
	})

	t.Run("evaluate image count rule", func(t *testing.T) {
		pushTestImage(t, m, "my-repo", "v1")
		fc.Advance(time.Minute)
		pushTestImage(t, m, "my-repo", "v2")
		fc.Advance(time.Minute)
		pushTestImage(t, m, "my-repo", "v3")

		expired, err := m.EvaluateLifecyclePolicy(ctx, "my-repo")
		require.NoError(t, err)
		assert.Equal(t, 1, len(expired))
	})

	t.Run("put policy on nonexistent repo", func(t *testing.T) {
		err := m.PutLifecyclePolicy(ctx, "nonexistent", policy)
		require.Error(t, err)
	})

	t.Run("get policy on nonexistent repo", func(t *testing.T) {
		_, err := m.GetLifecyclePolicy(ctx, "nonexistent")
		require.Error(t, err)
	})

	t.Run("evaluate on nonexistent repo", func(t *testing.T) {
		_, err := m.EvaluateLifecyclePolicy(ctx, "nonexistent")
		require.Error(t, err)
	})
}

func TestLifecyclePolicyAgeRule(t *testing.T) {
	m, fc := newTestMock()
	ctx := context.Background()

	createTestRepo(t, m, "age-repo")

	pushTestImage(t, m, "age-repo", "old-v1")
	fc.Advance(time.Minute)
	pushTestImage(t, m, "age-repo", "old-v2")

	fc.Advance(31 * 24 * time.Hour)

	pushTestImage(t, m, "age-repo", "new-v1")

	policy := driver.LifecyclePolicy{
		Rules: []driver.LifecycleRule{
			{
				Priority:    1,
				Description: "expire images older than 30 days",
				TagStatus:   "any",
				CountType:   "sinceImagePushed",
				CountValue:  30,
				Action:      "expire",
			},
		},
	}

	err := m.PutLifecyclePolicy(ctx, "age-repo", policy)
	require.NoError(t, err)

	expired, err := m.EvaluateLifecyclePolicy(ctx, "age-repo")
	require.NoError(t, err)
	assert.Equal(t, 2, len(expired))
}

func TestLifecyclePolicyNoPolicy(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	createTestRepo(t, m, "no-policy-repo")
	pushTestImage(t, m, "no-policy-repo", "v1")

	expired, err := m.EvaluateLifecyclePolicy(ctx, "no-policy-repo")
	require.NoError(t, err)
	assert.Equal(t, 0, len(expired))
}

// TestPreviewLifecyclePolicy exercises PreviewLifecyclePolicy's detail beyond
// what EvaluateLifecyclePolicy exposes: which rule matched each expiring
// image, and that a higher-priority rule's matches are excluded from a
// lower-priority rule's counting pool rather than double-counted.
func TestPreviewLifecyclePolicy(t *testing.T) {
	m, fc := newTestMock()
	ctx := context.Background()

	createTestRepo(t, m, "preview-repo")

	// Priority 1 matches "prod-*" tags and keeps only the newest one, so it
	// expires prod-1 and prod-2 (both older than prod-3). Priority 2 matches
	// everything and keeps 3. Without excluding rule 1's matches from rule 2's
	// pool, rule 2 would see all 4 images (3 prod-* plus "other") and
	// additionally re-expire prod-1 or prod-2; with exclusion it sees only the
	// 2 remaining images (prod-3, other) and expires nothing more, since 2 is
	// not more than 3.
	policy := driver.LifecyclePolicy{
		Rules: []driver.LifecycleRule{
			{
				Priority:   1,
				TagStatus:  "tagged",
				TagPattern: "prod-*",
				CountType:  "imageCountMoreThan",
				CountValue: 1,
				Action:     "expire",
			},
			{
				Priority:   2,
				TagStatus:  "any",
				CountType:  "imageCountMoreThan",
				CountValue: 3,
				Action:     "expire",
			},
		},
	}

	require.NoError(t, m.PutLifecyclePolicy(ctx, "preview-repo", policy))

	prod1 := pushTestImage(t, m, "preview-repo", "prod-1")
	fc.Advance(time.Minute)
	prod2 := pushTestImage(t, m, "preview-repo", "prod-2")
	fc.Advance(time.Minute)
	pushTestImage(t, m, "preview-repo", "prod-3")
	fc.Advance(time.Minute)
	pushTestImage(t, m, "preview-repo", "other")

	results, text, err := m.PreviewLifecyclePolicy(ctx, "preview-repo", nil)
	require.NoError(t, err)
	assert.Empty(t, text) // no Document was set via PutLifecyclePolicy's driver.LifecyclePolicy directly

	require.Len(t, results, 2)

	byDigest := map[string]driver.LifecyclePreviewResult{}
	for _, r := range results {
		byDigest[r.Digest] = r
	}

	got1, ok1 := byDigest[prod1.Digest]
	require.True(t, ok1, "prod-1 should be in the expiring set")
	assert.Equal(t, []string{"prod-1"}, got1.Tags)
	assert.Equal(t, 1, got1.AppliedRulePriority)

	got2, ok2 := byDigest[prod2.Digest]
	require.True(t, ok2, "prod-2 should be in the expiring set")
	assert.Equal(t, []string{"prod-2"}, got2.Tags)
	assert.Equal(t, 1, got2.AppliedRulePriority)

	t.Run("override policy does not persist", func(t *testing.T) {
		override := driver.LifecyclePolicy{
			Rules: []driver.LifecycleRule{
				{Priority: 1, TagStatus: "any", CountType: "imageCountMoreThan", CountValue: 0, Action: "expire"},
			},
		}

		results, _, err := m.PreviewLifecyclePolicy(ctx, "preview-repo", &override)
		require.NoError(t, err)
		assert.Len(t, results, 4) // every image expires under the override's countValue 0

		// The stored policy is untouched: re-running without an override still
		// reports just the two prod-* excess images.
		results, _, err = m.PreviewLifecyclePolicy(ctx, "preview-repo", nil)
		require.NoError(t, err)
		assert.Len(t, results, 2)
	})

	t.Run("no policy and no override", func(t *testing.T) {
		createTestRepo(t, m, "preview-no-policy-repo")

		_, _, err := m.PreviewLifecyclePolicy(ctx, "preview-no-policy-repo", nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no lifecycle policy")
	})

	t.Run("missing repository", func(t *testing.T) {
		_, _, err := m.PreviewLifecyclePolicy(ctx, "ghost-repo", nil)
		require.Error(t, err)
	})
}

func TestImageScan(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	createTestRepo(t, m, "scan-repo")
	pushTestImage(t, m, "scan-repo", "v1")

	t.Run("start scan", func(t *testing.T) {
		result, err := m.StartImageScan(ctx, "scan-repo", "v1")
		require.NoError(t, err)
		assert.Equal(t, scanStatusComplete, result.Status)
		assert.Equal(t, "scan-repo", result.Repository)
		assert.NotEmpty(t, result.Digest)
		assert.NotEmpty(t, result.CompletedAt)
		assert.NotNil(t, result.FindingCounts)
	})

	t.Run("get scan results", func(t *testing.T) {
		result, err := m.GetImageScanResults(ctx, "scan-repo", "v1")
		require.NoError(t, err)
		assert.Equal(t, scanStatusComplete, result.Status)
		assert.Contains(t, result.FindingCounts, "CRITICAL")
		assert.Contains(t, result.FindingCounts, "HIGH")
		assert.Contains(t, result.FindingCounts, "MEDIUM")
		assert.Contains(t, result.FindingCounts, "LOW")
		assert.Contains(t, result.FindingCounts, "INFORMATIONAL")
	})

	t.Run("get scan results no scan", func(t *testing.T) {
		pushTestImage(t, m, "scan-repo", "v2")
		_, err := m.GetImageScanResults(ctx, "scan-repo", "v2")
		require.Error(t, err)
	})

	t.Run("start scan image not found", func(t *testing.T) {
		_, err := m.StartImageScan(ctx, "scan-repo", "nonexistent")
		require.Error(t, err)
	})

	t.Run("start scan repo not found", func(t *testing.T) {
		_, err := m.StartImageScan(ctx, "nonexistent", "v1")
		require.Error(t, err)
	})

	t.Run("get scan results repo not found", func(t *testing.T) {
		_, err := m.GetImageScanResults(ctx, "nonexistent", "v1")
		require.Error(t, err)
	})
}

func TestScanOnPush(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	_, err := m.CreateRepository(ctx, driver.RepositoryConfig{
		Name:            "auto-scan-repo",
		ImageScanOnPush: true,
	})
	require.NoError(t, err)

	pushTestImage(t, m, "auto-scan-repo", "v1")

	result, err := m.GetImageScanResults(ctx, "auto-scan-repo", "v1")
	require.NoError(t, err)
	assert.Equal(t, scanStatusComplete, result.Status)
}

func TestMetricsEmission(t *testing.T) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"))

	cw := cloudwatch.New(opts)
	m := New(opts)
	m.SetMonitoring(cw)

	ctx := context.Background()

	createTestRepo(t, m, "metrics-repo")

	t.Run("PutImage emits ImagePushCount", func(t *testing.T) {
		pushTestImage(t, m, "metrics-repo", "v1")

		metrics, err := cw.ListMetrics(ctx, "AWS/ECR")
		require.NoError(t, err)
		assert.Contains(t, metrics, "ImagePushCount")
	})

	t.Run("GetImage emits ImagePullCount", func(t *testing.T) {
		_, err := m.GetImage(ctx, "metrics-repo", "v1")
		require.NoError(t, err)

		metrics, err := cw.ListMetrics(ctx, "AWS/ECR")
		require.NoError(t, err)
		assert.Contains(t, metrics, "ImagePullCount")
	})
}

// TestRepositoryTagging is a regression guard for issue #319: ECR
// TagResource/UntagResource/ListTagsForResource were unimplemented.
func TestRepositoryTagging(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	createTestRepo(t, m, "r1")

	require.NoError(t, m.TagRepository(ctx, "r1", map[string]string{"env": "prod", "team": "img"}))

	tags, err := m.ListRepositoryTags(ctx, "r1")
	require.NoError(t, err)
	assert.Equal(t, "prod", tags["env"])
	assert.Equal(t, "img", tags["team"])

	require.NoError(t, m.UntagRepository(ctx, "r1", []string{"env"}))

	tags, err = m.ListRepositoryTags(ctx, "r1")
	require.NoError(t, err)
	_, has := tags["env"]
	assert.False(t, has)

	assert.Error(t, m.TagRepository(ctx, "missing", map[string]string{"a": "b"}))
}

// pushManifest pushes a raw manifest with an optional explicit digest and tag,
// used by the image/digest-model tests.
func pushManifest(t *testing.T, m *Mock, repo, tag, digest, manifest string) *driver.ImageDetail {
	t.Helper()

	detail, err := m.PutImage(context.Background(), &driver.ImageManifest{
		Repository: repo,
		Tag:        tag,
		Digest:     digest,
		Manifest:   manifest,
		SizeBytes:  int64(len(manifest)),
	})
	require.NoError(t, err)

	return detail
}

func findByDigest(images []driver.ImageDetail, digest string) *driver.ImageDetail {
	for i := range images {
		if images[i].Digest == digest {
			return &images[i]
		}
	}

	return nil
}

// TestPutImageContentAddressedDigest: an omitted digest is the full sha256 of
// the manifest bytes, so identical content yields the same 64-hex digest under
// any tag, and the two pushes collapse into ONE image carrying both tags.
func TestPutImageContentAddressedDigest(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()
	createTestRepo(t, m, "repo")

	body := `{"schemaVersion":2,"layers":[]}`
	d1 := pushManifest(t, m, "repo", "v1", "", body)
	d2 := pushManifest(t, m, "repo", "v2", "", body)

	assert.Equal(t, d1.Digest, d2.Digest)
	assert.True(t, strings.HasPrefix(d1.Digest, "sha256:"))
	assert.Len(t, strings.TrimPrefix(d1.Digest, "sha256:"), 64)

	// Different manifest content -> a different digest.
	d3 := pushManifest(t, m, "repo", "v3", "", body+" ")
	assert.NotEqual(t, d1.Digest, d3.Digest)

	images, err := m.ListImages(ctx, "repo")
	require.NoError(t, err)
	assert.Equal(t, 2, len(images)) // (v1,v2) manifest + (v3) manifest

	img := findByDigest(images, d1.Digest)
	require.NotNil(t, img)
	assert.ElementsMatch(t, []string{"v1", "v2"}, img.Tags)
}

// TestPutImageExplicitDigestAccumulatesTags: pushing the same digest under a
// second tag accumulates the tag onto the one manifest instead of replacing it.
func TestPutImageExplicitDigestAccumulatesTags(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()
	createTestRepo(t, m, "repo")

	const digest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	first := pushManifest(t, m, "repo", "v1", digest, `{"a":1}`)
	assert.ElementsMatch(t, []string{"v1"}, first.Tags)

	second := pushManifest(t, m, "repo", "v2", digest, `{"a":1}`)
	assert.Equal(t, digest, second.Digest)
	assert.ElementsMatch(t, []string{"v1", "v2"}, second.Tags)

	images, err := m.ListImages(ctx, "repo")
	require.NoError(t, err)
	require.Equal(t, 1, len(images))
	assert.ElementsMatch(t, []string{"v1", "v2"}, images[0].Tags)
}

// TestDeleteImageByTagUntagsOnly: deleting by tag removes only that tag while
// other tags remain; removing the last tag deletes the manifest.
func TestDeleteImageByTagUntagsOnly(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()
	createTestRepo(t, m, "repo")

	const digest = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
	pushManifest(t, m, "repo", "v1", digest, `{"b":1}`)
	pushManifest(t, m, "repo", "v2", digest, `{"b":1}`)

	require.NoError(t, m.DeleteImage(ctx, "repo", "v1"))

	images, err := m.ListImages(ctx, "repo")
	require.NoError(t, err)
	require.Equal(t, 1, len(images))
	assert.ElementsMatch(t, []string{"v2"}, images[0].Tags)

	require.NoError(t, m.DeleteImage(ctx, "repo", "v2"))
	images, err = m.ListImages(ctx, "repo")
	require.NoError(t, err)
	assert.Equal(t, 0, len(images))
}

// TestDeleteImageByDigestRemovesManifest: deleting by digest removes the image
// and every one of its tags.
func TestDeleteImageByDigestRemovesManifest(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()
	createTestRepo(t, m, "repo")

	const digest = "sha256:3333333333333333333333333333333333333333333333333333333333333333"
	pushManifest(t, m, "repo", "v1", digest, `{"c":1}`)
	pushManifest(t, m, "repo", "v2", digest, `{"c":1}`)

	require.NoError(t, m.DeleteImage(ctx, "repo", digest))

	images, err := m.ListImages(ctx, "repo")
	require.NoError(t, err)
	assert.Equal(t, 0, len(images))
}

// TestConcurrentPutAndDelete exercises the model under -race: concurrent
// PutImage/DeleteImage/GetRepository/ListImages on one repository.
func TestConcurrentPutAndDelete(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()
	createTestRepo(t, m, "race")

	const workers = 20

	var wg sync.WaitGroup

	for i := range workers {
		tag := fmt.Sprintf("v%d", i)

		wg.Add(2)

		go func() {
			defer wg.Done()

			_, _ = m.PutImage(ctx, &driver.ImageManifest{
				Repository: "race", Tag: tag, Manifest: fmt.Sprintf(`{"t":%q}`, tag),
			})
		}()

		go func() {
			defer wg.Done()

			_ = m.DeleteImage(ctx, "race", tag)
			_, _ = m.GetRepository(ctx, "race")
			_, _ = m.ListImages(ctx, "race")
		}()
	}

	wg.Wait()
}
