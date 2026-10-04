package vtl

import (
	"context"
	"runtime"
	"testing"
	"time"
)

// heapGuard is the most live heap one fuzz iteration may leave behind.
const heapGuard = 1 << 30

// FuzzParseRender feeds arbitrary templates through the parser and the
// evaluator. Neither may panic, overflow the stack, run past its deadline by
// much, produce more than MaxOutputBytes, or hold on to a runaway heap.
func FuzzParseRender(f *testing.F) {
	for _, seed := range []string{
		`#set($l = [])#set($x = $l.add($l))$l`,
		`#set($m = {})#set($x = $m.put("a", $m))$m $m.equals($m)`,
		`#set($s = "ab")#foreach($i in [1..1000])#set($s = "$s$s")#end$s`,
		`#set($l = [1])#foreach($i in [1..1000])#set($x = $l.addAll($l))#end`,
		`#foreach($i in [1..9])#if($i % 2 == 0)$i#elseif($i > 5)x#{else}-#end#end`,
		`$input.body $util.parseJson('{"a":[1,2]}').a[1] ${x.y} $!z #* c *# ## c`,
		`#set($s = "a")$s.replaceAll("(a)", "$1$1").split("a") $s.matches("[a-")`,
		`#if((((1 + 2) * 3) > 4 && !false || $nope))ok#end#stop after`,
		`#set($a = [])#foreach($i in [1..1000])#set($a = [$a])#end$a`,
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, src string) {
		tmpl, err := Parse(src)
		if err != nil {
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()

		start := time.Now()
		res, err := tmpl.Render(ctx, map[string]any{"x": NewMap(), "util": fuzzHost{}}, RenderOptions{})

		if d := time.Since(start); d > 5*time.Second {
			t.Fatalf("render took %v", d)
		}

		if err == nil && len(res.Output) > MaxOutputBytes {
			t.Fatalf("output %d bytes", len(res.Output))
		}

		var ms runtime.MemStats

		runtime.ReadMemStats(&ms)

		if ms.HeapAlloc > heapGuard {
			runtime.GC()
			runtime.ReadMemStats(&ms)

			if ms.HeapAlloc > heapGuard {
				t.Fatalf("live heap %d bytes after render", ms.HeapAlloc)
			}
		}
	})
}

// fuzzHost is a host object with a JSON parser, like $util.
type fuzzHost struct{}

func (fuzzHost) Get(string) (any, bool) { return nil, false }

func (fuzzHost) Call(name string, args []any) (any, bool, error) {
	if name != "parseJson" || len(args) != 1 {
		return nil, false, nil
	}

	v, err := ParseJSON(Stringify(args[0]))

	return v, true, err
}
