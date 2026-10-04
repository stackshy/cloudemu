package vtl

import (
	"context"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestCache(t *testing.T) {
	// Room for two small templates.
	c := NewCache(2*entryOverhead + 200)

	a1, err := c.Parse("a$x")
	if err != nil {
		t.Fatal(err)
	}

	if a2, _ := c.Parse("a$x"); a2 != a1 {
		t.Fatal("cache miss on repeat")
	}

	if _, err := c.Parse("#if("); err == nil {
		t.Fatal("parse error not returned")
	}

	if _, err := c.Parse("#if("); err == nil {
		t.Fatal("cached parse error not returned")
	}

	// "a$x" was used before "#if(", so adding a third entry evicts it.
	if _, err := c.Parse("b"); err != nil || c.Len() != 2 {
		t.Fatalf("len = %d err = %v", c.Len(), err)
	}

	if a3, _ := c.Parse("a$x"); a3 == a1 {
		t.Fatal("evicted entry still cached")
	}

	// A cached template renders concurrently.
	var wg sync.WaitGroup

	for i := range 8 {
		wg.Add(1)

		go func() {
			defer wg.Done()

			res, err := a1.Render(context.Background(), map[string]any{"x": int64(i)}, RenderOptions{})
			if err != nil || res.Output != "a"+Stringify(int64(i)) {
				t.Errorf("render %d = %v %v", i, res, err)
			}
		}()
	}

	wg.Wait()
}

// TestCacheWeightCoversParsedSize checks the per-byte weight against what a
// parsed template really holds, and that the byte cap bounds the cache.
func TestCacheWeightCoversParsedSize(t *testing.T) {
	var b strings.Builder
	for b.Len() < MaxTemplateBytes-200 {
		b.WriteString(`#if($a.b("x", 1) == [1, 2])$c.d#{else}text $e#end`)
	}

	src := b.String()

	var before, after runtime.MemStats

	runtime.GC()
	runtime.ReadMemStats(&before)

	tmpl, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}

	runtime.GC()
	runtime.ReadMemStats(&after)
	runtime.KeepAlive(tmpl)

	held := int(after.HeapAlloc) - int(before.HeapAlloc)
	if held > weight(src) {
		t.Fatalf("parsed template holds %d bytes, weight is only %d", held, weight(src))
	}

	c := NewCache(4 * weight(src))
	for i := range 10 {
		if _, err := c.Parse(src + strings.Repeat(" ", i)); err != nil {
			t.Fatal(err)
		}
	}

	if c.Len() < 3 || c.Bytes() > 4*weight(src) {
		t.Fatalf("cache holds %d entries, %d bytes", c.Len(), c.Bytes())
	}
}
