package main

import (
	"context"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/server/serverkit"
)

// Provider names accepted by --providers and mapped onto serverkit. OCI is
// opt-in (not in the default set); Kubernetes is a shared data-plane toggled by
// --k8s-port rather than a provider.
const (
	providerAWS   = "aws"
	providerAzure = "azure"
	providerGCP   = "gcp"
	providerOCI   = "oci"
)

// app is the batteries-included emulator: a serverkit.App wired with the selected
// real data-plane engines. serverkit owns provider construction, listener
// binding, the shutdown/snapshot lifecycle, and engine teardown on Close, so this
// package only assembles the engine options and the Config seam.
type app struct {
	cfg appConfig
	kit *serverkit.App
}

// newAppFromOptions builds the serverkit App from an already-assembled option set
// (identity plus engines). Keeping it separate from option-building lets tests
// supply a pre-built set so an engine can bind a known port.
func newAppFromOptions(cfg *appConfig, opts []config.Option) (*app, error) {
	kit, err := serverkit.New(serverkitConfig(cfg, opts))
	if err != nil {
		return nil, err
	}

	return &app{cfg: *cfg, kit: kit}, nil
}

// serverkitConfig maps the resolved flag configuration onto a serverkit.Config.
// serverkit builds every selected provider (aws/azure/gcp/oci) via
// <provider>server.DriversFrom, wires the shared Kubernetes data-plane, admin
// control plane, persistence, seeding, TLS, latency, request logging, and auth
// enforcement, and owns the serve/shutdown/snapshot lifecycle plus engine
// teardown — this module only supplies the real-engine BaseOptions.
func serverkitConfig(cfg *appConfig, opts []config.Option) *serverkit.Config {
	return &serverkit.Config{
		Providers:     cfg.providers,
		Host:          cfg.host,
		AdvertiseHost: cfg.advertiseHost,
		Ports: map[string]string{
			providerAWS:   cfg.awsPort,
			providerAzure: cfg.azurePort,
			providerGCP:   cfg.gcpPort,
			providerOCI:   cfg.ociPort,
		},
		K8sPort:             cfg.k8sPort,
		AzureSubscription:   cfg.azureSubscription,
		Admin:               cfg.admin,
		Persist:             cfg.persist,
		StateFile:           cfg.stateFile,
		PersistMetadataOnly: cfg.persistMetaOnly,
		InitDir:             cfg.initDir,
		TLSCert:             cfg.tlsCert,
		TLSKey:              cfg.tlsKey,
		TLSHosts:            cfg.tlsHosts,
		Latency:             cfg.latency,
		LogRequests:         cfg.logRequests,
		Quiet:               cfg.quiet,
		EnforceAuth:         cfg.enforceAuth,
		EndpointsFile:       cfg.endpointsFile,
		BaseOptions:         opts,
		ShutdownTimeout:     cfg.shutdownTimeout,
		Out:                 cfg.out,
	}
}

// Serve runs the emulator until ctx is canceled, then gracefully shuts down and
// tears down every wired engine. It delegates wholesale to serverkit.
func (a *app) Serve(ctx context.Context) error {
	return a.kit.Serve(ctx)
}

// buildOptions assembles the identity options plus the selected engine options,
// and returns the per-capability MODE table for the startup banner. dockerProbe
// reports whether the docker CLI is available; a Docker-backed engine degrades to
// in-memory (a MODE row) when it is not.
func buildOptions(cfg *appConfig, dockerProbe func() bool) ([]config.Option, []engineMode, error) {
	engineOpts, modes, err := cfg.engines.buildEngineOptions(dockerProbe)
	if err != nil {
		return nil, nil, err
	}

	const identityOpts = 3

	opts := make([]config.Option, 0, identityOpts+len(engineOpts))
	opts = append(opts,
		config.WithAccountID(cfg.accountID),
		config.WithRegion(cfg.region),
		config.WithProjectID(cfg.projectID),
	)

	return append(opts, engineOpts...), modes, nil
}
