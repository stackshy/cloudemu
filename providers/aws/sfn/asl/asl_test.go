package asl

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestGlobMatch(t *testing.T) {
	cases := []struct {
		pattern, s string
		want       bool
	}{
		{"foo*bar", "foobar", true},
		{"foo*bar", "fooXXbar", true},
		{"foo*bar", "foobaz", false},
		{"*", "anything", true},
		{"a*b*c", "axxbyyc", true},
		{"a*b*c", "axxc", false},
		{"literal", "literal", true},
		{"literal", "other", false},
		{`foo\*bar`, "foo*bar", true},
		{`foo\*bar`, "fooXbar", false},
	}

	for _, c := range cases {
		if got := globMatch(c.pattern, c.s); got != c.want {
			t.Errorf("globMatch(%q,%q) = %v, want %v", c.pattern, c.s, got, c.want)
		}
	}
}

func TestEvalPath(t *testing.T) {
	root := map[string]any{
		"a": map[string]any{"b": []any{float64(10), float64(20), float64(30)}},
		"s": "hi",
	}

	cases := []struct {
		path    string
		want    any
		present bool
	}{
		{"$", root, true},
		{"$.s", "hi", true},
		{"$.a.b[2]", float64(30), true},
		{"$['s']", "hi", true},
		{"$.missing", nil, false},
		{"$.a.b[9]", nil, false},
	}

	for _, c := range cases {
		got, present, err := evalPath(c.path, root)
		if err != nil {
			t.Fatalf("evalPath(%q) error: %v", c.path, err)
		}

		if present != c.present {
			t.Errorf("evalPath(%q) present = %v, want %v", c.path, present, c.present)
		}

		if present && !reflect.DeepEqual(got, c.want) {
			t.Errorf("evalPath(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestEvalPathUnsupportedFailsLoudly(t *testing.T) {
	for _, p := range []string{"$.a[*]", "$..b", "$.a[?(@.x)]"} {
		if _, _, err := evalPath(p, map[string]any{}); err == nil {
			t.Errorf("evalPath(%q) should reject unsupported syntax", p)
		}
	}
}

func TestIntrinsics(t *testing.T) {
	it := &interp{}
	it.buildContext(&RunInput{ExecName: "run"}, nil)

	input := map[string]any{"name": "Ada", "n": float64(2)}

	got, err := it.evalIntrinsic(`States.Format('Hi {}, item {}', $.name, $.n)`, input)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}

	if got != "Hi Ada, item 2" {
		t.Errorf("Format = %q", got)
	}

	arr, err := it.evalIntrinsic(`States.Array($.name, 1, 'x')`, input)
	if err != nil {
		t.Fatalf("Array: %v", err)
	}

	if !reflect.DeepEqual(arr, []any{"Ada", float64(1), "x"}) {
		t.Errorf("Array = %#v", arr)
	}

	item, err := it.evalIntrinsic(`States.ArrayGetItem(States.Array('a','b','c'), 1)`, input)
	if err != nil {
		t.Fatalf("ArrayGetItem: %v", err)
	}

	if item != "b" {
		t.Errorf("ArrayGetItem = %v", item)
	}

	uuid, err := it.evalIntrinsic(`States.UUID()`, input)
	if err != nil || len(uuid.(string)) != 36 {
		t.Errorf("UUID = %v (err=%v)", uuid, err)
	}
}

func TestPayloadTemplateWithContext(t *testing.T) {
	it := &interp{}
	it.buildContext(&RunInput{ExecName: "myrun"}, nil)

	tmpl := json.RawMessage(`{"lit":"x","from.$":"$.v","name.$":"$$.Execution.Name"}`)

	got, err := it.applyPayloadTemplate(tmpl, map[string]any{"v": float64(7)})
	if err != nil {
		t.Fatalf("applyPayloadTemplate: %v", err)
	}

	want := map[string]any{"lit": "x", "from": float64(7), "name": "myrun"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("payload = %#v, want %#v", got, want)
	}
}

func TestParseAcceptsAndRejects(t *testing.T) {
	good := `{"StartAt":"A","States":{"A":{"Type":"Pass","End":true}}}`
	if _, err := Parse(good); err != nil {
		t.Fatalf("Parse(valid) = %v", err)
	}

	bad := []string{
		`{"StartAt":"A","States":{"A":{"Type":"Wait","Seconds":1,"Result":{},"End":true}}}`,
		`{"StartAt":"Missing","States":{"A":{"Type":"Pass","End":true}}}`,
		`{"QueryLanguage":"JSONata","StartAt":"A","States":{"A":{"Type":"Pass","End":true}}}`,
		// Pass does not support ResultSelector (Task/Parallel/Map only).
		`{"StartAt":"A","States":{"A":{"Type":"Pass","ResultSelector":{"x.$":"$.y"},"End":true}}}`,
	}

	for _, def := range bad {
		if _, err := Parse(def); err == nil {
			t.Errorf("Parse(%q) should have failed", def)
		}
	}
}

func TestResultPathMergesOntoRaw(t *testing.T) {
	def, err := Parse(`{"StartAt":"T","States":{"T":{"Type":"Pass",
		"InputPath":"$.detail","Result":{"ok":true},"ResultPath":"$.result","End":true}}}`)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	res := Run(nil, def, &RunInput{
		Input: `{"detail":{"id":1},"meta":"keep"}`, StartTime: time.Unix(0, 0),
	})

	if res.Status != "SUCCEEDED" {
		t.Fatalf("status = %q (%s)", res.Status, res.Cause)
	}

	var out map[string]any
	if err := json.Unmarshal([]byte(res.Output), &out); err != nil {
		t.Fatalf("output not JSON: %v", err)
	}

	if out["meta"] != "keep" {
		t.Fatalf("sibling 'meta' lost: %v", out)
	}
}

// TestRunHonorsContextCancellation proves the ctx actually reaches the walk loop
// (if Run discarded it, a cancelled ctx would be unobserved and the run would
// SUCCEED) — the plumbing the PR2 Task->Lambda recursion guard relies on.
func TestRunHonorsContextCancellation(t *testing.T) {
	def, err := Parse(`{"StartAt":"A","States":{"A":{"Type":"Pass","End":true}}}`)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	res := Run(ctx, def, &RunInput{Input: `{}`, StartTime: time.Unix(0, 0)})
	if res.Status != "FAILED" || res.Error != "CloudEmu.ExecutionCanceled" {
		t.Fatalf("cancelled run: status=%q error=%q, want FAILED/CloudEmu.ExecutionCanceled", res.Status, res.Error)
	}
}
