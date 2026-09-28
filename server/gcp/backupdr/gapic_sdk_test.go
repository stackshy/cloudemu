package backupdr_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	gapic "cloud.google.com/go/backupdr/apiv1"
	"cloud.google.com/go/backupdr/apiv1/backupdrpb"
	"google.golang.org/api/option"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
	bdrserver "github.com/stackshy/cloudemu/v2/server/gcp/backupdr"
)

// newGAPIC returns the idiomatic cloud.google.com/go/backupdr/apiv1 REST client
// pointed at srv. Unlike the google.golang.org/api discovery client, the GAPIC
// client decodes a completed operation's `response` Any and fails Wait when it
// is absent, so it is the client that proves the LRO wire shape.
func newGAPIC(t *testing.T, srv http.Handler) *gapic.Client {
	t.Helper()

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	c, err := gapic.NewRESTClient(context.Background(),
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("NewRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = c.Close() })

	return c
}

// gapicCreate creates a vault and waits for the returned operation.
func gapicCreate(t *testing.T, c *gapic.Client, id string) (*backupdrpb.BackupVault, string) {
	t.Helper()

	ctx := context.Background()

	op, err := c.CreateBackupVault(ctx, &backupdrpb.CreateBackupVaultRequest{
		Parent:        "projects/" + sdkProject + "/locations/" + sdkLocation,
		BackupVaultId: id,
		BackupVault: &backupdrpb.BackupVault{
			BackupMinimumEnforcedRetentionDuration: durationpb.New(86400e9),
		},
	})
	if err != nil {
		t.Fatalf("CreateBackupVault: %v", err)
	}

	v, err := op.Wait(ctx)
	if err != nil {
		t.Fatalf("CreateBackupVault Wait: %v", err)
	}

	return v, op.Name()
}

// assertDeleteWaits deletes name through the GAPIC client, then Waits both on
// the returned operation (the inline response) and on a handle rebuilt from the
// operation name (which forces a poll), so both shapes must carry the Empty
// response the client requires.
func assertDeleteWaits(t *testing.T, c *gapic.Client, name string) {
	t.Helper()

	ctx := context.Background()

	op, err := c.DeleteBackupVault(ctx, &backupdrpb.DeleteBackupVaultRequest{Name: name})
	if err != nil {
		t.Fatalf("DeleteBackupVault: %v", err)
	}

	if err := op.Wait(ctx); err != nil {
		t.Fatalf("DeleteBackupVault Wait (inline response): %v", err)
	}

	if err := c.DeleteBackupVaultOperation(op.Name()).Wait(ctx); err != nil {
		t.Fatalf("DeleteBackupVault Wait (polled %s): %v", op.Name(), err)
	}

	if _, err := c.GetBackupVault(ctx, &backupdrpb.GetBackupVaultRequest{Name: name}); err == nil {
		t.Fatalf("GetBackupVault after delete succeeded, want NOT_FOUND")
	}
}

// TestGAPICDeleteWaitSharedPoller drives create+delete with Wait through the
// assembled GCP server, where operation polls go to the shared LRO poller.
func TestGAPICDeleteWaitSharedPoller(t *testing.T) {
	cloud := cloudemu.NewGCP(config.WithClock(config.NewFakeClock(fixedNow)))
	c := newGAPIC(t, gcpserver.NewFromProvider(cloud))

	v, _ := gapicCreate(t, c, "gapic-vault")
	assertDeleteWaits(t, c, v.GetName())
}

// TestGAPICDeleteWaitStandalone drives the same flow against a standalone
// package handler (no shared registry), which serves its own operation polls.
func TestGAPICDeleteWaitStandalone(t *testing.T) {
	cloud := cloudemu.NewGCP(config.WithClock(config.NewFakeClock(fixedNow)))
	c := newGAPIC(t, bdrserver.New(cloud.BackupDR))

	v, opName := gapicCreate(t, c, "gapic-vault")

	// A create poll through the standalone handler must carry the vault too.
	polled, err := c.CreateBackupVaultOperation(opName).Wait(context.Background())
	if err != nil {
		t.Fatalf("CreateBackupVault Wait (polled %s): %v", opName, err)
	}

	if polled.GetName() != v.GetName() {
		t.Fatalf("polled create response name = %q, want %q", polled.GetName(), v.GetName())
	}

	assertDeleteWaits(t, c, v.GetName())
}

// fixedNow is the fake-clock start for the GAPIC tests.
//
//nolint:gochecknoglobals // immutable test fixture
var fixedNow = time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)

// TestStandalonePollShapes: a standalone poll of an unknown operation, or of a
// create whose vault has since been deleted, replays google.protobuf.Empty.
func TestStandalonePollShapes(t *testing.T) {
	cloud := cloudemu.NewGCP(config.WithClock(config.NewFakeClock(fixedNow)))
	ts := httptest.NewServer(bdrserver.New(cloud.BackupDR))
	t.Cleanup(ts.Close)

	c := newGAPIC(t, bdrserver.New(cloud.BackupDR))
	v, createOp := gapicCreate(t, c, "gone")
	assertDeleteWaits(t, c, v.GetName())

	parent := "projects/" + sdkProject + "/locations/" + sdkLocation
	for _, name := range []string{createOp, parent + "/operations/never-created"} {
		resp, err := http.Get(ts.URL + "/v1/" + name) //nolint:noctx // test poll
		if err != nil {
			t.Fatalf("poll %s: %v", name, err)
		}

		var op struct {
			Done     bool           `json:"done"`
			Response map[string]any `json:"response"`
		}

		err = json.NewDecoder(resp.Body).Decode(&op)
		resp.Body.Close()

		if err != nil || !op.Done || op.Response["@type"] != "type.googleapis.com/google.protobuf.Empty" {
			t.Fatalf("poll %s = %+v (%v), want done with an Empty response", name, op, err)
		}
	}

	resp, err := http.Post(ts.URL+"/v1/"+parent+"/operations/x", "application/json", nil) //nolint:noctx // test
	if err != nil {
		t.Fatalf("POST operation: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST operation = %d, want 405", resp.StatusCode)
	}
}
