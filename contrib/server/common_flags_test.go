package main

import (
	"flag"
	"sort"
	"testing"

	"github.com/stackshy/cloudemu/v2/server/serveflags"
)

// noEnv reports every variable as unset, so defaults are the built-in ones.
func noEnv(string) string { return "" }

// buildParseFlagSet registers the batteries entrypoint's flags exactly as
// parseFlags composes them: the engine selectors plus the shared common set.
func buildParseFlagSet() *flag.FlagSet {
	fs := flag.NewFlagSet("cloudemu-server", flag.ContinueOnError)

	var (
		engines engineSelection
		allReal bool
	)

	registerEngineFlags(fs, &engines, &allReal, noEnv)
	serveflags.RegisterCommon(fs, &serveflags.CommonConfig{}, noEnv)

	return fs
}

// TestBatteriesRegistersEveryCommonFlag mirrors cmd/cloudemu's guard: the
// batteries entrypoint registers every shared common flag (name + default) via
// serveflags.RegisterCommon, so it can't drift from `cloudemu serve`.
func TestBatteriesRegistersEveryCommonFlag(t *testing.T) {
	ref := flag.NewFlagSet("ref", flag.ContinueOnError)
	serveflags.RegisterCommon(ref, &serveflags.CommonConfig{}, noEnv)

	fs := buildParseFlagSet()

	ref.VisitAll(func(rf *flag.Flag) {
		f := fs.Lookup(rf.Name)
		if f == nil {
			t.Errorf("batteries server is missing shared common flag --%s", rf.Name)

			return
		}

		if f.DefValue != rf.DefValue {
			t.Errorf("--%s default = %q in batteries server, want %q (shared)", rf.Name, f.DefValue, rf.DefValue)
		}
	})
}

// TestBatteriesEngineFlagNamesMatchSharedList proves the engine selectors this
// module registers are exactly serveflags.EngineFlags — the single list the lean
// binary's stub detector also ranges over. A rename in one place fails here.
func TestBatteriesEngineFlagNamesMatchSharedList(t *testing.T) {
	fs := buildParseFlagSet()

	var got []string

	fs.VisitAll(func(f *flag.Flag) {
		if serveflags.IsEngineFlag(f.Name) {
			got = append(got, f.Name)
		}
	})

	want := make([]string, 0, len(serveflags.EngineFlags))
	for _, f := range serveflags.EngineFlags {
		want = append(want, f.Name)
	}

	sort.Strings(got)
	sort.Strings(want)

	if len(got) != len(want) {
		t.Fatalf("engine flags registered = %v, want %v", got, want)
	}

	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("engine flags registered = %v, want %v", got, want)
		}
	}
}
