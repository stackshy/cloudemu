package jsonpath

import "testing"

type obj map[string]any

func (o obj) Lookup(k string) (any, bool) {
	v, ok := o[k]

	return v, ok
}

type arr []any

func (a arr) Index(i int) (any, bool) {
	if i < 0 || i >= len(a) {
		return nil, false
	}

	return a[i], true
}

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
