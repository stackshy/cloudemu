// Command cloudemu-server runs the CloudEmu standalone wire-protocol server with
// real data-plane engines wired in — the batteries-included variant of
// `cloudemu serve`. The core `cloudemu` module stays dependency-free, so the
// real engines live in contrib and are composed here in their own module.
//
// Assembly (provider construction, listener binding, the shutdown/snapshot
// lifecycle, engine teardown) is delegated to the shared server/serverkit
// package, so this binary and `cloudemu serve` don't drift; this module only adds
// the real-engine selection and its startup MODE banner. The common flags are
// registered from the shared server/serveflags package (the same source
// `cloudemu serve` builds from), so the ~30 shared flags can't drift either.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/stackshy/cloudemu/v2/server/serveflags"
)

// appConfig is the resolved flag/env configuration for one server run. The
// common flags live in the embedded serveflags.CommonConfig (shared with
// `cloudemu serve`); this module only adds the engine selection and the parsed
// provider list.
type appConfig struct {
	serveflags.CommonConfig

	engines engineSelection

	providers []string  // parsed provider set from CommonConfig.Providers
	out       io.Writer // banner/diagnostics sink handed to serverkit
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "cloudemu-server:", err)
		os.Exit(1)
	}
}

// run parses configuration, builds the server, prints the engine MODE banner,
// and blocks until a termination signal (or a fatal serve error) arrives.
// serverkit owns the serve/shutdown lifecycle and the endpoint banner.
func run(args []string, stdout, stderr io.Writer) error {
	cfg, err := parseFlags(args, os.Getenv, stderr)
	if err != nil {
		return err
	}

	cfg.out = stdout

	opts, modes, err := buildOptions(&cfg, dockerAvailable)
	if err != nil {
		return err
	}

	a, err := newAppFromOptions(&cfg, opts)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// The MODE banner prints before serverkit's endpoint banner: serverkit prints
	// its banner from inside Serve (which then blocks), so this is the last chance
	// to emit synchronously. Degrade warnings always go to stderr; the table is
	// suppressed under --quiet.
	warnDegraded(stderr, modes)

	if !cfg.Quiet {
		printEngineModes(stdout, modes)
	}

	// serverkit binds the listeners, prints the endpoint banner, and owns the
	// graceful-shutdown → persistence-snapshot → engine-teardown ordering. Serve
	// blocks until ctx is canceled (SIGINT/SIGTERM) or a listener fails fatally.
	return a.Serve(ctx)
}

// parseFlags resolves the configuration from args. The engine selectors are this
// module's own flags (with environment fallbacks); the ~30 common flags come from
// serveflags.RegisterCommon, and the cross-field checks from CommonConfig.Validate
// — so this entrypoint and `cloudemu serve` build the same serverkit.Config.
func parseFlags(args []string, getenv func(string) string, out io.Writer) (appConfig, error) {
	fs := flag.NewFlagSet("cloudemu-server", flag.ContinueOnError)
	fs.SetOutput(out)

	var (
		cfg     appConfig
		allReal bool
	)

	registerEngineFlags(fs, &cfg.engines, &allReal, getenv)
	serveflags.RegisterCommon(fs, &cfg.CommonConfig, getenv)

	if err := fs.Parse(args); err != nil {
		return appConfig{}, err
	}

	if allReal {
		cfg.engines.applyAllReal()
	}

	sel, err := serveflags.ParseProviders(cfg.Providers)
	if err != nil {
		return appConfig{}, err
	}

	cfg.providers = sel

	if err := cfg.Validate(); err != nil {
		return appConfig{}, err
	}

	warnPersistFlagsWithoutPersist(fs, &cfg, out)

	return cfg, nil
}

// registerEngineFlags registers the real-engine selectors — this module's only
// flags beyond the shared common set. Their names are the single shared list
// serveflags.EngineFlags (asserted by the engine-flag drift test), so the lean
// binary's stub detector and this registration stay in lockstep.
func registerEngineFlags(fs *flag.FlagSet, engines *engineSelection, allReal *bool, getenv func(string) string) {
	fs.StringVar(&engines.db, "db", engineEnvOr(getenv, "CLOUDEMU_DB"), "database engine: off|postgres|mysql|both")
	fs.StringVar(&engines.cache, "cache", engineEnvOr(getenv, "CLOUDEMU_CACHE"), "cache engine: off|redis")
	fs.StringVar(&engines.functions, "functions", engineEnvOr(getenv, "CLOUDEMU_FUNCTIONS"), "function engine: off|subprocess")
	fs.StringVar(&engines.compute, "compute", engineEnvOr(getenv, "CLOUDEMU_COMPUTE"), "compute engine: off|docker")
	fs.StringVar(&engines.containers, "containers", engineEnvOr(getenv, "CLOUDEMU_CONTAINERS"), "container engine: off|docker")
	fs.StringVar(&engines.storage, "storage", engineEnvOr(getenv, "CLOUDEMU_STORAGE"), "storage engine: off|localfs")
	fs.StringVar(&engines.storageDir, "storage-dir", "",
		"root directory for --storage=localfs (default: a temporary directory)")
	fs.BoolVar(allReal, serveflags.EngineAllReal, false,
		"shorthand: postgres + redis + subprocess + docker compute + docker containers + localfs storage")
}

// engineEnvOr returns the engine value from the environment for key, defaulting
// to "off" (in-memory) when it is unset/empty.
func engineEnvOr(getenv func(string) string, key string) string {
	if v := getenv(key); v != "" {
		return v
	}

	return engineOff
}

// warnPersistFlagsWithoutPersist prints a warning for each persist-tuning flag
// set explicitly while --persist is off, so the ignored knob is visible.
func warnPersistFlagsWithoutPersist(fs *flag.FlagSet, cfg *appConfig, stderr io.Writer) {
	if cfg.Persist {
		return
	}

	set := map[string]bool{}

	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })

	for _, name := range []string{"persist-strategy", "persist-interval", "persist-metadata-only"} {
		if set[name] {
			fmt.Fprintf(stderr, "warning: --%s has no effect without --persist\n", name)
		}
	}
}

// printEngineModes prints the resolved per-capability engine MODE table.
// serverkit prints the endpoint banner and SDK hints itself once the listeners
// are bound.
func printEngineModes(w io.Writer, modes []engineMode) {
	fmt.Fprintln(w, "engines:")

	for _, m := range modes {
		line := fmt.Sprintf("  %-11s %s", m.capability, m.status)
		if m.detail != "" {
			line += fmt.Sprintf(" (%s)", m.detail)
		}

		fmt.Fprintln(w, line)
	}
}

// warnDegraded emits one stderr line per capability that fell back to in-memory,
// so the degrade is visible even under --quiet (which suppresses the table).
func warnDegraded(stderr io.Writer, modes []engineMode) {
	for _, m := range modes {
		if m.status == modeMemory {
			fmt.Fprintf(stderr, "warning: %s engine fell back to in-memory (%s)\n", m.capability, m.detail)
		}
	}
}
