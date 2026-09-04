package main

import (
	"io"
	"testing"

	"github.com/stackshy/cloudemu/v2/server/serverkit"
)

// TestPersistFlagDefaults pins the persist-strategy/persist-interval defaults to
// the shared serverkit constants — the mirror of cmd/cloudemu's test, so the two
// entrypoints stay in lockstep even though they live in separate modules.
func TestPersistFlagDefaults(t *testing.T) {
	getenv := func(string) string { return "" }

	cfg, err := parseFlags(nil, getenv, io.Discard)
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}

	if cfg.PersistStrategy != serverkit.DefaultPersistStrategy {
		t.Fatalf("default strategy = %q, want %q", cfg.PersistStrategy, serverkit.DefaultPersistStrategy)
	}

	if cfg.PersistInterval != serverkit.DefaultPersistInterval {
		t.Fatalf("default interval = %v, want %v", cfg.PersistInterval, serverkit.DefaultPersistInterval)
	}

	// Overrides flow through.
	over, err := parseFlags([]string{"--persist-strategy", "manual", "--persist-interval", "7s"}, getenv, io.Discard)
	if err != nil {
		t.Fatalf("parseFlags(override): %v", err)
	}

	if over.PersistStrategy != "manual" {
		t.Fatalf("override strategy = %q, want manual", over.PersistStrategy)
	}

	if over.PersistInterval.String() != "7s" {
		t.Fatalf("override interval = %v, want 7s", over.PersistInterval)
	}

	// Env fallback is honored when the flag is absent.
	envGet := func(k string) string {
		switch k {
		case "CLOUDEMU_PERSIST_STRATEGY":
			return "on-request"
		case "CLOUDEMU_PERSIST_INTERVAL":
			return "2s"
		default:
			return ""
		}
	}

	fromEnv, err := parseFlags(nil, envGet, io.Discard)
	if err != nil {
		t.Fatalf("parseFlags(env): %v", err)
	}

	if fromEnv.PersistStrategy != "on-request" {
		t.Fatalf("env strategy = %q, want on-request", fromEnv.PersistStrategy)
	}

	if fromEnv.PersistInterval.String() != "2s" {
		t.Fatalf("env interval = %v, want 2s", fromEnv.PersistInterval)
	}
}
