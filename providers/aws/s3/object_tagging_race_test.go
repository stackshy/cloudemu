package s3

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// TestPutObjectTaggingConcurrentWithRestore runs PutObjectTagging against
// RestoreObject and object-lock writers on the same object. Every writer must
// take versionsMu and replace the record, so the race detector stays quiet and
// the final object has both the last tag set and the restore.
func TestPutObjectTaggingConcurrentWithRestore(t *testing.T) {
	ctx := context.Background()
	m, _ := newRestoreMock(t, false)

	requireNoError(t, m.EnableObjectLock(ctx, "b"))
	putClass(t, m, "k", storageClassGlacier)

	const rounds = 200

	var wg sync.WaitGroup

	wg.Add(3)

	go func() {
		defer wg.Done()

		for i := 0; i < rounds; i++ {
			_ = m.PutObjectTagging(ctx, "b", "k", map[string]string{"n": fmt.Sprint(i)})
		}
	}()

	go func() {
		defer wg.Done()

		for i := 0; i < rounds; i++ {
			_, _ = m.RestoreObject(ctx, "b", "k", "", driver.RestoreRequest{Days: 1 + i%3})
		}
	}()

	go func() {
		defer wg.Done()

		for i := 0; i < rounds; i++ {
			_ = m.PutObjectLegalHold(ctx, "b", "k", "", i%2 == 0)
			_, _ = m.GetObjectTagging(ctx, "b", "k")
		}
	}()

	wg.Wait()

	requireNoError(t, m.PutObjectTagging(ctx, "b", "k", map[string]string{"final": "yes"}))

	_, err := m.RestoreObject(ctx, "b", "k", "", driver.RestoreRequest{Days: 1})
	requireNoError(t, err)

	tags, err := m.GetObjectTagging(ctx, "b", "k")
	requireNoError(t, err)
	assertEqual(t, "yes", tags["final"])

	info, err := m.HeadObject(ctx, "b", "k")
	requireNoError(t, err)

	if info.Restore == nil {
		t.Fatal("restore lost after concurrent tagging")
	}
}
