package gcpfilter

import (
	"errors"
	"strings"
	"testing"
)

type vm struct {
	Name        string            `json:"name"`
	Status      string            `json:"status"`
	Zone        string            `json:"zone"`
	CPUs        int               `json:"cpus"`
	Preemptible bool              `json:"preemptible"`
	Labels      map[string]string `json:"labels,omitempty"`
	Tags        []string          `json:"tags,omitempty"`
}

func TestCompileAndMatch(t *testing.T) {
	item := vm{
		Name: "web-1", Status: "RUNNING", CPUs: 4,
		Zone:   "https://compute.googleapis.com/compute/v1/projects/p/zones/us-central1-a",
		Labels: map[string]string{"env": "prod"}, Tags: []string{"http", "ssh"},
	}

	tests := []struct {
		expr string
		want bool
	}{
		{"", true},
		{"name = web-1", true},
		{`name = "web-2"`, false},
		{"name != web-2", true},
		{"labels.env = prod", true},
		{"labels.env != prod", false},
		{"labels.team = x", false},
		{"labels.team != x", true},
		{"labels.env:*", true},
		{"labels.team:*", false},
		{"labels:env", true},
		{"name:web", true},
		{"tags:ssh", true},
		{"(status = RUNNING) (labels.env = prod)", true},
		{"(status = RUNNING) AND (labels.env = dev)", false},
		{"(status = STOPPED) OR (labels.env = prod)", true},
		{"(name = a) OR (name = b) AND (status = RUNNING)", false},
		{"(name = web-1) OR (name = b) AND (status = RUNNING)", true},
		{`name eq "web-.*"`, true},
		{"name eq web", false},
		{"name ne .*-1", false},
		{"(name eq web-.*) (status eq RUN.*)", true},
		{"name = web-*", true},
		{"cpus > 2", true},
		{"cpus <= 2", false},
		{"cpus = 4", true},
		{"preemptible = false", true},
		{"zone = us-central1-a", true},
		{"zone eq .*us-central1-a", true},
		{"((((name = web-1))))", true},
		{"-name = web-1", false},
		{"-name = web-2", true},
		{"(status = RUNNING) -labels.env = dev", true},
		// Unset fields, whether optional or not modelled by the emulator.
		{"unknownField = x", false},
		{"unknownField != x", true},
		{"unknownField:*", false},
		{"-unknownField = x", true},
		{"unknownField eq x", false},
		{"unknownField ne x", true},
		{"unknownField > 1", false},
		{"architecture = X86_64", false},
		{"description:foo", false},
		{"properties.machineType = e2-small", false},
		{"labels.team:*", false},
	}

	for _, tc := range tests {
		f, err := Compile(tc.expr)
		if err != nil {
			t.Errorf("Compile(%q): %v", tc.expr, err)
			continue
		}

		if got := f.Match(item); got != tc.want {
			t.Errorf("%q matched %v, want %v", tc.expr, got, tc.want)
		}
	}
}

func TestCompileInvalid(t *testing.T) {
	for _, expr := range []string{
		"name",
		"name =",
		"= x",
		"(name = x",
		"name = x)",
		`name = "x`,
		"name ~ x",
		"name eq (",
		"(name eq x) (status = y)",
		"name = x AND",
		strings.Repeat("(", 40) + "name = x" + strings.Repeat(")", 40),
		"name = " + strings.Repeat("x", 3000),
		"NOT name = x",
		"--name = x",
	} {
		if _, err := Compile(expr); !errors.Is(err, ErrInvalid) {
			t.Errorf("Compile(%q) err = %v, want ErrInvalid", expr, err)
		}
	}
}
