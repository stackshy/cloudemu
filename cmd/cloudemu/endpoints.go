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
	OCI        string `json:"oci,omitempty"`
	Kubernetes string `json:"kubernetes,omitempty"`
}

func (e *endpointSet) writeFile(path string) error {
	b, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// banner lists the endpoints in display order, so adding a provider is one
// row rather than another branch in printBanner.
func (e *endpointSet) banner() []struct{ label, url, note string } {
	return []struct{ label, url, note string }{
		{"AWS", e.AWS, ""},
		{"Azure", e.Azure, "   (self-signed TLS)"},
		{"GCP", e.GCP, ""},
		{"OCI", e.OCI, ""},
		{"Kubernetes", e.Kubernetes, ""},
	}
}

// sdkHints pairs each endpoint with the snippet that points its SDK at it.
func (e *endpointSet) sdkHints() []struct{ label, url, hint string } {
	return []struct{ label, url, hint string }{
		{"AWS", e.AWS, "export AWS_ENDPOINT_URL=%s   (or o.BaseEndpoint in aws-sdk-go-v2)"},
		{"GCP", e.GCP, "option.WithEndpoint(%q) + option.WithoutAuthentication()"},
		{"OCI", e.OCI, "common.NewRawConfigurationProvider + client.Host = %q"},
		{"Azure", e.Azure, "cloud.Configuration ResourceManager endpoint = %q (trust the cert or skip verify)"},
	}
}

// printBanner writes the startup summary: the live endpoints and copy-paste
// snippets for pointing each SDK at them.
func printBanner(w io.Writer, e *endpointSet, adminOn bool) {
	fmt.Fprintln(w, "cloudemu — standalone server")
	fmt.Fprintln(w, "────────────────────────────")

	for _, row := range e.banner() {
		if row.url != "" {
			fmt.Fprintf(w, "  %-11s %s%s\n", row.label, row.url, row.note)
		}
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, "Point your SDKs at these endpoints:")

	for _, row := range e.sdkHints() {
		if row.url != "" {
			fmt.Fprintf(w, "  %-5s "+row.hint+"\n", row.label, row.url)
		}
	}

	if adminOn {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Reset state between tests: POST /_cloudemu/reset")
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, "Press Ctrl-C to stop.")
}
