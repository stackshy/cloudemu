package jsonpath

import (
	"context"
	"fmt"
	"sort"
	"testing"
)

type obj map[string]any

func (o obj) Lookup(k string) (any, bool) {
	v, ok := o[k]

	return v, ok
}

func (o obj) Keys() []string {
	keys := make([]string, 0, len(o))
	for k := range o {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	return keys
}

type arr []any

func (a arr) Index(i int) (any, bool) {
	if i < 0 || i >= len(a) {
		return nil, false
	}

	return a[i], true
}

func (a arr) Len() int { return len(a) }

func TestEval(t *testing.T) {
	root := map[string]any{"a": map[string]any{"b": []any{1, 2}}, "o": obj{"x": arr{"y"}}}

	cases := []struct {
		path    string
		want    any
		present bool
	}{
		{"$", nil, true},
		{"$.a.b[1]", 2, true},
		{"$['a'].b[0]", 1, true},
		{"$.o.x[0]", "y", true},
		{"$.o.x[5]", nil, false},
		{"$.a.b[9]", nil, false},
		{"$.missing", nil, false},
		{"$.a.b.c", nil, false},
	}

	for _, c := range cases {
		got, ok, err := Eval(c.path, root)
		if c.path == "$" {
			if err != nil || !ok {
				t.Errorf("Eval($) = %v %v", ok, err)
			}

			continue
		}

		if err != nil || ok != c.present || got != c.want {
			t.Errorf("Eval(%q) = %v %v %v", c.path, got, ok, err)
		}
	}

	for _, bad := range []string{"a", "$.a[*]", "$..a", "$.", "$[x]", "$[0", "$x"} {
		if _, _, err := Eval(bad, root); err == nil {
			t.Errorf("Eval(%q) accepted", bad)
		}
	}
}

func TestEvalAll(t *testing.T) {
	root := map[string]any{
		"items": []any{map[string]any{"id": 1}, map[string]any{"id": 2, "sub": map[string]any{"id": 3}}},
		"o":     obj{"b": arr{"x"}, "a": "y"},
	}

	cases := []struct {
		path       string
		want       string
		indefinite bool
	}{
		{"$.items[*].id", "[1 2]", true},
		{"$.items.*.id", "[1 2]", true},
		{"$..id", "[1 2 3]", true},
		{"$.o.*", "[y [x]]", true},
		{"$..[0]", "[map[id:1] x]", true},
		{"$.items[1].sub.id", "[3]", false},
		{"$.missing", "[]", false},
		{"$..nothing", "[]", true},
	}

	for _, c := range cases {
		got, indefinite, err := EvalAll(context.Background(), c.path, root)
		if err != nil || fmt.Sprint(got) != c.want && !(len(got) == 0 && c.want == "[]") || indefinite != c.indefinite {
			t.Errorf("EvalAll(%q) = %v %v %v, want %s %v", c.path, got, indefinite, err, c.want, c.indefinite)
		}
	}

	for _, bad := range []string{"x", "$[?(@.a)]", "$..", "$.a[", "$[x]"} {
		if _, _, err := EvalAll(context.Background(), bad, root); err == nil {
			t.Errorf("EvalAll(%q) accepted", bad)
		}
	}

	// Descent stops at maxDepth instead of exhausting the stack.
	var deep any = "leaf"
	for range maxDepth + 50 {
		deep = []any{deep}
	}

	if _, _, err := EvalAll(context.Background(), "$..*", deep); err != nil {
		t.Fatalf("deep descent: %v", err)
	}
}

func TestEvalAllCapsMatches(t *testing.T) {
	// A 900-deep chain: each descent step multiplies the matches.
	var items any = 1
	for range 900 {
		items = []any{items, 2}
	}

	if _, _, err := EvalAll(context.Background(), "$..*..*..*", items); err == nil {
		t.Fatal("multiplying path not capped")
	}
}

func TestEvalAllStopsAtDeadline(t *testing.T) {
	// A wide, deep document: descent over it is slow enough to pass a
	// cancelled context's first check.
	var doc any = 1
	for range 500 {
		doc = []any{doc, 1, 2, 3}
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, _, err := EvalAll(ctx, "$..*..zz", doc); err == nil {
		t.Fatal("cancelled walk did not stop")
	}
}
