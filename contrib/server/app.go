package main

import (
	"context"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/server/serverkit"
)

// Providers wired by the batteries-included server. Kubernetes and OCI are
// intentionally omitted in this build; they arrive in a later PR.
const (
	providerAWS   = "aws"
	providerAzure = "azure"
	providerGCP   = "gcp"
)

// app is the batteries-included emulator: a serverkit.App wired with the selected
// real data-plane engines. serverkit owns provider construction, listener
// binding, the shutdown/snapshot lifecycle, and engine teardown on Close, so this
// package only assembles the engine options and the Config seam.
type app struct {
	cfg appConfig
	kit *serverkit.App
}

// newApp assembles the engine options and builds the serverkit App.
func newApp(cfg *appConfig) (*app, error) {
	opts, err := buildOptions(cfg)
	if err != nil {
		return nil, err
	}

	return newAppFromOptions(cfg, opts)
}

// newAppFromOptions is newApp's core: it builds the serverkit App from an
// already-assembled option set (identity plus engines). Keeping it separate lets
// tests supply a pre-built set so an engine can bind a known port.
func newAppFromOptions(cfg *appConfig, opts []config.Option) (*app, error) {
	kit, err := serverkit.New(serverkitConfig(cfg, opts))
	if err != nil {
		return nil, err
	}

	return &app{cfg: *cfg, kit: kit}, nil
}

// serverkitConfig maps the resolved flag configuration onto a serverkit.Config.
// This build runs aws/azure/gcp with the real-engine BaseOptions plus the admin
// control plane, persistence, and init-dir seeding — k8s/oci/tls parity arrives
// in later PRs. The swap from awsserver.NewFromProvider to serverkit (which
// builds via <provider>server.DriversFrom) is identity-preserving: DriversFrom
// copies AccountID/Region/EnforceAuth verbatim, and with k8s disabled K8sAPI is
// nil on both paths.
func serverkitConfig(cfg *appConfig, opts []config.Option) *serverkit.Config {
	return &serverkit.Config{
		Providers: []string{providerAWS, providerAzure, providerGCP},
		Host:      cfg.host,
		Ports: map[string]string{
			providerAWS:   cfg.awsPort,
			providerAzure: cfg.azurePort,
			providerGCP:   cfg.gcpPort,
		},
		AzureSubscription:   cfg.azureSubscription,
		Admin:               cfg.admin,
		Persist:             cfg.persist,
		StateFile:           cfg.stateFile,
		PersistMetadataOnly: cfg.persistMetaOnly,
		InitDir:             cfg.initDir,
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

// buildOptions assembles the identity options plus the selected engine options.
func buildOptions(cfg *appConfig) ([]config.Option, error) {
	engineOpts, err := cfg.engines.buildEngineOptions()
	if err != nil {
		return nil, err
	}

	const identityOpts = 3

	opts := make([]config.Option, 0, identityOpts+len(engineOpts))
	opts = append(opts,
		config.WithAccountID(cfg.accountID),
		config.WithRegion(cfg.region),
		config.WithProjectID(cfg.projectID),
	)

	return append(opts, engineOpts...), nil
}
