package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/server/serveflags"
)

// TestServeEngineFlagsPointToEnginesImage verifies that engine flags/env passed
// to the lean binary return the :engines pointer error before any server starts,
// instead of being silently ignored.
func TestServeEngineFlagsPointToEnginesImage(t *testing.T) {
	cases := []struct {
		name string
		args []string
		env  map[string]string
	}{
		{"db-flag", []string{"--db=postgres"}, nil},
		{"all-real", []string{"--all-real"}, nil},
		{"storage-dir", []string{"--storage-dir=/x"}, nil},
		{"env-only", nil, map[string]string{"CLOUDEMU_DB": "postgres"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}

			err := runServe(tc.args)
			if !errors.Is(err, errEnginesNotInLeanBinary) {
				t.Fatalf("runServe(%v) err = %v, want the :engines pointer", tc.args, err)
			}

			if !strings.Contains(err.Error(), "cloudemu:engines") {
				t.Fatalf("pointer message %q does not mention the :engines image", err)
			}
		})
	}
}

// TestEnginesHintSharedBetweenServeAndDoctor asserts serve and doctor surface the
// one canonical enginesImagePointer in their actual output, so a future edit to
// the const changes every code path and none can silently hardcode a divergent
// hint. It exercises all three call sites: serve's error, serve's --help footer,
// and the doctor report.
func TestEnginesHintSharedBetweenServeAndDoctor(t *testing.T) {
	// serve's engines-pointer error.
	if !strings.Contains(errEnginesNotInLeanBinary.Error(), enginesImagePointer) {
		t.Fatalf("serve error %q does not contain the shared hint %q",
			errEnginesNotInLeanBinary.Error(), enginesImagePointer)
	}

	// serve's --help footer.
	var c serveflags.CommonConfig

	fs := newServeFlagSet(&c)

	var help strings.Builder

	fs.SetOutput(&help)
	fs.Usage()

	if !strings.Contains(help.String(), enginesImagePointer) {
		t.Fatalf("serve --help footer does not contain the shared hint %q:\n%s",
			enginesImagePointer, help.String())
	}

	// doctor's report footer.
	var report strings.Builder

	writeDoctorReport(&report, "127.0.0.1",
		[]portCheck{{"AWS", "4566", true}},
		func(string) bool { return true },
		func(string) (string, error) { return "", errors.New("not found") })

	if !strings.Contains(report.String(), enginesImagePointer) {
		t.Fatalf("doctor report does not contain the shared hint %q:\n%s",
			enginesImagePointer, report.String())
	}
}

// TestServeAccountIDValueIsNotAnEngineFlag is the false-positive guard: with
// `serve --account-id --db`, `--db` is the VALUE of --account-id (a string flag),
// not a set flag. fs.Visit reports account-id set and db not set, so the engines
// pointer must NOT trigger. This proves the stub-flags+fs.Visit approach (a raw
// os.Args pre-scan would wrongly fire here).
func TestServeAccountIDValueIsNotAnEngineFlag(t *testing.T) {
	var c serveflags.CommonConfig

	fs := newServeFlagSet(&c)
	if err := fs.Parse([]string{"--account-id", "--db"}); err != nil {
		t.Fatalf("parse: %v", err)
	}

	if c.AccountID != "--db" {
		t.Fatalf("account-id = %q, want %q (--db consumed as its value)", c.AccountID, "--db")
	}

	if enginesRequested(fs, func(string) string { return "" }) {
		t.Fatal("engines falsely detected: --db was account-id's value, not a set engine flag")
	}
}

// TestServeNoEngineFlagsNotTriggered confirms a plain serve invocation with no
// engine flag/env is not misread as an engine request.
func TestServeNoEngineFlagsNotTriggered(t *testing.T) {
	var c serveflags.CommonConfig

	fs := newServeFlagSet(&c)
	if err := fs.Parse([]string{"--quiet", "--region", "eu-west-1"}); err != nil {
		t.Fatalf("parse: %v", err)
	}

	if enginesRequested(fs, func(string) string { return "" }) {
		t.Fatal("engines detected for a plain serve invocation with no engine flag/env")
	}
}

// TestServeEngineFlagSyntaxes confirms detection works for every flag syntax the
// real parser accepts (--flag=v, --flag v, -flag) and for a non-empty env var.
func TestServeEngineFlagSyntaxes(t *testing.T) {
	syntaxes := [][]string{
		{"--db=postgres"},
		{"--db", "postgres"},
		{"-db", "postgres"},
		{"--all-real"},
	}

	for _, args := range syntaxes {
		var c serveflags.CommonConfig

		fs := newServeFlagSet(&c)
		if err := fs.Parse(args); err != nil {
			t.Fatalf("parse(%v): %v", args, err)
		}

		if !enginesRequested(fs, func(string) string { return "" }) {
			t.Fatalf("engines NOT detected for %v", args)
		}
	}

	// Env form, no flag set.
	var c serveflags.CommonConfig

	fs := newServeFlagSet(&c)
	if err := fs.Parse(nil); err != nil {
		t.Fatalf("parse(nil): %v", err)
	}

	getenv := func(k string) string {
		if k == "CLOUDEMU_CACHE" {
			return "redis"
		}

		return ""
	}

	if !enginesRequested(fs, getenv) {
		t.Fatal("engines NOT detected for CLOUDEMU_CACHE env")
	}
}
