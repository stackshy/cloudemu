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

	if cfg.persistStrategy != serverkit.DefaultPersistStrategy {
		t.Fatalf("default strategy = %q, want %q", cfg.persistStrategy, serverkit.DefaultPersistStrategy)
	}

	if cfg.persistInterval != serverkit.DefaultPersistInterval {
		t.Fatalf("default interval = %v, want %v", cfg.persistInterval, serverkit.DefaultPersistInterval)
	}

	// Overrides flow through.
	over, err := parseFlags([]string{"--persist-strategy", "manual", "--persist-interval", "7s"}, getenv, io.Discard)
	if err != nil {
		t.Fatalf("parseFlags(override): %v", err)
	}

	if over.persistStrategy != "manual" {
		t.Fatalf("override strategy = %q, want manual", over.persistStrategy)
	}

	if over.persistInterval.String() != "7s" {
		t.Fatalf("override interval = %v, want 7s", over.persistInterval)
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

	if fromEnv.persistStrategy != "on-request" {
		t.Fatalf("env strategy = %q, want on-request", fromEnv.persistStrategy)
	}

	if fromEnv.persistInterval.String() != "2s" {
		t.Fatalf("env interval = %v, want 2s", fromEnv.persistInterval)
	}
}
