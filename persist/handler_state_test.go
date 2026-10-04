package persist_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/persist"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

// wireCall is one request against an in-process wire server.
type wireCall struct {
	method, path, body string
}

func doWire(t *testing.T, h http.Handler, c wireCall) (int, string) {
	t.Helper()

	var body io.Reader
	if c.body != "" {
		body = strings.NewReader(c.body)
	}

	req := httptest.NewRequestWithContext(context.Background(), c.method, c.path, body)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec.Code, rec.Body.String()
}

// roundTrip exports services, JSON-encodes the snapshot, and restores it into
// dst, the way serve --persist does across a restart.
func roundTrip(t *testing.T, cloud string, src, dst persist.Services) {
	t.Helper()

	ctx := context.Background()

	snap, err := persist.ExportAll(ctx, map[string]persist.Services{cloud: src}, persist.Options{})
	if err != nil {
		t.Fatalf("ExportAll: %v", err)
	}

	raw, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}

	var got persist.Snapshot
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal snapshot: %v", err)
	}

	if err := persist.RestoreAll(ctx, &got, map[string]persist.Services{cloud: dst}); err != nil {
		t.Fatalf("RestoreAll: %v", err)
	}
}

// assertSameReads issues each GET against both servers and requires identical
// 200 bodies.
func assertSameReads(t *testing.T, before, after http.Handler, paths []string) {
	t.Helper()

	for _, p := range paths {
		wantCode, want := doWire(t, before, wireCall{method: http.MethodGet, path: p})
		if wantCode != http.StatusOK {
			t.Fatalf("GET %s before restore = %d %s", p, wantCode, want)
		}

		gotCode, got := doWire(t, after, wireCall{method: http.MethodGet, path: p})
		if gotCode != wantCode || got != want {
			t.Errorf("GET %s after restore = %d %s, want %d %s", p, gotCode, got, wantCode, want)
		}
	}
}

func mustWire(t *testing.T, h http.Handler, calls []wireCall) {
	t.Helper()

	for _, c := range calls {
		if code, body := doWire(t, h, c); code >= http.StatusBadRequest {
			t.Fatalf("%s %s = %d %s", c.method, c.path, code, body)
		}
	}
}

// TestPubSubHandlerStateSurvivesRestore covers GPS-N1: subscriptions, their
// config, patched topic labels and Pub/Sub snapshots live in the wire handler,
// so a restart used to drop them and Terraform recreated the subscriptions.
// Both orders are covered: restore into a running server (serve --persist) and
// a server attached after the restore.
func TestPubSubHandlerStateSurvivesRestore(t *testing.T) {
	const base = "/v1/projects/p1/"

	src := cloudemu.NewGCP()
	srcSrv := gcpserver.NewFromProvider(src)

	mustWire(t, srcSrv, []wireCall{
		{http.MethodPut, base + "topics/t1", `{"labels":{"env":"dev"}}`},
		{http.MethodPatch, base + "topics/t1", `{"topic":{"labels":{"env":"prod"}},"updateMask":"labels"}`},
		{http.MethodPut, base + "subscriptions/s1", `{"topic":"projects/p1/topics/t1","ackDeadlineSeconds":42,` +
			`"labels":{"team":"a"},"filter":"attributes.k = \"v\"",` +
			`"pushConfig":{"pushEndpoint":"https://example.com/push"}}`},
		{http.MethodPut, base + "subscriptions/s2", `{"topic":"projects/p1/topics/t1"}`},
		{http.MethodPut, base + "snapshots/snap1", `{"subscription":"projects/p1/subscriptions/s2","labels":{"x":"y"}}`},
		{http.MethodPost, base + "topics/t1:publish", `{"messages":[{"data":"aGk=","attributes":{"k":"v"}}]}`},
	})

	reads := []string{
		base + "topics/t1", base + "subscriptions/s1", base + "subscriptions/s2",
		base + "subscriptions", base + "snapshots/snap1",
	}

	t.Run("server before restore", func(t *testing.T) {
		dst := cloudemu.NewGCP()
		dstSrv := gcpserver.NewFromProvider(dst)
		roundTrip(t, "gcp", src.SnapshotServices(), dst.SnapshotServices())
		assertSameReads(t, srcSrv, dstSrv, reads)

		// The published message survives too, so s2 still pulls it.
		code, body := doWire(t, dstSrv, wireCall{http.MethodPost, base + "subscriptions/s2:pull", `{"maxMessages":5}`})
		if code != http.StatusOK || !strings.Contains(body, `"data":"aGk="`) {
			t.Fatalf("pull after restore = %d %s, want the published message", code, body)
		}
	})

	t.Run("server after restore", func(t *testing.T) {
		dst := cloudemu.NewGCP()
		roundTrip(t, "gcp", src.SnapshotServices(), dst.SnapshotServices())
		assertSameReads(t, srcSrv, gcpserver.NewFromProvider(dst), reads)
	})
}

// TestAzureHandlerStateSurvivesRestore covers AZOBS-N5 and the tags/locks
// handler maps: Application Insights components (with billing features),
// management locks and tags-at-scope used to live only in the wire handlers.
// Resource groups are not persisted yet, so the test recreates the group on the
// restored server before reading.
func TestAzureHandlerStateSurvivesRestore(t *testing.T) {
	const (
		sub    = "/subscriptions/00000000-0000-0000-0000-0000000000ab"
		rg     = sub + "/resourceGroups/rg1"
		comp   = rg + "/providers/Microsoft.Insights/components/ai1"
		lock   = rg + "/providers/Microsoft.Authorization/locks/lk1"
		tagsAt = sub + "/providers/Microsoft.Resources/tags/default"
		apiVer = "?api-version=2020-02-02"
	)

	putRG := wireCall{http.MethodPut, rg + apiVer, `{"location":"eastus"}`}

	src := cloudemu.NewAzure()
	srcSrv := azureserver.NewFromProvider(src)

	mustWire(t, srcSrv, []wireCall{
		putRG,
		{http.MethodPut, comp + apiVer, `{"location":"eastus","kind":"web","tags":{"a":"b"},` +
			`"properties":{"Application_Type":"web","RetentionInDays":30}}`},
		{http.MethodPut, comp + "/currentbillingfeatures" + apiVer,
			`{"CurrentBillingFeatures":["Basic"],"DataVolumeCap":{"Cap":5}}`},
		{http.MethodPut, lock + apiVer, `{"properties":{"level":"CanNotDelete","notes":"keep"}}`},
		{http.MethodPut, tagsAt + apiVer, `{"properties":{"tags":{"cost":"42"}}}`},
	})

	dst := cloudemu.NewAzure()
	dstSrv := azureserver.NewFromProvider(dst)
	roundTrip(t, "azure", src.SnapshotServices(), dst.SnapshotServices())
	mustWire(t, dstSrv, []wireCall{putRG})

	assertSameReads(t, srcSrv, dstSrv, []string{
		comp + apiVer, comp + "/currentbillingfeatures" + apiVer, rg + "/providers/Microsoft.Insights/components" + apiVer,
		lock + apiVer, tagsAt + apiVer,
	})

	// The restored lock still protects its group.
	if code, body := doWire(t, dstSrv, wireCall{method: http.MethodDelete, path: rg + apiVer}); code != http.StatusConflict {
		t.Fatalf("DELETE locked group after restore = %d %s, want 409 ScopeLocked", code, body)
	}
}
