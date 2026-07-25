package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
	"github.com/stackshy/cloudemu/v2/services/kubernetes"
)

// serveConfig holds the resolved serve flags.
type serveConfig struct {
	providers  string
	host       string
	awsPort    string
	azurePort  string
	gcpPort    string
	k8sPort    string
	accountID  string
	region     string
	projectID  string
	latency    time.Duration
	tlsCert    string
	tlsKey     string
	tlsHosts   stringList
	endpoints  string
	logReqs    bool
	quiet      bool
	shutdownTO time.Duration
}

// stringList is a repeatable string flag (e.g. --tls-host a --tls-host b).
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	var c serveConfig
	fs.StringVar(&c.providers, "providers", "aws,azure,gcp", "comma-separated providers to start: aws,azure,gcp")
	fs.StringVar(&c.host, "host", "127.0.0.1", "host/interface to bind (use 0.0.0.0 to expose on the network)")
	fs.StringVar(&c.awsPort, "aws-port", "4566", "port for the AWS endpoint (HTTP)")
	fs.StringVar(&c.azurePort, "azure-port", "4568", "port for the Azure endpoint (HTTPS)")
	fs.StringVar(&c.gcpPort, "gcp-port", "4569", "port for the GCP endpoint (HTTP)")
	fs.StringVar(&c.k8sPort, "k8s-port", "4570", "port for the shared Kubernetes data-plane (HTTP); empty to disable")
	fs.StringVar(&c.accountID, "account-id", "000000000000", "AWS account ID / Azure subscription ID reported by the emulator")
	fs.StringVar(&c.region, "region", "us-east-1", "default region reported by the emulator")
	fs.StringVar(&c.projectID, "project-id", "cloudemu-local", "GCP project ID reported by the emulator")
	fs.DurationVar(&c.latency, "latency", 0, "artificial latency added to every emulated call (e.g. 20ms)")
	fs.StringVar(&c.tlsCert, "tls-cert", "", "PEM cert file for the Azure HTTPS endpoint (default: a self-signed cert generated in memory)")
	fs.StringVar(&c.tlsKey, "tls-key", "", "PEM key file matching --tls-cert")
	fs.Var(&c.tlsHosts, "tls-host", "extra SAN host/IP for the generated self-signed cert (repeatable)")
	fs.StringVar(&c.endpoints, "endpoints-file", "", "write the resolved endpoints as JSON to this path")
	fs.BoolVar(&c.logReqs, "log-requests", false, "log every HTTP request (method, path, status, duration)")
	fs.BoolVar(&c.quiet, "quiet", false, "suppress the startup banner")
	fs.DurationVar(&c.shutdownTO, "shutdown-timeout", 10*time.Second, "grace period for in-flight requests on shutdown")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: cloudemu serve [flags]\n\nStart the standalone emulator. Flags:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if (c.tlsCert == "") != (c.tlsKey == "") {
		return errors.New("--tls-cert and --tls-key must be given together")
	}

	sel, err := parseProviders(c.providers)
	if err != nil {
		return err
	}

	opts := []config.Option{
		config.WithAccountID(c.accountID),
		config.WithRegion(c.region),
		config.WithProjectID(c.projectID),
	}
	if c.latency > 0 {
		opts = append(opts, config.WithLatency(c.latency))
	}

	// One shared Kubernetes data-plane so a kubeconfig from any provider's
	// control plane (EKS/AKS/GKE) reaches the same backend.
	var k8s *kubernetes.APIServer
	if c.k8sPort != "" {
		k8s = kubernetes.NewAPIServer()
	}

	var (
		servers []*namedServer
		eps     = endpointSet{}
	)

	for _, p := range sel {
		switch p {
		case "aws":
			cloud := cloudemu.NewAWS(opts...)
			d := awsserver.DriversFrom(cloud)
			d.K8sAPI = k8s
			h := wrap(awsserver.New(d), "aws", c.logReqs)
			addr := net.JoinHostPort(c.host, c.awsPort)
			servers = append(servers, &namedServer{name: "aws", srv: &http.Server{Addr: addr, Handler: h}})
			eps.AWS = fmt.Sprintf("http://%s", addr)
		case "gcp":
			cloud := cloudemu.NewGCP(opts...)
			d := gcpserver.DriversFrom(cloud)
			d.K8sAPI = k8s
			h := wrap(gcpserver.New(d), "gcp", c.logReqs)
			addr := net.JoinHostPort(c.host, c.gcpPort)
			servers = append(servers, &namedServer{name: "gcp", srv: &http.Server{Addr: addr, Handler: h}})
			eps.GCP = fmt.Sprintf("http://%s", addr)
		case "azure":
			cloud := cloudemu.NewAzure(opts...)
			d := azureserver.DriversFrom(cloud)
			d.K8sAPI = k8s
			h := wrap(azureserver.New(d), "azure", c.logReqs)
			addr := net.JoinHostPort(c.host, c.azurePort)
			tlsCfg, err := tlsConfig(c, addr)
			if err != nil {
				return fmt.Errorf("azure TLS: %w", err)
			}
			servers = append(servers, &namedServer{name: "azure", srv: &http.Server{Addr: addr, Handler: h, TLSConfig: tlsCfg}, tls: true})
			eps.Azure = fmt.Sprintf("https://%s", addr)
		}
	}

	if k8s != nil {
		addr := net.JoinHostPort(c.host, c.k8sPort)
		servers = append(servers, &namedServer{name: "kubernetes", srv: &http.Server{Addr: addr, Handler: wrap(k8s, "kubernetes", c.logReqs)}})
		eps.Kubernetes = fmt.Sprintf("http://%s", addr)
	}

	// Bind every listener before serving so a port clash fails fast, before
	// we print a banner promising endpoints that never came up.
	listeners := make([]net.Listener, len(servers))
	for i, s := range servers {
		ln, err := net.Listen("tcp", s.srv.Addr)
		if err != nil {
			for _, l := range listeners[:i] {
				l.Close()
			}
			return fmt.Errorf("bind %s (%s): %w", s.name, s.srv.Addr, err)
		}
		listeners[i] = ln
	}

	errCh := make(chan error, len(servers))
	for i, s := range servers {
		s := s
		ln := listeners[i]
		go func() {
			var err error
			if s.tls {
				err = s.srv.ServeTLS(ln, "", "")
			} else {
				err = s.srv.Serve(ln)
			}
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCh <- fmt.Errorf("%s: %w", s.name, err)
			}
		}()
	}

	if !c.quiet {
		printBanner(os.Stdout, eps)
	}
	if c.endpoints != "" {
		if err := eps.writeFile(c.endpoints); err != nil {
			return fmt.Errorf("write endpoints file: %w", err)
		}
	}

	// Block until a signal or a fatal serve error.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	select {
	case err := <-errCh:
		return err
	case <-sigCh:
		if !c.quiet {
			fmt.Fprintln(os.Stdout, "\nshutting down…")
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), c.shutdownTO)
	defer cancel()
	var shutErr error
	for _, s := range servers {
		if err := s.srv.Shutdown(ctx); err != nil && shutErr == nil {
			shutErr = err
		}
	}
	return shutErr
}

type namedServer struct {
	name string
	srv  *http.Server
	tls  bool
}

func parseProviders(s string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, raw := range strings.Split(s, ",") {
		p := strings.TrimSpace(strings.ToLower(raw))
		if p == "" {
			continue
		}
		if p != "aws" && p != "azure" && p != "gcp" {
			return nil, fmt.Errorf("unknown provider %q (want aws, azure, or gcp)", p)
		}
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("no providers selected")
	}
	return out, nil
}
