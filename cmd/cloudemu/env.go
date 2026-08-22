package main

import (
	"flag"
	"fmt"
	"net"
	"os"
)

// runEnv prints shell `export` lines that point real SDKs/CLIs at a running
// cloudemu, so `eval "$(cloudemu env)"` wires the AWS CLI/SDK with zero code
// change (the GCP/Azure on-ramps are printed as comments — they have no single
// endpoint env var). Ports/host mirror `cloudemu serve` defaults.
func runEnv(args []string) error {
	fs := flag.NewFlagSet("env", flag.ContinueOnError)

	var (
		host    = fs.String("host", "127.0.0.1", "host the running emulator is reachable at")
		awsPort = fs.String("aws-port", "4566", "AWS endpoint port")
		azrPort = fs.String("azure-port", "4568", "Azure endpoint port")
		gcpPort = fs.String("gcp-port", "4569", "GCP endpoint port")
		region  = fs.String("region", "us-east-1", "region to export for the AWS SDK/CLI")
	)

	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: cloudemu env [flags]\n\n"+
			"Print shell exports that point SDKs/CLIs at a running cloudemu.\n"+
			"  eval \"$(cloudemu env)\"\n\nFlags:\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return err
	}

	aws := "http://" + net.JoinHostPort(*host, *awsPort)
	gcp := "http://" + net.JoinHostPort(*host, *gcpPort)
	azure := "https://" + net.JoinHostPort(*host, *azrPort)

	w := os.Stdout
	fmt.Fprintf(w, "export AWS_ENDPOINT_URL=%s\n", aws)
	fmt.Fprintf(w, "export AWS_ACCESS_KEY_ID=test\n")
	fmt.Fprintf(w, "export AWS_SECRET_ACCESS_KEY=test\n")
	fmt.Fprintf(w, "export AWS_DEFAULT_REGION=%s\n", *region)
	fmt.Fprintf(w, "export AWS_REGION=%s\n", *region)
	fmt.Fprintf(w, "# GCP:   option.WithEndpoint(%q) + option.WithoutAuthentication()\n", gcp)
	fmt.Fprintf(w, "# Azure: ResourceManager endpoint = %q (self-signed TLS — trust the cert or skip verify)\n", azure)

	return nil
}
