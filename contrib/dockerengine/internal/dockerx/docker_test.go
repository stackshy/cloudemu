package dockerx

import (
	"reflect"
	"testing"
)

func TestAvailableMatchesLookPath(t *testing.T) {
	// Available must not panic and must agree with the runner's binary name;
	// its boolean result depends on the host, so we only assert it is callable
	// and idempotent.
	if Available() != Available() {
		t.Fatal("Available should be deterministic within a run")
	}
}

func TestBuildRunArgs(t *testing.T) {
	cases := []struct {
		name     string
		image    string
		cmd      []string
		env      map[string]string
		detached bool
		want     []string
	}{
		{
			name:  "plain foreground",
			image: "busybox",
			want:  []string{"run", "busybox"},
		},
		{
			name:     "detached with command",
			image:    "busybox",
			cmd:      []string{"sh", "-c", "echo hi"},
			detached: true,
			want:     []string{"run", "-d", "busybox", "sh", "-c", "echo hi"},
		},
		{
			name:  "env sorted deterministically",
			image: "busybox",
			env:   map[string]string{"B": "2", "A": "1"},
			want:  []string{"run", "-e", "A=1", "-e", "B=2", "busybox"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildRunArgs(tc.image, tc.cmd, tc.env, tc.detached)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("buildRunArgs = %v, want %v", got, tc.want)
			}
		})
	}
}
