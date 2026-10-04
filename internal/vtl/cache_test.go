package vtl

import (
	"context"
	"sync"
	"testing"
)

func TestCache(t *testing.T) {
	c := NewCache(2)

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
