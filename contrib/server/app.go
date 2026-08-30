package main

import (
	"context"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/server/serverkit"
)

// app is the batteries-included emulator: a serverkit.App wired with the selected
// real data-plane engines. serverkit owns provider construction, listener
// binding, the shutdown/snapshot lifecycle, and engine teardown on Close, so this
// package only assembles the engine options and the Config seam.
type app struct {
	cfg appConfig
	kit *serverkit.App
}

// newAppFromOptions builds the serverkit App from an already-assembled engine
// option set. The shared serveflags.ToServerkitConfig maps the common flags onto
// serverkit.Config and seeds BaseOptions with the identity options
// (account/region/project); this appends the real-engine options to BaseOptions
// afterwards and points Out at this run's sink. Keeping option-building separate
// lets tests supply a pre-built engine set so an engine can bind a known port.
func newAppFromOptions(cfg *appConfig, engineOpts []config.Option) (*app, error) {
	skc := cfg.ToServerkitConfig(cfg.providers)
	skc.Out = cfg.out
	skc.BaseOptions = append(skc.BaseOptions, engineOpts...)

	kit, err := serverkit.New(&skc)
	if err != nil {
		return nil, err
	}

	return &app{cfg: *cfg, kit: kit}, nil
}

// Serve runs the emulator until ctx is canceled, then gracefully shuts down and
// tears down every wired engine. It delegates wholesale to serverkit.
func (a *app) Serve(ctx context.Context) error {
	return a.kit.Serve(ctx)
}

// buildOptions assembles the selected engine options and returns the
// per-capability MODE table for the startup banner. The identity options are
// added by ToServerkitConfig, so this returns the engine options only, which
// newAppFromOptions appends to BaseOptions. dockerProbe reports whether the docker
// CLI is available; a Docker-backed engine degrades to in-memory (a MODE row)
// when it is not.
func buildOptions(cfg *appConfig, dockerProbe func() bool) ([]config.Option, []engineMode, error) {
	return cfg.engines.buildEngineOptions(dockerProbe)
}
