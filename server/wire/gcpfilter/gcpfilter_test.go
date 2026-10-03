package gcpfilter

import (
	"errors"
	"reflect"
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
		{"unknownField = x", false},
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
		"-name = x",
		"NOT name = x",
	} {
		if _, err := Compile(expr); !errors.Is(err, ErrInvalid) {
			t.Errorf("Compile(%q) err = %v, want ErrInvalid", expr, err)
		}
	}
}

type wrapped struct {
	vm
	Disks []struct {
		Source string `json:"source"`
	} `json:"disks"`
	Extra  any    `json:"extra,omitempty"`
	hidden string //nolint:unused // proves unexported fields are not filterable
}

func TestValidate(t *testing.T) {
	typ := reflect.TypeFor[wrapped]()

	for _, tc := range []struct {
		expr string
		ok   bool
	}{
		{"name = x", true},
		{"labels.anything = x", true},
		{"labels.env != prod", true},
		{"disks.source = x", true},
		{"extra.deep.field = x", true},
		{"(status = RUNNING) (cpus > 1)", true},
		{"unknownField = x", false},
		{"unknownField != x", false},
		{"name.sub = x", false},
		{"disks.nope = x", false},
		{"hidden = x", false},
	} {
		f, err := Compile(tc.expr)
		if err != nil {
			t.Fatalf("Compile(%q): %v", tc.expr, err)
		}

		if err := f.Validate(typ); (err == nil) != tc.ok {
			t.Errorf("Validate(%q) err = %v, want ok=%v", tc.expr, err, tc.ok)
		}
	}
}
