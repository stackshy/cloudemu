// Package gcs e2e suite tests: STORAGE / gcp / portable lifecycle conditions.
//
// These tests exercise the full GCS lifecycle rule condition set beyond age:
// verbatim round-trip through SetLifecycleGCS/GetLifecycleGCS, the extended
// live-object EvaluateLifecycle (createdBefore/matchesStorageClass/prefix/
// suffix/isLive), and the versioning-aware ApplyLifecycleGCS pass
// (numNewerVersions), all driven by the deterministic fake clock.
package gcs

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/storage/driver"
)

func intPtr(v int) *int    { return &v }
func boolPtr(v bool) *bool { return &v }

// marshalLifecycle renders rules into the GCS lifecycle JSON the wire layer
// stores verbatim.
func marshalLifecycle(t *testing.T, rules ...gcsLifecycleRule) []byte {
	t.Helper()

	doc, err := json.Marshal(gcsLifecycleDoc{Rule: rules})
	require.NoError(t, err)

	return doc
}

// TestLifecycleRawRoundTrip stores a config carrying every standard condition
// and reads it back byte-for-byte, and confirms the portable projection.
func TestLifecycleRawRoundTrip(t *testing.T) {
	ctx := context.Background()
	m, _ := newMock(t)

	const bucket = "e2e-lc-roundtrip"
	require.NoError(t, m.CreateBucket(ctx, bucket))

	doc := marshalLifecycle(t,
		gcsLifecycleRule{
			Action: gcsLifecycleAction{Type: "Delete"},
			Condition: gcsLifecycleCondition{
				Age:                     intPtr(30),
				CreatedBefore:           "2026-01-01",
				NumNewerVersions:        intPtr(3),
				IsLive:                  boolPtr(false),
				MatchesStorageClass:     []string{"NEARLINE", "COLDLINE"},
				MatchesPrefix:           []string{"logs/"},
				MatchesSuffix:           []string{".tmp"},
				DaysSinceNoncurrentTime: intPtr(7),
			},
		},
		gcsLifecycleRule{
			Action:    gcsLifecycleAction{Type: "SetStorageClass", StorageClass: "COLDLINE"},
			Condition: gcsLifecycleCondition{Age: intPtr(90)},
		},
	)

	require.NoError(t, m.SetLifecycleGCS(ctx, bucket, doc))

	got, ok, err := m.GetLifecycleGCS(ctx, bucket)
	require.NoError(t, err)
	require.True(t, ok)
	assert.JSONEq(t, string(doc), string(got), "lifecycle must round-trip verbatim")

	// Portable projection surfaces the age subset (no NotFound).
	cfg, err := m.GetLifecycleConfig(ctx, bucket)
	require.NoError(t, err)
	require.Len(t, cfg.Rules, 2)
	assert.Equal(t, 30, cfg.Rules[0].ExpirationDays)
	assert.Equal(t, "logs/", cfg.Rules[0].Prefix)
	assert.Equal(t, 90, cfg.Rules[1].TransitionDays)
	assert.Equal(t, "COLDLINE", cfg.Rules[1].TransitionStorageClass)

	// Unset bucket: not found; missing bucket: not found.
	_, ok, err = m.GetLifecycleGCS(ctx, "e2e-lc-none")
	require.Error(t, err)
	assert.False(t, ok)
}

// TestEvaluateLifecycleRichConditions checks the live-object evaluator honors
// createdBefore, matchesStorageClass, prefix/suffix and isLive — not just age —
// and never over-deletes on unevaluable (customTime) conditions.
func TestEvaluateLifecycleRichConditions(t *testing.T) {
	ctx := context.Background()
	m, clk := newMock(t)

	const bucket = "e2e-lc-eval"
	require.NoError(t, m.CreateBucket(ctx, bucket))

	// Two objects, distinct storage classes, created at t0.
	_, err := m.PutObjectGCS(ctx, bucket, "logs/a.log", []byte("a"), "text/plain", nil,
		&driver.GCSObjectAttrs{StorageClass: "NEARLINE"}, driver.GCSPrecondition{})
	require.NoError(t, err)
	_, err = m.PutObjectGCS(ctx, bucket, "logs/b.txt", []byte("b"), "text/plain", nil,
		&driver.GCSObjectAttrs{StorageClass: "STANDARD"}, driver.GCSPrecondition{})
	require.NoError(t, err)

	t.Run("matchesStorageClass + prefix", func(t *testing.T) {
		doc := marshalLifecycle(t, gcsLifecycleRule{
			Action: gcsLifecycleAction{Type: "Delete"},
			Condition: gcsLifecycleCondition{
				MatchesStorageClass: []string{"NEARLINE"},
				MatchesPrefix:       []string{"logs/"},
			},
		})
		require.NoError(t, m.SetLifecycleGCS(ctx, bucket, doc))

		expired, evErr := m.EvaluateLifecycle(ctx, bucket)
		require.NoError(t, evErr)
		assert.Equal(t, []string{"logs/a.log"}, expired, "only the NEARLINE object matches")
	})

	t.Run("suffix match", func(t *testing.T) {
		doc := marshalLifecycle(t, gcsLifecycleRule{
			Action:    gcsLifecycleAction{Type: "Delete"},
			Condition: gcsLifecycleCondition{MatchesSuffix: []string{".txt"}},
		})
		require.NoError(t, m.SetLifecycleGCS(ctx, bucket, doc))

		expired, evErr := m.EvaluateLifecycle(ctx, bucket)
		require.NoError(t, evErr)
		assert.Equal(t, []string{"logs/b.txt"}, expired)
	})

	t.Run("createdBefore", func(t *testing.T) {
		// Objects created 2026-07-18; a cutoff the next day matches both.
		doc := marshalLifecycle(t, gcsLifecycleRule{
			Action:    gcsLifecycleAction{Type: "Delete"},
			Condition: gcsLifecycleCondition{CreatedBefore: "2026-07-19"},
		})
		require.NoError(t, m.SetLifecycleGCS(ctx, bucket, doc))

		expired, evErr := m.EvaluateLifecycle(ctx, bucket)
		require.NoError(t, evErr)
		assert.Equal(t, []string{"logs/a.log", "logs/b.txt"}, expired)

		// A cutoff on the creation date itself matches nothing (before is strict).
		doc = marshalLifecycle(t, gcsLifecycleRule{
			Action:    gcsLifecycleAction{Type: "Delete"},
			Condition: gcsLifecycleCondition{CreatedBefore: "2026-07-18"},
		})
		require.NoError(t, m.SetLifecycleGCS(ctx, bucket, doc))

		expired, evErr = m.EvaluateLifecycle(ctx, bucket)
		require.NoError(t, evErr)
		assert.Empty(t, expired)
	})

	t.Run("isLive false skips live objects", func(t *testing.T) {
		doc := marshalLifecycle(t, gcsLifecycleRule{
			Action:    gcsLifecycleAction{Type: "Delete"},
			Condition: gcsLifecycleCondition{IsLive: boolPtr(false), Age: intPtr(0)},
		})
		require.NoError(t, m.SetLifecycleGCS(ctx, bucket, doc))

		expired, evErr := m.EvaluateLifecycle(ctx, bucket)
		require.NoError(t, evErr)
		assert.Empty(t, expired, "isLive:false targets noncurrent versions, not live objects")
	})

	t.Run("age condition with clock", func(t *testing.T) {
		doc := marshalLifecycle(t, gcsLifecycleRule{
			Action:    gcsLifecycleAction{Type: "Delete"},
			Condition: gcsLifecycleCondition{Age: intPtr(10)},
		})
		require.NoError(t, m.SetLifecycleGCS(ctx, bucket, doc))

		expired, evErr := m.EvaluateLifecycle(ctx, bucket)
		require.NoError(t, evErr)
		assert.Empty(t, expired, "not old enough yet")

		clk.Advance(11 * 24 * time.Hour)

		expired, evErr = m.EvaluateLifecycle(ctx, bucket)
		require.NoError(t, evErr)
		assert.Equal(t, []string{"logs/a.log", "logs/b.txt"}, expired)
	})

	t.Run("unevaluable customTime never matches", func(t *testing.T) {
		doc := marshalLifecycle(t, gcsLifecycleRule{
			Action:    gcsLifecycleAction{Type: "Delete"},
			Condition: gcsLifecycleCondition{DaysSinceCustomTime: intPtr(0)},
		})
		require.NoError(t, m.SetLifecycleGCS(ctx, bucket, doc))

		expired, evErr := m.EvaluateLifecycle(ctx, bucket)
		require.NoError(t, evErr)
		assert.Empty(t, expired, "customTime has no backing state; rule must not fire")
	})

	// Evaluation stays non-destructive.
	_, err = m.GetObject(ctx, bucket, "logs/a.log")
	require.NoError(t, err)
}

// TestApplyLifecycleNumNewerVersions drives the destructive versioning-aware
// pass: overwriting a key on a versioned bucket archives prior generations, and
// a Delete rule with numNewerVersions prunes the excess noncurrent versions.
func TestApplyLifecycleNumNewerVersions(t *testing.T) {
	ctx := context.Background()
	m, _ := newMock(t)

	const bucket = "e2e-lc-nnv"
	require.NoError(t, m.CreateBucket(ctx, bucket))
	require.NoError(t, m.SetBucketVersioning(ctx, bucket, true))

	// Four generations of the same key: live = v4; noncurrent = [v1, v2, v3].
	for _, body := range []string{"v1", "v2", "v3", "v4"} {
		require.NoError(t, m.PutObject(ctx, bucket, "obj", []byte(body), "text/plain", nil))
	}

	bkt, ok := m.buckets.Get(bucket)
	require.True(t, ok)
	require.Len(t, bkt.versions["obj"], 3, "three archived generations before pruning")

	// Delete noncurrent versions with >=2 newer versions (including live).
	doc := marshalLifecycle(t, gcsLifecycleRule{
		Action:    gcsLifecycleAction{Type: "Delete"},
		Condition: gcsLifecycleCondition{NumNewerVersions: intPtr(2)},
	})
	require.NoError(t, m.SetLifecycleGCS(ctx, bucket, doc))

	deleted, err := m.ApplyLifecycleGCS(ctx, bucket)
	require.NoError(t, err)
	assert.Len(t, deleted, 2, "the two oldest noncurrent versions have >=2 newer versions")

	// Exactly one noncurrent version survives (the newest, with 1 newer).
	require.Len(t, bkt.versions["obj"], 1)

	// Live object is untouched.
	live, err := m.GetObject(ctx, bucket, "obj")
	require.NoError(t, err)
	assert.Equal(t, []byte("v4"), live.Data)

	// Idempotent second pass deletes nothing more.
	deleted, err = m.ApplyLifecycleGCS(ctx, bucket)
	require.NoError(t, err)
	assert.Empty(t, deleted)
}

// TestApplyLifecycleNoConfig is a no-op when no verbatim config is set.
func TestApplyLifecycleNoConfig(t *testing.T) {
	ctx := context.Background()
	m, _ := newMock(t)

	const bucket = "e2e-lc-apply-empty"
	require.NoError(t, m.CreateBucket(ctx, bucket))

	deleted, err := m.ApplyLifecycleGCS(ctx, bucket)
	require.NoError(t, err)
	assert.Empty(t, deleted)

	_, err = m.ApplyLifecycleGCS(ctx, "no-such-bucket")
	requireCode(t, err, cerrors.NotFound)
}
