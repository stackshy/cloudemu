package main

import (
	"errors"
	"net"
	"strings"
	"testing"
)

// TestCheckPortFree probes a port the test itself binds (expect in use) and an
// unused port (expect free), exercising the real listen-then-close helper.
func TestCheckPortFree(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("open listener: %v", err)
	}
	defer ln.Close()

	busy := ln.Addr().String()
	if checkPortFree(busy) {
		t.Fatalf("checkPortFree(%q) = true, want false (a listener is bound there)", busy)
	}

	// Bind then close to obtain an address that is momentarily free.
	free, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("open second listener: %v", err)
	}

	freeAddr := free.Addr().String()
	_ = free.Close()

	if !checkPortFree(freeAddr) {
		t.Fatalf("checkPortFree(%q) = false, want true (nothing is bound there)", freeAddr)
	}
}

// TestDockerLine confirms the injected lookPath drives the found/not-found line
// and its pass/warn marker, so the check is testable without a real docker.
func TestDockerLine(t *testing.T) {
	found := func(string) (string, error) { return "/usr/bin/docker", nil }
	line := dockerLine(found)

	if !strings.Contains(line, markPass) || !strings.Contains(line, "/usr/bin/docker") {
		t.Fatalf("found docker line = %q, want pass marker + path", line)
	}

	missing := func(string) (string, error) { return "", errors.New("not found") }
	line = dockerLine(missing)

	if !strings.Contains(line, markWarn) || strings.Contains(line, markFail) {
		t.Fatalf("missing docker line = %q, want warn marker (not fail)", line)
	}
}

// TestWriteDoctorReportBlockingPort verifies a required port in use is a blocker
// (fail marker, non-zero return) while an opt-in OCI port in use is only a warn.
func TestWriteDoctorReportBlockingPort(t *testing.T) {
	ports := []portCheck{
		{"AWS", "4566", true},
		{"OCI (opt-in)", "4571", false},
	}

	// AWS busy (required → blocker), OCI busy (opt-in → warn only).
	portFree := func(hostPort string) bool { return false }
	found := func(string) (string, error) { return "/usr/bin/docker", nil }

	var sb strings.Builder

	blockers := writeDoctorReport(&sb, "127.0.0.1", ports, portFree, found)
	out := sb.String()

	if blockers != 1 {
		t.Fatalf("blockers = %d, want 1 (only the required AWS port)", blockers)
	}

	if !strings.Contains(out, markFail) {
		t.Fatalf("report missing fail marker for the in-use required port:\n%s", out)
	}

	if !strings.Contains(out, markWarn) {
		t.Fatalf("report missing warn marker for the in-use opt-in OCI port:\n%s", out)
	}

	if !strings.Contains(out, "required port(s) in use") {
		t.Fatalf("report missing the blocker summary line:\n%s", out)
	}
}

// TestWriteDoctorReportAllClear verifies the all-pass path: zero blockers, a pass
// summary, and the exact :engines pointer string.
func TestWriteDoctorReportAllClear(t *testing.T) {
	ports := []portCheck{
		{"AWS", "4566", true},
		{"Kubernetes", "4570", true},
	}

	portFree := func(string) bool { return true }
	missing := func(string) (string, error) { return "", errors.New("not found") }

	var sb strings.Builder

	blockers := writeDoctorReport(&sb, "127.0.0.1", ports, portFree, missing)
	out := sb.String()

	if blockers != 0 {
		t.Fatalf("blockers = %d, want 0 (all ports free)", blockers)
	}

	if !strings.Contains(out, "all preflight checks passed") {
		t.Fatalf("report missing the all-clear summary:\n%s", out)
	}

	if !strings.Contains(out, markPass) {
		t.Fatalf("report missing any pass marker:\n%s", out)
	}

	if !strings.Contains(out, enginesImagePointer) {
		t.Fatalf("report missing the exact :engines pointer %q:\n%s", enginesImagePointer, out)
	}

	if !strings.Contains(out, "free (at check time)") {
		t.Fatalf("report should phrase free ports as a point-in-time check:\n%s", out)
	}
}

// TestDefaultPortChecks confirms the doctor reads the real serve defaults, so it
// can never drift from what `cloudemu serve` binds.
func TestDefaultPortChecks(t *testing.T) {
	want := map[string]struct {
		port     string
		required bool
	}{
		"AWS":          {"4566", true},
		"Azure":        {"4568", true},
		"GCP":          {"4569", true},
		"Kubernetes":   {"4570", true},
		"OCI (opt-in)": {"4571", false},
	}

	got := defaultPortChecks()
	if len(got) != len(want) {
		t.Fatalf("defaultPortChecks() returned %d entries, want %d", len(got), len(want))
	}

	for _, p := range got {
		exp, ok := want[p.label]
		if !ok {
			t.Fatalf("unexpected port label %q", p.label)
		}

		if p.port != exp.port || p.required != exp.required {
			t.Fatalf("port %q = {%s, required=%v}, want {%s, required=%v}",
				p.label, p.port, p.required, exp.port, exp.required)
		}
	}
}
