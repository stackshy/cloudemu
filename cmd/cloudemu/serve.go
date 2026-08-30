package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/stackshy/cloudemu/v2/server/serveflags"
	"github.com/stackshy/cloudemu/v2/server/serverkit"
)

// enginesImagePointer is the single canonical `docker run` line that points users
// at the :engines image. serve prints it (the error below and --help footer) and
// doctor echoes it, all from this one const so the CLI can never drift on where
// the real engines live.
const enginesImagePointer = "docker run -p 4566:4566 ghcr.io/stackshy/cloudemu:engines --all-real"

// errEnginesNotInLeanBinary is returned when engine flags/env are passed to the
// lean cloudemu binary, which deliberately does not compile the real engines
// (postgres/redis/subprocess/docker/localfs) — those heavy deps live only in the
// :engines image (contrib/server). Rather than silently ignore the intent, serve
// points the user at the batteries image.
var errEnginesNotInLeanBinary = errors.New(
	"real engines (postgres/redis/subprocess/docker/localfs) aren't compiled into the lean cloudemu binary.\n" +
		"run the batteries image:  " + enginesImagePointer + "\n" +
		"(or build ./contrib/server from a repo checkout). see https://cloudemu.info/docs/standalone-server")

func runServe(args []string) error {
	var c serveflags.CommonConfig

	fs := newServeFlagSet(&c)
	if err := fs.Parse(args); err != nil {
		return err
	}

	// The lean binary can't run real engines; point the user at the :engines
	// image before binding listeners or building providers.
	if enginesRequested(fs, os.Getenv) {
		return errEnginesNotInLeanBinary
	}

	if err := c.Validate(); err != nil {
		return err
	}

	warnPersistFlagsWithoutPersist(fs, &c)

	sel, err := serveflags.ParseProviders(c.Providers)
	if err != nil {
		return err
	}

	cfg := c.ToServerkitConfig(sel)

	app, err := serverkit.New(&cfg)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	return app.Serve(ctx)
}

// newServeFlagSet registers every serve flag against c and returns the flag set.
// The common flags come from the shared server/serveflags package (so this lean
// binary and the :engines image can't drift); the engine selectors are registered
// as inert stubs so the real parser handles every syntax and runServe can detect
// engine intent via fs.Visit. Splitting registration out of runServe lets tests
// inspect flag names/defaults without executing a server.
func newServeFlagSet(c *serveflags.CommonConfig) *flag.FlagSet {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	serveflags.RegisterCommon(fs, c, os.Getenv)
	// Register the engine flags as inert stubs so the REAL parser handles every
	// syntax (--db=x, --db x, -db) and an engine name that appears as another
	// flag's value stays a value. runServe detects intent via fs.Visit.
	registerEngineStubs(fs)

	fs.Usage = func() {
		out := fs.Output()
		fmt.Fprintf(out, "Usage: cloudemu serve [flags]\n\nStart the standalone emulator. Flags:\n")
		// Hide the engine stubs from --help: render from a real-only clone so the
		// output matches stdlib's exact formatting without the engine flags, then
		// point at the :engines image in a footer.
		realOnly := flag.NewFlagSet("serve", flag.ContinueOnError)
		realOnly.SetOutput(out)
		serveflags.RegisterCommon(realOnly, &serveflags.CommonConfig{}, os.Getenv)
		realOnly.PrintDefaults()
		fmt.Fprintf(out, "\nReal engines (postgres/redis/subprocess/docker/localfs) live in the "+
			"cloudemu:engines image, not this lean binary:\n"+
			"  "+enginesImagePointer+"\n")
	}

	return fs
}

// registerEngineStubs registers each engine flag as an inert stub whose target is
// discarded. Their only purpose is to let the flag tokenizer accept the flag in
// any form; detection is done afterwards via fs.Visit, never these values.
func registerEngineStubs(fs *flag.FlagSet) {
	for _, f := range serveflags.EngineFlags {
		if f.Name == serveflags.EngineAllReal {
			_ = fs.Bool(f.Name, false, "")

			continue
		}

		_ = fs.String(f.Name, "", "")
	}
}

// enginesRequested reports whether the user asked for a real engine — either by
// setting an engine flag on the command line (detected via fs.Visit after a real
// parse, so an engine name that is another flag's value does not count) or via a
// non-empty engine env var.
func enginesRequested(fs *flag.FlagSet, getenv func(string) string) bool {
	set := false

	fs.Visit(func(f *flag.Flag) {
		if serveflags.IsEngineFlag(f.Name) {
			set = true
		}
	})

	if set {
		return true
	}

	for _, f := range serveflags.EngineFlags {
		if f.Env != "" && getenv(f.Env) != "" {
			return true
		}
	}

	return false
}

// warnPersistFlagsWithoutPersist prints a one-line warning when a persist-tuning
// flag was set explicitly but --persist is off, so the ignored knob is visible.
func warnPersistFlagsWithoutPersist(fs *flag.FlagSet, c *serveflags.CommonConfig) {
	if c.Persist {
		return
	}

	set := map[string]bool{}

	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })

	for _, name := range []string{"persist-strategy", "persist-interval", "persist-metadata-only"} {
		if set[name] {
			fmt.Fprintf(os.Stderr, "warning: --%s has no effect without --persist\n", name)
		}
	}
}
