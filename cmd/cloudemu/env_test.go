package main

import (
	"strings"
	"testing"
)

// TestWriteEnvPrintsTheOCIOnRamp checks the OCI comment line and that it
// follows --oci-port and --host.
func TestWriteEnvPrintsTheOCIOnRamp(t *testing.T) {
	var sb strings.Builder

	if err := writeEnv(&sb, nil); err != nil {
		t.Fatalf("writeEnv: %v", err)
	}

	if !strings.Contains(sb.String(), `# OCI:   client.Host = "http://127.0.0.1:4571"`) {
		t.Fatalf("default env output missing the OCI line:\n%s", sb.String())
	}

	sb.Reset()

	if err := writeEnv(&sb, []string{"--host", "10.0.0.5", "--oci-port", "5571"}); err != nil {
		t.Fatalf("writeEnv with flags: %v", err)
	}

	if !strings.Contains(sb.String(), `client.Host = "http://10.0.0.5:5571"`) {
		t.Fatalf("env output ignores --host/--oci-port:\n%s", sb.String())
	}
}
