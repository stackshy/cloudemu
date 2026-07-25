package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// endpointSet is the machine-readable bundle of URLs a client points at. Empty
// fields (a provider that wasn't started) are omitted from the JSON.
type endpointSet struct {
	AWS        string `json:"aws,omitempty"`
	Azure      string `json:"azure,omitempty"`
	GCP        string `json:"gcp,omitempty"`
	Kubernetes string `json:"kubernetes,omitempty"`
}

func (e endpointSet) writeFile(path string) error {
	b, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// printBanner writes the startup summary: the live endpoints and copy-paste
// snippets for pointing each SDK at them.
func printBanner(w io.Writer, e endpointSet, adminOn bool) {
	fmt.Fprintln(w, "cloudemu — standalone server")
	fmt.Fprintln(w, "────────────────────────────")
	if e.AWS != "" {
		fmt.Fprintf(w, "  AWS         %s\n", e.AWS)
	}
	if e.Azure != "" {
		fmt.Fprintf(w, "  Azure       %s   (self-signed TLS)\n", e.Azure)
	}
	if e.GCP != "" {
		fmt.Fprintf(w, "  GCP         %s\n", e.GCP)
	}
	if e.Kubernetes != "" {
		fmt.Fprintf(w, "  Kubernetes  %s\n", e.Kubernetes)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Point your SDKs at these endpoints:")
	if e.AWS != "" {
		fmt.Fprintf(w, "  AWS   export AWS_ENDPOINT_URL=%s   (or o.BaseEndpoint in aws-sdk-go-v2)\n", e.AWS)
	}
	if e.GCP != "" {
		fmt.Fprintf(w, "  GCP   option.WithEndpoint(%q) + option.WithoutAuthentication()\n", e.GCP)
	}
	if e.Azure != "" {
		fmt.Fprintf(w, "  Azure cloud.Configuration ResourceManager endpoint = %q (trust the cert or skip verify)\n", e.Azure)
	}
	if adminOn {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Reset state between tests: POST /_cloudemu/reset")
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Press Ctrl-C to stop.")
}
