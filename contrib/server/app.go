package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

// closer is the Close() surface of a *Provider, cascaded on shutdown to free the
// real engines (embedded Postgres, miniredis, Docker containers) it wired.
type closer interface{ Close() error }

// boundServer is one cloud endpoint bound to a listener and ready to serve.
type boundServer struct {
	name     string
	scheme   string
	listener net.Listener
	server   *http.Server
}

// url returns the endpoint clients dial, using a connectable display host.
func (b boundServer) url(displayHost string) string {
	port := b.listener.Addr().(*net.TCPAddr).Port //nolint:forcetypeassert // always a TCP listener

	return fmt.Sprintf("%s://%s:%d", b.scheme, displayHost, port)
}

// app is a fully wired batteries-included emulator: the three cloud providers
// and their bound wire-protocol listeners.
type app struct {
	cfg         appConfig
	providers   []closer
	servers     []boundServer
	displayHost string
}

// newApp builds the three clouds with the selected engines and binds their
// listeners. It does not start serving — call serve for that.
func newApp(cfg *appConfig) (*app, error) {
	opts, err := buildOptions(cfg)
	if err != nil {
		return nil, err
	}

	return newAppFromOptions(cfg, opts)
}

// newAppFromOptions is newApp's core: it wires the clouds from already-built
// options and binds the listeners. Keeping it separate lets callers supply a
// pre-assembled option set (identity plus engines) directly.
func newAppFromOptions(cfg *appConfig, opts []config.Option) (*app, error) {
	awsCloud := cloudemu.NewAWS(opts...)
	gcpCloud := cloudemu.NewGCP(opts...)
	// Azure reports its subscription via AccountID; override it last-wins on a
	// copy so the AWS/GCP account ID is untouched.
	azureOpts := append(append([]config.Option{}, opts...), config.WithAccountID(cfg.azureSubscription))
	azureCloud := cloudemu.NewAzure(azureOpts...)

	a := &app{
		cfg:         *cfg,
		providers:   []closer{awsCloud, gcpCloud, azureCloud},
		displayHost: displayHost(cfg.host),
	}

	if err := a.bindAll(awsserver.NewFromProvider(awsCloud),
		gcpserver.NewFromProvider(gcpCloud), azureserver.NewFromProvider(azureCloud)); err != nil {
		_ = a.closeProviders()

		return nil, err
	}

	return a, nil
}

// bindAll binds the AWS/GCP (HTTP) and Azure (HTTPS) listeners. A failure part
// way through closes any listeners already opened.
func (a *app) bindAll(awsH, gcpH, azureH http.Handler) error {
	aws, err := a.bind("aws", "http", a.cfg.awsPort, awsH)
	if err != nil {
		return err
	}

	a.servers = append(a.servers, aws)

	gcp, err := a.bind("gcp", "http", a.cfg.gcpPort, gcpH)
	if err != nil {
		return err
	}

	a.servers = append(a.servers, gcp)

	azure, err := a.bindTLS("azure", a.cfg.azurePort, azureH)
	if err != nil {
		return err
	}

	a.servers = append(a.servers, azure)

	return nil
}

// bind opens a plain-HTTP listener for handler h on the configured host:port.
func (a *app) bind(name, scheme, port string, h http.Handler) (boundServer, error) {
	var lc net.ListenConfig

	ln, err := lc.Listen(context.Background(), "tcp", net.JoinHostPort(a.cfg.host, port))
	if err != nil {
		return boundServer{}, fmt.Errorf("bind %s: %w", name, err)
	}

	return boundServer{name: name, scheme: scheme, listener: ln, server: &http.Server{Handler: h, ReadHeaderTimeout: readHeaderTimeout}}, nil
}

// bindTLS opens an HTTPS listener with an in-memory self-signed cert.
func (a *app) bindTLS(name, port string, h http.Handler) (boundServer, error) {
	b, err := a.bind(name, "https", port, h)
	if err != nil {
		return boundServer{}, err
	}

	tlsCfg, err := selfSignedTLSConfig(a.displayHost)
	if err != nil {
		_ = b.listener.Close()

		return boundServer{}, fmt.Errorf("bind %s tls: %w", name, err)
	}

	b.server.TLSConfig = tlsCfg

	return b, nil
}

// serve starts every bound listener in its own goroutine and returns a channel
// that reports the first fatal serve error.
func (a *app) serve() <-chan error {
	errc := make(chan error, len(a.servers))

	for _, s := range a.servers {
		go func(s boundServer) {
			var err error
			if s.server.TLSConfig != nil {
				err = s.server.ServeTLS(s.listener, "", "")
			} else {
				err = s.server.Serve(s.listener)
			}

			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				errc <- fmt.Errorf("serve %s: %w", s.name, err)
			}
		}(s)
	}

	return errc
}

// shutdown gracefully stops the HTTP servers, then closes the providers so the
// real engines (containers, embedded Postgres, miniredis) are freed.
func (a *app) shutdown(ctx context.Context) error {
	var errs []error

	for _, s := range a.servers {
		if err := s.server.Shutdown(ctx); err != nil {
			errs = append(errs, err)
		}
	}

	errs = append(errs, a.closeProviders())

	return errors.Join(errs...)
}

// closeProviders closes every provider, cascading teardown to wired engines.
func (a *app) closeProviders() error {
	var errs []error

	for _, p := range a.providers {
		if err := p.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
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

// displayHost maps a bind host to one clients can actually dial: 0.0.0.0 / :: are
// not connectable, so advertise loopback instead.
func displayHost(host string) string {
	switch host {
	case "", "0.0.0.0", "::", "[::]":
		return "127.0.0.1"
	default:
		return host
	}
}
