package vtl

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func render(t *testing.T, src string, vars map[string]any) string {
	t.Helper()

	tmpl, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse(%q): %v", src, err)
	}

	res, err := tmpl.Render(context.Background(), vars, RenderOptions{})
	if err != nil {
		t.Fatalf("Render(%q): %v", src, err)
	}

	return res.Output
}

func TestRenderDirectivesAndExpressions(t *testing.T) {
	body, _ := ParseJSON(`{"name":"pet","tags":["a","b"],"n":3,"nested":{"x":1.5}}`)

	cases := []struct {
		name, src, want string
	}{
		{"text", "hello", "hello"},
		{"ref", "$name", "pet"},
		{"braced", "${name}s", "pets"},
		{"quiet missing", "[$!missing]", "[]"},
		{"missing renders empty", "[$missing]", "[]"},
		{"dollar not ref", "cost $5", "cost $5"},
		{"escape", `\$name`, "$name"},
		{"property", "$body.nested.x", "1.5"},
		{"index", "$body.tags[1]", "b"},
		{"set and math", "#set($a = 2 + 3 * 4)$a", "14"},
		{"int division", "#set($a = 7 / 2)$a", "3"},
		{"float", "#set($a = 1.5 * 2)$a", "3.0"},
		{"concat", `#set($s = "x" + 1)$s`, "x1"},
		{"interpolated string", `#set($s = "hi $name")$s`, "hi pet"},
		{"single quoted literal", `#set($s = 'hi $name')$s`, "hi $name"},
		{"if else", "#if($body.n > 2)big#else small#end", "big"},
		{"elseif", "#if($body.n == 1)one#elseif($body.n == 3)three#else other#end", "three"},
		{"word ops", "#if($body.n gt 2 and not false)y#end", "y"},
		{"null false", "#if($nope)y#{else}n#end", "n"},
		{"foreach", "#foreach($t in $body.tags)$t$foreach.count#if($foreach.hasNext),#end#end", "a1,b2"},
		{"foreach range", "#foreach($i in [1..3])$i#end", "123"},
		{"foreach map values", `#set($m = {"a": 1, "b": 2})#foreach($v in $m)$v#end`, "12"},
		{"break", "#foreach($i in [1..5])#if($i == 3)#break#end$i#end", "12"},
		{"stop", "a#stop b", "a"},
		{"line comment", "a## comment\nb", "ab"},
		{"block comment", "a#* x *#b", "ab"},
		{"unparsed", "#[[$name]]#", "$name"},
		{"map literal tostring", `#set($m = {"a": 1, "b": "x"})$m`, "{a=1, b=x}"},
		{"list tostring", `#set($l = [1, "two"])$l`, "[1, two]"},
		{"string methods", `$name.toUpperCase() $name.length() $name.substring(1) $name.replace("p", "b")`, "PET 3 et bet"},
		{"string regex", `#set($s = "a1b22")$s.replaceAll("[0-9]+", "-") $s.matches("[a-z0-9]+") $s.split("[0-9]+")`, "a-b- true [a, b]"},
		{"list methods", `#set($l = [])#set($d = $l.add("x"))$l.size() $l.get(0) $l.contains("x")`, "1 x true"},
		{"map methods", `#set($m = {})#set($d = $m.put("k", "v"))$m.get("k") $m.containsKey("k") $m.keySet() $m.size()`, "v true [k] 1"},
		{"set map property", `#set($m = {})#set($m.k = "v")$m`, "{k=v}"},
		{"directive line gobbled", "a\n  #set($x = 1)\nb", "a\nb"},
		{"if block gobbled", "#if(true)\n  yes\n#end\n", "  yes\n"},
		{"hash text", "a # b", "a # b"},
		{"braced directive", "#{if}(true)y#{else}n#{end}", "y"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := render(t, c.src, map[string]any{"name": "pet", "body": body})
			if got != c.want {
				t.Fatalf("render(%q) = %q, want %q", c.src, got, c.want)
			}
		})
	}
}

func TestReturnDirective(t *testing.T) {
	tmpl, err := Parse(`before#return({"a": 1})after`)
	if err != nil {
		t.Fatal(err)
	}

	res, err := tmpl.Render(context.Background(), nil, RenderOptions{})
	if err != nil {
		t.Fatal(err)
	}

	if !res.Returned || res.Output != "before" || mustJSON(t, res.ReturnValue) != `{"a":1}` {
		t.Fatalf("got %+v", res)
	}
}

func TestParseRejectsUnsupported(t *testing.T) {
	for _, src := range []string{
		"#macro(x)#end", "#parse('x')", "#include('x')", "#evaluate('x')", "#define($x)#end",
		"#if(true)x", "#foreach($i in [1])x", "#end", "#set($x = )", `#set($x = "open)`,
	} {
		if _, err := Parse(src); err == nil {
			t.Errorf("Parse(%q) succeeded, want error", src)
		}
	}
}

func TestForeachCapAndStepBudget(t *testing.T) {
	src := `#set($l = [])#foreach($i in [1..5000])#set($d = $l.add($i))#end$l.size()`
	if got := render(t, src, nil); got != "1000" {
		t.Fatalf("foreach cap: got %s", got)
	}

	tmpl, _ := Parse(`#foreach($i in [1..1000])#foreach($j in [1..1000])x#end#end`)

	_, err := tmpl.Render(context.Background(), nil, RenderOptions{MaxSteps: 10000})
	if !errors.Is(err, ErrStepBudget) {
		t.Fatalf("step budget: err = %v", err)
	}

	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	if _, err := tmpl.Render(ctx, nil, RenderOptions{}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline: err = %v", err)
	}
}

type hostObj struct{}

func (hostObj) Get(name string) (any, bool) {
	if name == "prop" {
		return "P", true
	}

	return nil, false
}

func (hostObj) Call(name string, args []any) (any, bool, error) {
	switch name {
	case "echo":
		return args[0], true, nil
	case "fail":
		return nil, true, errors.New("boom")
	}

	return nil, false, nil
}

func TestHostObject(t *testing.T) {
	if got := render(t, `$h.prop $h.echo("x") [$h.nope()]`, map[string]any{"h": hostObj{}}); got != "P x []" {
		t.Fatalf("got %q", got)
	}

	tmpl, _ := Parse(`$h.fail()`)
	if _, err := tmpl.Render(context.Background(), map[string]any{"h": hostObj{}}, RenderOptions{}); err == nil {
		t.Fatal("host error not surfaced")
	}
}

func TestJSONRoundTripKeepsOrder(t *testing.T) {
	src := `{"z":1,"a":[true,null,"s",2.5],"m":{"b":2,"a":1}}`

	v, err := ParseJSON(src)
	if err != nil {
		t.Fatal(err)
	}

	if got := mustJSON(t, v); got != src {
		t.Fatalf("ToJSON = %s, want %s", got, src)
	}

	if _, err := ParseJSON(`{"a":1} x`); err == nil {
		t.Fatal("trailing data accepted")
	}

	if got := mustJSON(t, "<&>"); got != `"<&>"` {
		t.Fatalf("html escaped: %s", got)
	}

	if !strings.Contains(mustJSON(t, StringMap(map[string]string{"b": "2", "a": "1"})), `{"a":"1","b":"2"}`) {
		t.Fatal("StringMap order")
	}
}
