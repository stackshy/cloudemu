package persist_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"strings"
	"testing"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/persist"
)

// TestRestoreWarnsOnUnmatchedService covers #817: a snapshot that carries a
// service the running build no longer exposes must not be silently dropped — the
// skip is intentional, but it is logged as a warning so a state-loss on restore
// is visible in the server logs rather than swallowed.
func TestRestoreWarnsOnUnmatchedService(t *testing.T) {
	ctx := context.Background()

	var buf bytes.Buffer

	prev := log.Writer()
	prevFlags := log.Flags()

	log.SetOutput(&buf)
	log.SetFlags(0)

	t.Cleanup(func() {
		log.SetOutput(prev)
		log.SetFlags(prevFlags)
	})

	// A provider state naming a service ("ghostservice") that the target does not
	// expose. Restore must skip it AND warn.
	ps := persist.ProviderState{Services: map[string]json.RawMessage{
		"ghostservice": json.RawMessage(`{"anything":true}`),
	}}

	dst := cloudemu.NewAWS()
	if err := persist.Restore(ctx, dst.SnapshotServices(), &ps); err != nil {
		t.Fatalf("restore with unmatched service: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "ghostservice") || !strings.Contains(out, "no matching target service") {
		t.Fatalf("expected a warning naming the unmatched service, got log: %q", out)
	}
}

// TestRestoreNoWarnWhenAllServicesMatch is the negative control: restoring a
// snapshot whose services all exist in the target emits no unmatched-service
// warning, so the warn path only fires on genuine surface mismatch.
func TestRestoreNoWarnWhenAllServicesMatch(t *testing.T) {
	ctx := context.Background()

	src := cloudemu.NewAWS()
	if err := src.S3.CreateBucket(ctx, "warn-control"); err != nil {
		t.Fatalf("create bucket: %v", err)
	}

	ps, err := persist.Export(ctx, src.SnapshotServices(), persist.Options{})
	if err != nil {
		t.Fatalf("export: %v", err)
	}

	var buf bytes.Buffer

	prev := log.Writer()
	prevFlags := log.Flags()

	log.SetOutput(&buf)
	log.SetFlags(0)

	t.Cleanup(func() {
		log.SetOutput(prev)
		log.SetFlags(prevFlags)
	})

	dst := cloudemu.NewAWS()
	if err := persist.Restore(ctx, dst.SnapshotServices(), &ps); err != nil {
		t.Fatalf("restore: %v", err)
	}

	if strings.Contains(buf.String(), "no matching target service") {
		t.Fatalf("unexpected unmatched-service warning on a fully-matching restore: %q", buf.String())
	}
}
