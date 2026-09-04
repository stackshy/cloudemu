package serveflags

import (
	"flag"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/server/serverkit"
)

// noEnv is a getenv stub that reports every variable as unset, so flag defaults
// are the built-in ones (not the host environment).
func noEnv(string) string { return "" }

// commonFlagNames is the pinned set of flag names RegisterCommon must register —
// the single source of truth both serve entrypoints build from. A common flag
// added, renamed, or dropped in only one place changes this set and fails the
// test, so the two mains cannot drift. Update it deliberately when the shared
// flag set genuinely changes.
//
//nolint:gochecknoglobals // test fixture: the pinned common-flag name set
var commonFlagNames = []string{
	"account-id", "admin", "advertise-host", "aws-port", "azure-port", "azure-subscription",
	"endpoints-file", "enforce-auth", "gcp-port", "host", "init-dir", "k8s-nodes", "k8s-port",
	"k8s-progression", "k8s-progression-interval", "latency", "log-requests", "oci-port",
	"persist", "persist-interval", "persist-metadata-only", "persist-strategy", "project-id",
	"providers", "quiet", "region", "shutdown-timeout", "state-file", "tls-cert", "tls-host",
	"tls-key", "vcr", "vcr-cassette", "vcr-strict",
}

// registeredNames returns the sorted flag names a fresh RegisterCommon produces.
func registeredNames() []string {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	RegisterCommon(fs, &CommonConfig{}, noEnv)

	var names []string

	fs.VisitAll(func(f *flag.Flag) { names = append(names, f.Name) })
	sort.Strings(names)

	return names
}

// TestRegisterCommonNameSet is the drift guard: the flags RegisterCommon registers
// must be exactly commonFlagNames. Both mains register their common flags here, so
// pinning the set here pins it for both.
func TestRegisterCommonNameSet(t *testing.T) {
	got := registeredNames()

	want := append([]string(nil), commonFlagNames...)
	sort.Strings(want)

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RegisterCommon flag set drifted:\n got=%v\nwant=%v", got, want)
	}
}

// TestRegisterCommonDefaults pins the defaults that flow from shared constants, so
// a change to the persist/k8s defaults can't silently diverge from serverkit.
func TestRegisterCommonDefaults(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	RegisterCommon(fs, &CommonConfig{}, noEnv)

	cases := map[string]string{
		"persist-strategy":         serverkit.DefaultPersistStrategy,
		"persist-interval":         serverkit.DefaultPersistInterval.String(),
		"k8s-progression":          "false",
		"k8s-progression-interval": time.Second.String(),
		"k8s-nodes":                "1",
		"providers":                "aws,azure,gcp",
		"aws-port":                 "4566",
		"shutdown-timeout":         defaultShutdownTimeout.String(),
	}

	for name, want := range cases {
		f := fs.Lookup(name)
		if f == nil {
			t.Fatalf("--%s not registered", name)
		}

		if f.DefValue != want {
			t.Fatalf("--%s default = %q, want %q", name, f.DefValue, want)
		}
	}
}

// TestRegisterCommonEnvFallback proves the injected getenv drives the env-backed
// defaults (contrib relies on an injectable getenv for its tests).
func TestRegisterCommonEnvFallback(t *testing.T) {
	env := map[string]string{
		"CLOUDEMU_PERSIST_STRATEGY":       "on-request",
		"CLOUDEMU_PERSIST_INTERVAL":       "2s",
		"CLOUDEMU_K8S_PROGRESSION":        "true",
		"CLOUDEMU_K8S_PROGRESSION_INTERVAL": "5s",
	}
	getenv := func(k string) string { return env[k] }

	var c CommonConfig

	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	RegisterCommon(fs, &c, getenv)

	if err := fs.Parse(nil); err != nil {
		t.Fatalf("parse: %v", err)
	}

	if c.PersistStrategy != "on-request" {
		t.Fatalf("persist-strategy = %q, want on-request (env)", c.PersistStrategy)
	}

	if c.PersistInterval != 2*time.Second {
		t.Fatalf("persist-interval = %v, want 2s (env)", c.PersistInterval)
	}

	if !c.K8sProgression {
		t.Fatal("k8s-progression = false, want true (env)")
	}

	if c.K8sProgressionInterval != 5*time.Second {
		t.Fatalf("k8s-progression-interval = %v, want 5s (env)", c.K8sProgressionInterval)
	}
}

// TestToServerkitConfigRoundTrip parses a representative arg set and asserts the
// resulting serverkit.Config carries every value through — ports, persistence,
// TLS, k8s progression, and the identity BaseOptions.
func TestToServerkitConfigRoundTrip(t *testing.T) {
	var c CommonConfig

	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	RegisterCommon(fs, &c, noEnv)

	args := []string{
		"--host", "0.0.0.0",
		"--aws-port", "5001", "--azure-port", "5002", "--gcp-port", "5003", "--oci-port", "5004",
		"--k8s-port", "5005", "--advertise-host", "10.0.0.1",
		"--account-id", "210987654321", "--region", "eu-west-2", "--project-id", "proj-x",
		"--azure-subscription", "11111111-1111-1111-1111-111111111111",
		"--latency", "20ms",
		"--tls-cert", "/c.pem", "--tls-key", "/k.pem", "--tls-host", "a", "--tls-host", "b",
		"--endpoints-file", "/eps.json",
		"--admin=false", "--log-requests", "--quiet", "--enforce-auth",
		"--shutdown-timeout", "3s",
		"--persist", "--state-file", "/s.json", "--persist-metadata-only",
		"--persist-strategy", "manual", "--persist-interval", "7s",
		"--init-dir", "/seeds",
		"--k8s-progression", "--k8s-progression-interval", "4s", "--k8s-nodes", "3",
		"--vcr", "record", "--vcr-cassette", "/c.json", "--vcr-strict=false",
	}
	if err := fs.Parse(args); err != nil {
		t.Fatalf("parse: %v", err)
	}

	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}

	sel, err := ParseProviders(c.Providers)
	if err != nil {
		t.Fatalf("parse providers: %v", err)
	}

	sk := c.ToServerkitConfig(sel)

	assertEqual(t, "host", sk.Host, "0.0.0.0")
	assertEqual(t, "aws-port", sk.Ports["aws"], "5001")
	assertEqual(t, "azure-port", sk.Ports["azure"], "5002")
	assertEqual(t, "gcp-port", sk.Ports["gcp"], "5003")
	assertEqual(t, "oci-port", sk.Ports["oci"], "5004")
	assertEqual(t, "k8s-port", sk.K8sPort, "5005")
	assertEqual(t, "advertise-host", sk.AdvertiseHost, "10.0.0.1")
	assertEqual(t, "azure-subscription", sk.AzureSubscription, "11111111-1111-1111-1111-111111111111")
	assertEqual(t, "latency", sk.Latency, 20*time.Millisecond)
	assertEqual(t, "tls-cert", sk.TLSCert, "/c.pem")
	assertEqual(t, "tls-key", sk.TLSKey, "/k.pem")
	assertEqual(t, "endpoints-file", sk.EndpointsFile, "/eps.json")
	assertEqual(t, "admin", sk.Admin, false)
	assertEqual(t, "log-requests", sk.LogRequests, true)
	assertEqual(t, "quiet", sk.Quiet, true)
	assertEqual(t, "enforce-auth", sk.EnforceAuth, true)
	assertEqual(t, "shutdown-timeout", sk.ShutdownTimeout, 3*time.Second)
	assertEqual(t, "persist", sk.Persist, true)
	assertEqual(t, "state-file", sk.StateFile, "/s.json")
	assertEqual(t, "persist-metadata-only", sk.PersistMetadataOnly, true)
	assertEqual(t, "persist-strategy", sk.PersistStrategy, "manual")
	assertEqual(t, "persist-interval", sk.PersistInterval, 7*time.Second)
	assertEqual(t, "init-dir", sk.InitDir, "/seeds")
	assertEqual(t, "k8s-progression", sk.K8sProgression, true)
	assertEqual(t, "k8s-progression-interval", sk.K8sProgressionInterval, 4*time.Second)
	assertEqual(t, "k8s-nodes", sk.K8sNodes, 3)
	assertEqual(t, "vcr", sk.VCRMode, "record")
	assertEqual(t, "vcr-cassette", sk.VCRCassette, "/c.json")
	assertEqual(t, "vcr-strict", sk.VCRStrict, false)

	if !reflect.DeepEqual([]string(sk.TLSHosts), []string{"a", "b"}) {
		t.Fatalf("tls-hosts = %v, want [a b]", sk.TLSHosts)
	}

	// The identity options are seeded into BaseOptions.
	opts := config.NewOptions(sk.BaseOptions...)
	assertEqual(t, "account-id option", opts.AccountID, "210987654321")
	assertEqual(t, "region option", opts.Region, "eu-west-2")
	assertEqual(t, "project-id option", opts.ProjectID, "proj-x")
}

// TestValidate covers the two cross-field rules both mains share.
func TestValidate(t *testing.T) {
	if err := (&CommonConfig{TLSCert: "/c.pem"}).Validate(); err != ErrTLSPairRequired {
		t.Fatalf("tls-cert without tls-key: err = %v, want %v", err, ErrTLSPairRequired)
	}

	if err := (&CommonConfig{TLSKey: "/k.pem"}).Validate(); err != ErrTLSPairRequired {
		t.Fatalf("tls-key without tls-cert: err = %v, want %v", err, ErrTLSPairRequired)
	}

	if err := (&CommonConfig{Persist: true}).Validate(); err != ErrStateFileRequired {
		t.Fatalf("persist without state-file: err = %v, want %v", err, ErrStateFileRequired)
	}

	if err := (&CommonConfig{VCRMode: "record"}).Validate(); err != ErrVCRCassetteRequired {
		t.Fatalf("vcr without cassette: err = %v, want %v", err, ErrVCRCassetteRequired)
	}

	if err := (&CommonConfig{VCRMode: "bogus", VCRCassette: "/c.json"}).Validate(); err == nil {
		t.Fatal("invalid vcr mode should be rejected")
	}

	ok := &CommonConfig{TLSCert: "/c.pem", TLSKey: "/k.pem", Persist: true, StateFile: "/s.json"}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}

	okVCR := &CommonConfig{VCRMode: "replay", VCRCassette: "/c.json"}
	if err := okVCR.Validate(); err != nil {
		t.Fatalf("valid vcr config rejected: %v", err)
	}
}

// TestParseProviders covers de-duplication, validation, and the empty case.
func TestParseProviders(t *testing.T) {
	got, err := ParseProviders("aws, AWS ,gcp,, oci")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if !reflect.DeepEqual(got, []string{"aws", "gcp", "oci"}) {
		t.Fatalf("parsed = %v, want [aws gcp oci]", got)
	}

	if _, err := ParseProviders("aws,bogus"); err == nil {
		t.Fatal("expected error for unknown provider")
	}

	if _, err := ParseProviders(" , "); err == nil {
		t.Fatal("expected error for empty provider set")
	}
}

func assertEqual[T comparable](t *testing.T, name string, got, want T) {
	t.Helper()

	if got != want {
		t.Fatalf("%s = %v, want %v", name, got, want)
	}
}
