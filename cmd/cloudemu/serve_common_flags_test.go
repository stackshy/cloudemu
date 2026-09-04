package main

import (
	"flag"
	"testing"

	"github.com/stackshy/cloudemu/v2/server/serveflags"
)

// commonFlagNames returns the set of flag names a bare serveflags.RegisterCommon
// produces — the shared source of truth both serve entrypoints build from.
func commonFlagNames(t *testing.T) map[string]string {
	t.Helper()

	ref := flag.NewFlagSet("ref", flag.ContinueOnError)
	serveflags.RegisterCommon(ref, &serveflags.CommonConfig{}, func(string) string { return "" })

	names := map[string]string{}
	ref.VisitAll(func(f *flag.Flag) { names[f.Name] = f.DefValue })

	return names
}

// TestServeRegistersEveryCommonFlag proves the lean serve FlagSet is built from
// serveflags.RegisterCommon: every shared common flag (name + default) is present.
// Together with contrib's mirror of this test, neither main can carry a divergent
// copy of a common flag — the drift the shared package removes.
func TestServeRegistersEveryCommonFlag(t *testing.T) {
	t.Setenv("CLOUDEMU_PERSIST_STRATEGY", "")
	t.Setenv("CLOUDEMU_PERSIST_INTERVAL", "")
	t.Setenv("CLOUDEMU_K8S_PROGRESSION", "")
	t.Setenv("CLOUDEMU_K8S_PROGRESSION_INTERVAL", "")

	var c serveflags.CommonConfig

	fs := newServeFlagSet(&c)

	for name, def := range commonFlagNames(t) {
		f := fs.Lookup(name)
		if f == nil {
			t.Errorf("lean serve is missing shared common flag --%s", name)

			continue
		}

		if f.DefValue != def {
			t.Errorf("--%s default = %q in lean serve, want %q (shared)", name, f.DefValue, def)
		}
	}
}
