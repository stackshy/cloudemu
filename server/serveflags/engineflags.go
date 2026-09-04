package serveflags

// EngineAllReal is the one boolean engine flag (a shorthand for the full real
// engine set); every other engine flag takes a string value.
const EngineAllReal = "all-real"

// EngineFlag names a real-engine selector understood by the :engines image
// (contrib/server) but only stubbed in the lean cloudemu binary. Env is its
// environment-variable form ("" when it has none).
type EngineFlag struct{ Name, Env string }

// EngineFlags is the single source of truth for the real-engine selector flags.
// Both entrypoints range over it: contrib/server registers each as a real flag,
// the lean binary registers them as inert stubs and detects intent. Adding a new
// engine capability is one line here, picked up everywhere.
//
//nolint:gochecknoglobals // single source of truth for engine flag names/envs, shared by both serve entrypoints
var EngineFlags = []EngineFlag{
	{"db", "CLOUDEMU_DB"},
	{"cache", "CLOUDEMU_CACHE"},
	{"functions", "CLOUDEMU_FUNCTIONS"},
	{"compute", "CLOUDEMU_COMPUTE"},
	{"containers", "CLOUDEMU_CONTAINERS"},
	{"storage", "CLOUDEMU_STORAGE"},
	{"storage-dir", ""},
	{EngineAllReal, ""},
}

// IsEngineFlag reports whether name is one of the real-engine selector flags.
func IsEngineFlag(name string) bool {
	for _, f := range EngineFlags {
		if f.Name == name {
			return true
		}
	}

	return false
}
