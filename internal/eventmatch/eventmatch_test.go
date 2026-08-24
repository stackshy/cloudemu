package eventmatch_test

import (
	"testing"

	"github.com/stackshy/cloudemu/v2/internal/eventmatch"
)

func mustPattern(t *testing.T, raw string) map[string]any {
	t.Helper()

	p, ok := eventmatch.ParsePattern(raw)
	if !ok {
		t.Fatalf("ParsePattern(%q) failed", raw)
	}

	return p
}

func TestMatchEventNestedDetail(t *testing.T) {
	pattern := mustPattern(t, `{"source":["my.app"],"detail":{"state":["running"]}}`)

	cases := []struct {
		name  string
		event map[string]any
		want  bool
	}{
		{
			name:  "matches nested detail",
			event: map[string]any{"source": "my.app", "detail": map[string]any{"state": "running"}},
			want:  true,
		},
		{
			name:  "nested detail value differs",
			event: map[string]any{"source": "my.app", "detail": map[string]any{"state": "stopped"}},
			want:  false,
		},
		{
			name:  "nested detail field missing",
			event: map[string]any{"source": "my.app", "detail": map[string]any{"other": "x"}},
			want:  false,
		},
		{
			name:  "source differs",
			event: map[string]any{"source": "other.app", "detail": map[string]any{"state": "running"}},
			want:  false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := eventmatch.MatchEvent(pattern, tc.event); got != tc.want {
				t.Fatalf("MatchEvent = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMatchEventContentOperators(t *testing.T) {
	cases := []struct {
		name    string
		pattern string
		value   any
		present bool
		want    bool
	}{
		{"prefix hit", `{"source":[{"prefix":"aws."}]}`, "aws.ec2", true, true},
		{"prefix miss", `{"source":[{"prefix":"aws."}]}`, "gcp.gce", true, false},
		{"suffix hit", `{"source":[{"suffix":".png"}]}`, "photo.png", true, true},
		{"anything-but hit", `{"source":[{"anything-but":"init"}]}`, "running", true, true},
		{"anything-but miss", `{"source":[{"anything-but":"init"}]}`, "init", true, false},
		{"anything-but list", `{"source":[{"anything-but":["a","b"]}]}`, "c", true, true},
		{"exists true present", `{"source":[{"exists":true}]}`, "x", true, true},
		{"exists true absent", `{"source":[{"exists":true}]}`, nil, false, false},
		{"exists false absent", `{"source":[{"exists":false}]}`, nil, false, true},
		{"numeric in range", `{"source":[{"numeric":[">",0,"<=",5]}]}`, float64(3), true, true},
		{"numeric out of range", `{"source":[{"numeric":[">",0,"<=",5]}]}`, float64(9), true, false},
		{"cidr hit", `{"source":[{"cidr":"10.0.0.0/24"}]}`, "10.0.0.5", true, true},
		{"cidr miss", `{"source":[{"cidr":"10.0.0.0/24"}]}`, "10.0.1.5", true, false},
		{"equals-ignore-case hit", `{"source":[{"equals-ignore-case":"AWS.ec2"}]}`, "aws.EC2", true, true},
		{"wildcard hit", `{"source":[{"wildcard":"aws.*.event"}]}`, "aws.ec2.event", true, true},
		{"wildcard miss", `{"source":[{"wildcard":"aws.*.event"}]}`, "aws.ec2.notice", true, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := mustPattern(t, tc.pattern)
			allowed, _ := p["source"].([]any)

			if got := eventmatch.MatchLeaf(allowed, tc.value, tc.present); got != tc.want {
				t.Fatalf("MatchLeaf = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMatchEventArrayValueIntersection(t *testing.T) {
	pattern := mustPattern(t, `{"resources":["arn:2"]}`)
	event := map[string]any{"resources": []any{"arn:1", "arn:2"}}

	if !eventmatch.MatchEvent(pattern, event) {
		t.Fatal("expected array intersection to match")
	}
}

func TestParsePatternRejectsGarbage(t *testing.T) {
	if _, ok := eventmatch.ParsePattern("not json"); ok {
		t.Fatal("expected garbage pattern to fail parse")
	}

	if _, ok := eventmatch.ParsePattern(""); ok {
		t.Fatal("expected empty pattern to fail parse")
	}
}
