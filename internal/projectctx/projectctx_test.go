package projectctx

import (
	"context"
	"testing"
)

func TestWithProjectIgnoresWildcards(t *testing.T) {
	for _, p := range []string{"", "_", "-"} {
		ctx := WithProject(context.Background(), p)
		if _, ok := Lookup(ctx); ok {
			t.Errorf("WithProject(%q) stored a project", p)
		}

		if got := ProjectOr(ctx, "def"); got != "def" {
			t.Errorf("ProjectOr after WithProject(%q) = %q, want def", p, got)
		}
	}

	ctx := WithProject(context.Background(), "p-b")
	if got := ProjectOr(ctx, "def"); got != "p-b" {
		t.Errorf("ProjectOr = %q, want p-b", got)
	}
}

func TestAllProjects(t *testing.T) {
	ctx := WithProject(context.Background(), "p-a")
	if IsAllProjects(ctx) {
		t.Fatal("stamped ctx reported AllProjects")
	}

	if !IsAllProjects(AllProjects(ctx)) {
		t.Fatal("AllProjects not reported")
	}
}

func TestFromPath(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"/compute/v1/projects/p1/zones/z/instances/x", "p1"},
		{"projects/p2/topics/t", "p2"},
		{"https://pubsub.googleapis.com/v1/projects/p3/topics/t", "p3"},
		{"//storage.googleapis.com/projects/p4/buckets/b", "p4"},
		{"/storage/v1/b/bucket", ""},
		{"projects/", ""},
	}

	for _, tc := range tests {
		if got := FromPath(tc.in); got != tc.want {
			t.Errorf("FromPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestKeySplit(t *testing.T) {
	p, l, ok := Split(Key("p-a", "sec1"))
	if !ok || p != "p-a" || l != "sec1" {
		t.Fatalf("Split(Key) = %q %q %v", p, l, ok)
	}

	if _, l, ok := Split("legacy"); ok || l != "legacy" {
		t.Fatalf("Split(legacy) = %q %v", l, ok)
	}
}
