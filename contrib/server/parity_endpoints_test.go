package main

import (
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

// TestOCIEndpointBinds boots the batteries server with --providers aws,oci and
// asserts the OCI endpoint binds and answers HTTP — proving --oci-port and the
// oci provider thread through serverkit. It uses a raw HTTP request (no OCI SDK
// dependency): any HTTP status proves the listener is up and the handler runs.
func TestOCIEndpointBinds(t *testing.T) {
	cfg := testConfig(t, allEnginesOff())
	cfg.providers = []string{providerAWS, providerOCI}
	cfg.ociPort = freePortStr(t)

	_, stop := startAWS(t, cfg, mustOptions(t, &cfg))
	defer stop()

	// The AWS listener answering (startAWS waited on it) does not prove OCI bound;
	// wait for the OCI port explicitly, then hit it.
	waitListening(t, cfg.host, cfg.ociPort)

	url := "http://" + net.JoinHostPort(cfg.host, cfg.ociPort) + "/"

	resp, err := http.Get(url) //nolint:noctx // short-lived in-process test call
	if err != nil {
		t.Fatalf("GET OCI endpoint %s: %v", url, err)
	}

	defer resp.Body.Close()

	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode == 0 {
		t.Fatalf("OCI endpoint returned no HTTP status")
	}
}

// TestKubernetesEndpointReachable boots with the shared Kubernetes data-plane
// enabled (--k8s-port set) and asserts its HTTPS endpoint answers — proving
// --k8s-port threads through and serverkit binds the k8s listener with its
// self-signed serving cert. The cert is self-signed, so the client skips
// verification; any HTTP status proves the TLS listener is up.
func TestKubernetesEndpointReachable(t *testing.T) {
	cfg := testConfig(t, allEnginesOff())
	cfg.providers = []string{providerAWS}
	cfg.k8sPort = freePortStr(t)

	_, stop := startAWS(t, cfg, mustOptions(t, &cfg))
	defer stop()

	waitListening(t, cfg.host, cfg.k8sPort)

	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // self-signed emulator cert
		},
	}

	url := "https://" + net.JoinHostPort(cfg.host, cfg.k8sPort) + "/"

	resp, err := client.Get(url) //nolint:noctx // short-lived in-process test call
	if err != nil {
		t.Fatalf("GET Kubernetes endpoint %s: %v", url, err)
	}

	defer resp.Body.Close()

	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode == 0 {
		t.Fatalf("Kubernetes endpoint returned no HTTP status")
	}
}
