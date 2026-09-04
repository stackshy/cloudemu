package main

import (
	"testing"

	"github.com/stackshy/cloudemu/v2/server/serveflags"
	"github.com/stackshy/cloudemu/v2/server/serverkit"
)

// TestServePersistFlagDefaults pins the persist-strategy/persist-interval flag
// names and defaults to the shared serverkit constants. contrib/server has the
// mirror test asserting the SAME constants, so the two entrypoints cannot drift.
func TestServePersistFlagDefaults(t *testing.T) {
	t.Setenv("CLOUDEMU_PERSIST_STRATEGY", "")
	t.Setenv("CLOUDEMU_PERSIST_INTERVAL", "")

	var c serveflags.CommonConfig
	fs := newServeFlagSet(&c)

	strat := fs.Lookup("persist-strategy")
	if strat == nil {
		t.Fatal("--persist-strategy not registered")
	}

	if strat.DefValue != serverkit.DefaultPersistStrategy {
		t.Fatalf("persist-strategy default = %q, want %q", strat.DefValue, serverkit.DefaultPersistStrategy)
	}

	iv := fs.Lookup("persist-interval")
	if iv == nil {
		t.Fatal("--persist-interval not registered")
	}

	if iv.DefValue != serverkit.DefaultPersistInterval.String() {
		t.Fatalf("persist-interval default = %q, want %q", iv.DefValue, serverkit.DefaultPersistInterval.String())
	}

	// The flags flow through to the config.
	if err := fs.Parse([]string{"--persist-strategy", "on-request", "--persist-interval", "3s"}); err != nil {
		t.Fatal(err)
	}

	if c.PersistStrategy != "on-request" {
		t.Fatalf("parsed strategy = %q, want on-request", c.PersistStrategy)
	}

	if c.PersistInterval.String() != "3s" {
		t.Fatalf("parsed interval = %q, want 3s", c.PersistInterval)
	}
}
