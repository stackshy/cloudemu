package vtl

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

func mustJSON(t *testing.T, v any) string {
	t.Helper()

	s, err := ToJSON(v)
	if err != nil {
		t.Fatalf("ToJSON: %v", err)
	}

	return s
}

func renderErr(t *testing.T, src string, vars map[string]any) error {
	t.Helper()

	tmpl, err := Parse(src)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = tmpl.Render(ctx, vars, RenderOptions{})

	return err
}

func TestSelfReferencingValues(t *testing.T) {
	cases := []struct {
		name, src, want string
	}{
		{"list prints itself", `#set($l = [1])#set($x = $l.add($l))$l`, "[1, (this Collection)]"},
		{"map prints itself", `#set($m = {"a": 1})#set($x = $m.put("self", $m))$m`, "{a=1, self=(this Map)}"},
		{"indirect cycle", `#set($a = [])#set($b = [$a])#set($x = $a.add($b))$a`, "[[(this Collection)]]"},
		{"cyclic equality", `#set($l = [])#set($x = $l.add($l))#if($l == $l)same#end#if($l.contains($l))has#end`, "samehas"},
		{"cyclic concat", `#set($l = [])#set($x = $l.add($l))#set($s = "v=$l")$s`, "v=[(this Collection)]"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := render(t, c.src, nil); got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}

	l := NewList()
	l.Items = append(l.Items, l)

	if _, err := ToJSON(l); !errors.Is(err, ErrCyclicValue) {
		t.Fatalf("ToJSON(cyclic) err = %v", err)
	}

	if s := Stringify(l); s != "[(this Collection)]" {
		t.Fatalf("Stringify(cyclic) = %q", s)
	}
}

func TestDeepValuesAreBounded(t *testing.T) {
	// Each iteration wraps the list one level deeper.
	src := `#set($a = [])#foreach($i in [1..1000])#set($a = [$a])#end#foreach($i in [1..1000])#set($a = [$a])#end$a`
	if err := renderErr(t, src, nil); !errors.Is(err, ErrDepthLimit) {
		t.Fatalf("deep print err = %v", err)
	}

	deep := strings.Repeat("[", MaxValueDepth+1) + strings.Repeat("]", MaxValueDepth+1)
	if _, err := ParseJSON(deep); !errors.Is(err, ErrDepthLimit) {
		t.Fatalf("deep JSON err = %v", err)
	}

	ok := strings.Repeat("[", 50) + strings.Repeat("]", 50)
	if _, err := ParseJSON(ok); err != nil {
		t.Fatalf("50-deep JSON: %v", err)
	}
}

func TestGrowthIsBounded(t *testing.T) {
	cases := []struct {
		name, src string
		want      error
	}{
		{"string doubling", `#set($s = "ab")#foreach($i in [1..100])#set($s = "$s$s")#end`, ErrMemoryLimit},
		{"concat doubling", `#set($s = "ab")#foreach($i in [1..100])#set($s = $s + $s)#end`, ErrOutputLimit},
		{"list doubling", `#set($l = [1])#foreach($i in [1..100])#set($x = $l.addAll($l))#end`, ErrMemoryLimit},
		{"map growth", `#set($m = {})#foreach($i in [1..1000])#foreach($j in [1..1000])#set($m["$i-$j"] = $i)#end#end`, ErrStepBudget},
		{"replace blowup", `#set($s = "aaaaaaaaaa")#foreach($i in [1..40])#set($s = $s.replace("a", "aa"))#end`, ErrOutputLimit},
		{"literal replaceAll blowup", `#set($s = "aaaaaaaaaa")#foreach($i in [1..40])#set($s = $s.replaceAll("a", "aa"))#end`, ErrOutputLimit},
		{"regex blowup", `#set($s = "aaaaaaaaaa")#foreach($i in [1..40])#set($s = $s.replaceAll("[a]", "$0$0"))#end`, ErrOutputLimit},
		{"output flood", `#set($s = "0123456789")#foreach($i in [1..20])#set($s = "$s$s")#end#foreach($i in [1..1000])$s#end`, ErrOutputLimit},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			start := time.Now()

			err := renderErr(t, c.src, nil)
			if !errors.Is(err, c.want) && !isLimit(err) {
				t.Fatalf("err = %v, want a limit error", err)
			}

			// A render stops at its deadline (5s here) plus at most one slow
			// call; the bound is loose so a loaded -race run stays green.
			if d := time.Since(start); d > 20*time.Second {
				t.Fatalf("took %v", d)
			}
		})
	}
}

func TestParserLimits(t *testing.T) {
	if _, err := Parse(strings.Repeat("a", MaxTemplateBytes+1)); err == nil {
		t.Fatal("oversized template accepted")
	}

	for name, src := range map[string]string{
		"nested parens":  "#set($x = " + strings.Repeat("(", 200) + "1" + strings.Repeat(")", 200) + ")",
		"nested ifs":     strings.Repeat("#if(true)", 200) + strings.Repeat("#end", 200),
		"nested not":     "#set($x = " + strings.Repeat("!", 200) + "true)",
		"operator chain": "#set($x = 1" + strings.Repeat(" + 1", maxOperatorChain+1) + ")",
	} {
		if _, err := Parse(src); err == nil {
			t.Errorf("%s accepted", name)
		}
	}

	if got := render(t, "#set($x = 1"+strings.Repeat(" + 1", 500)+")$x", nil); got != "501" {
		t.Fatalf("500-operator chain = %s", got)
	}
}

// TestParseIsLinear guards against quadratic parsing: a template of many short
// directives and literal dollars parses in well under a second.
func TestParseIsLinear(t *testing.T) {
	var b strings.Builder

	for b.Len() < MaxTemplateBytes-64 {
		b.WriteString("  #set($a = 1)\n$ $ #* c *#\n")
	}

	start := time.Now()

	if _, err := Parse(b.String()); err != nil {
		t.Fatal(err)
	}

	if d := time.Since(start); d > time.Second {
		t.Fatalf("parse took %v", d)
	}
}

func isLimit(err error) bool {
	return errors.Is(err, ErrMemoryLimit) || errors.Is(err, ErrOutputLimit) || errors.Is(err, ErrStepBudget) ||
		errors.Is(err, context.DeadlineExceeded)
}

// TestStringMethodsAllocateWithinBudget runs splits and regex replacements over
// a 6 MB string with millions of matches. Each must stop at the memory budget
// and allocate no more than about twice the budget on the way.
func TestStringMethodsAllocateWithinBudget(t *testing.T) {
	body := strings.Repeat("&a", 3<<20)

	for name, src := range map[string]string{
		"literal split":  `#set($p = $b.split("&"))`,
		"regex split":    `#set($p = $b.split("[&]"))`,
		"regex replace":  `#set($p = $b.replaceAll("(&)", "$1"))`,
		"empty split":    `#set($p = $b.split(""))`,
		"anchored split": `#set($p = $b.split("\b"))`,
	} {
		t.Run(name, func(t *testing.T) {
			var before, after runtime.MemStats

			runtime.GC()
			runtime.ReadMemStats(&before)

			err := renderErr(t, src, map[string]any{"b": body})

			runtime.ReadMemStats(&after)

			if !isLimit(err) {
				t.Fatalf("err = %v, want a limit error", err)
			}

			if alloc := after.TotalAlloc - before.TotalAlloc; !raceEnabled && alloc > 2*MaxAllocBytes {
				t.Fatalf("allocated %d MB, budget is %d MB", alloc>>20, MaxAllocBytes>>20)
			}
		})
	}

	// A modest split still works and keeps Java's trailing-empty rule.
	if got := render(t, `$b.split("&").size()`, map[string]any{"b": "a&b&&"}); got != "2" {
		t.Fatalf("split size = %s", got)
	}

	if got := render(t, `$b.split("")`, map[string]any{"b": "abc"}); got != "[a, b, c]" {
		t.Fatalf("empty split = %s", got)
	}
}

// TestEachMatchAgreesWithFindAll checks the incremental matcher against Go's
// FindAll on patterns with empty and overlapping candidates.
func TestEachMatchAgreesWithFindAll(t *testing.T) {
	for _, c := range []struct{ pattern, s string }{
		{"a*", "baaacaa"}, {"", "héllo"}, {"x?", "axxbx"}, {"(a)(b)?", "abaab"},
		{"[&]", "&a&&b&"}, {"\\b", "ab cd"}, {"^a", "aaa"}, {"é|", "aéb"},
	} {
		re := regexp.MustCompile(c.pattern)
		want := fmt.Sprint(re.FindAllStringSubmatchIndex(c.s, -1))

		var got [][]int

		err := eachMatch(&budget{}, re, c.s, -1, func(loc []int) error {
			got = append(got, loc)

			return nil
		})
		if err != nil || fmt.Sprint(got) != want {
			t.Errorf("%q on %q: got %v, want %s", c.pattern, c.s, got, want)
		}
	}
}
