// Package gcs_test — suite cell STORAGE / gcp / sdk-compat.
//
// Real cloud.google.com/go/storage SDK journeys for bucket retention policies +
// bucket lock and object temporary/event-based holds (WORM), driven against the
// emulator's GCP HTTP server with a FakeClock so retention expiry is
// deterministic.
package gcs_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"cloud.google.com/go/storage"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

// newRetentionClient boots a fresh emulator whose GCS driver is backed by a
// FakeClock (returned) so retention math can be advanced deterministically.
func newRetentionClient(t *testing.T) (context.Context, *storage.Client, *config.FakeClock) {
	t.Helper()

	clk := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	cloudP := cloudemu.NewGCP(config.WithClock(clk))
	srv := gcpserver.New(gcpserver.Drivers{Storage: cloudP.GCS})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	ctx := context.Background()

	client, err := storage.NewClient(ctx,
		option.WithEndpoint(ts.URL+"/storage/v1/"),
		option.WithoutAuthentication(),
		option.WithHTTPClient(ts.Client()),
	)
	if err != nil {
		t.Fatalf("storage.NewClient: %v", err)
	}

	client.SetRetry(storage.WithPolicy(storage.RetryNever))
	t.Cleanup(func() { _ = client.Close() })

	return ctx, client, clk
}

func writeRetObject(t *testing.T, ctx context.Context, bkt *storage.BucketHandle, key string) {
	t.Helper()

	w := bkt.Object(key).NewWriter(ctx)
	if _, err := w.Write([]byte("payload")); err != nil {
		t.Fatalf("Write %q: %v", key, err)
	}

	if err := w.Close(); err != nil {
		t.Fatalf("Close %q: %v", key, err)
	}
}

func assertForbidden(t *testing.T, err error) {
	t.Helper()

	if err == nil {
		t.Fatal("expected a 403 error, got nil")
	}

	var apiErr *googleapi.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *googleapi.Error, got %T: %v", err, err)
	}

	if apiErr.Code != http.StatusForbidden {
		t.Fatalf("HTTP status = %d, want 403; err=%v", apiErr.Code, err)
	}
}

// TestGCSRetentionPolicyBlocksDeleteUntilElapsed proves a bucket retention
// policy set through the real SDK blocks object deletion until the FakeClock is
// advanced past the retention period, and that the policy round-trips.
func TestGCSRetentionPolicyBlocksDeleteUntilElapsed(t *testing.T) {
	ctx, client, clk := newRetentionClient(t)

	bkt := client.Bucket("worm-bucket")
	if err := bkt.Create(ctx, e2eProject, nil); err != nil {
		t.Fatalf("Create bucket: %v", err)
	}

	if _, err := bkt.Update(ctx, storage.BucketAttrsToUpdate{
		RetentionPolicy: &storage.RetentionPolicy{RetentionPeriod: time.Hour},
	}); err != nil {
		t.Fatalf("set retention policy: %v", err)
	}

	attrs, err := bkt.Attrs(ctx)
	if err != nil {
		t.Fatalf("bucket Attrs: %v", err)
	}

	if attrs.RetentionPolicy == nil || attrs.RetentionPolicy.RetentionPeriod != time.Hour {
		t.Fatalf("retention policy did not round-trip: %+v", attrs.RetentionPolicy)
	}

	if attrs.RetentionPolicy.EffectiveTime.IsZero() {
		t.Error("retention policy EffectiveTime is zero, want a stamped time")
	}

	writeRetObject(t, ctx, bkt, "obj")

	// Delete before the period elapses is forbidden.
	assertForbidden(t, bkt.Object("obj").Delete(ctx))

	// Advance past the period — delete now succeeds.
	clk.Advance(2 * time.Hour)

	if err := bkt.Object("obj").Delete(ctx); err != nil {
		t.Fatalf("Delete after retention elapsed: %v", err)
	}
}

// TestGCSRetentionPolicyBlocksOverwrite proves a retained object cannot be
// overwritten before its retention elapses.
func TestGCSRetentionPolicyBlocksOverwrite(t *testing.T) {
	ctx, client, clk := newRetentionClient(t)

	bkt := client.Bucket("worm-overwrite")
	if err := bkt.Create(ctx, e2eProject, nil); err != nil {
		t.Fatalf("Create bucket: %v", err)
	}

	if _, err := bkt.Update(ctx, storage.BucketAttrsToUpdate{
		RetentionPolicy: &storage.RetentionPolicy{RetentionPeriod: time.Hour},
	}); err != nil {
		t.Fatalf("set retention policy: %v", err)
	}

	writeRetObject(t, ctx, bkt, "obj")

	// Overwrite before the period elapses is forbidden.
	w := bkt.Object("obj").NewWriter(ctx)
	_, _ = w.Write([]byte("new"))
	assertForbidden(t, w.Close())

	clk.Advance(2 * time.Hour)
	writeRetObject(t, ctx, bkt, "obj") // now allowed
}

// TestGCSRetentionLockCannotShorten proves a locked retention policy cannot be
// shortened but can be increased.
func TestGCSRetentionLockCannotShorten(t *testing.T) {
	ctx, client, _ := newRetentionClient(t)

	bkt := client.Bucket("worm-locked")
	if err := bkt.Create(ctx, e2eProject, nil); err != nil {
		t.Fatalf("Create bucket: %v", err)
	}

	if _, err := bkt.Update(ctx, storage.BucketAttrsToUpdate{
		RetentionPolicy: &storage.RetentionPolicy{RetentionPeriod: time.Hour},
	}); err != nil {
		t.Fatalf("set retention policy: %v", err)
	}

	attrs, err := bkt.Attrs(ctx)
	if err != nil {
		t.Fatalf("Attrs: %v", err)
	}

	// Lock the policy (requires a metageneration precondition).
	if err := bkt.If(storage.BucketConditions{MetagenerationMatch: attrs.MetaGeneration}).LockRetentionPolicy(ctx); err != nil {
		t.Fatalf("LockRetentionPolicy: %v", err)
	}

	locked, err := bkt.Attrs(ctx)
	if err != nil {
		t.Fatalf("Attrs after lock: %v", err)
	}

	if locked.RetentionPolicy == nil || !locked.RetentionPolicy.IsLocked {
		t.Fatalf("policy not reported locked: %+v", locked.RetentionPolicy)
	}

	// Shortening a locked policy is forbidden.
	_, err = bkt.Update(ctx, storage.BucketAttrsToUpdate{
		RetentionPolicy: &storage.RetentionPolicy{RetentionPeriod: 30 * time.Minute},
	})
	assertForbidden(t, err)

	// Increasing it is allowed.
	if _, err := bkt.Update(ctx, storage.BucketAttrsToUpdate{
		RetentionPolicy: &storage.RetentionPolicy{RetentionPeriod: 2 * time.Hour},
	}); err != nil {
		t.Fatalf("increase locked retention: %v", err)
	}
}

// TestGCSTemporaryHoldBlocksDelete proves a temporary hold blocks deletion and
// overwrite regardless of retention, and releasing it re-enables both.
func TestGCSTemporaryHoldBlocksDelete(t *testing.T) {
	ctx, client, _ := newRetentionClient(t)

	bkt := client.Bucket("hold-temp")
	if err := bkt.Create(ctx, e2eProject, nil); err != nil {
		t.Fatalf("Create bucket: %v", err)
	}

	writeRetObject(t, ctx, bkt, "obj")

	obj := bkt.Object("obj")

	if _, err := obj.Update(ctx, storage.ObjectAttrsToUpdate{TemporaryHold: true}); err != nil {
		t.Fatalf("set temporary hold: %v", err)
	}

	attrs, err := obj.Attrs(ctx)
	if err != nil {
		t.Fatalf("obj Attrs: %v", err)
	}

	if !attrs.TemporaryHold {
		t.Error("TemporaryHold did not round-trip as true")
	}

	// Delete + overwrite are forbidden while held.
	assertForbidden(t, obj.Delete(ctx))

	w := obj.NewWriter(ctx)
	_, _ = w.Write([]byte("x"))
	assertForbidden(t, w.Close())

	// Release the hold — delete now succeeds.
	if _, err := obj.Update(ctx, storage.ObjectAttrsToUpdate{TemporaryHold: false}); err != nil {
		t.Fatalf("release temporary hold: %v", err)
	}

	if err := obj.Delete(ctx); err != nil {
		t.Fatalf("Delete after hold released: %v", err)
	}
}

// TestGCSEventBasedHoldResetsRetentionClock proves an event-based hold blocks
// deletion and, once released, restarts the retention clock from the release
// instant.
func TestGCSEventBasedHoldResetsRetentionClock(t *testing.T) {
	ctx, client, clk := newRetentionClient(t)

	bkt := client.Bucket("hold-event")
	if err := bkt.Create(ctx, e2eProject, nil); err != nil {
		t.Fatalf("Create bucket: %v", err)
	}

	if _, err := bkt.Update(ctx, storage.BucketAttrsToUpdate{
		RetentionPolicy: &storage.RetentionPolicy{RetentionPeriod: time.Hour},
	}); err != nil {
		t.Fatalf("set retention policy: %v", err)
	}

	writeRetObject(t, ctx, bkt, "obj")
	obj := bkt.Object("obj")

	if _, err := obj.Update(ctx, storage.ObjectAttrsToUpdate{EventBasedHold: true}); err != nil {
		t.Fatalf("set event-based hold: %v", err)
	}

	// Blocked while held even after the original retention window would elapse.
	clk.Advance(2 * time.Hour)
	assertForbidden(t, obj.Delete(ctx))

	// Release the hold — the retention clock restarts from now.
	if _, err := obj.Update(ctx, storage.ObjectAttrsToUpdate{EventBasedHold: false}); err != nil {
		t.Fatalf("release event-based hold: %v", err)
	}

	// Still retained for a fresh full period from the release instant.
	assertForbidden(t, obj.Delete(ctx))

	clk.Advance(2 * time.Hour)
	if err := obj.Delete(ctx); err != nil {
		t.Fatalf("Delete after fresh retention elapsed: %v", err)
	}
}
