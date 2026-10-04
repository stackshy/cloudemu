package vtl

import (
	"strings"
	"testing"
)

func TestRenderEdgeCases(t *testing.T) {
	cases := []struct {
		name, src, want string
	}{
		{"unary minus", "#set($a = 3)#set($b = -$a)$b", "-3"},
		{"negative literal", "#set($a = -2.5)$a", "-2.5"},
		{"modulo", "#set($a = 7 % 3)$a", "1"},
		{"float mod", "#set($a = 7.5 % 2)$a", "1.5"},
		{"div zero", "#set($a = 1 / 0)[$a]", "[]"},
		{"float div zero", "#set($a = 1.0 / 0)[$a]", "[]"},
		{"mixed math", "#set($a = 1 + 0.5)#set($b = 2 - 0.5)#set($c = 2 * 0.5)$a $b $c", "1.5 1.5 1.0"},
		{"null arithmetic", "#set($a = $nope + 1)[$a]", "[]"},
		{"string compare", `#if("a" < "b" && "b" >= "b" && "c" > "b" && "a" <= "a")y#end`, "y"},
		{"incomparable", `#if("a" < 1)y#{else}n#end`, "n"},
		{"word comparisons", `#if(1 ne 2 and 2 eq 2.0 and 1 lt 2 and 2 le 2 and 3 ge 2)y#end`, "y"},
		{"or", "#if(false || $nope || true)y#end", "y"},
		{"or word", "#if(false or false)y#{else}n#end", "n"},
		{"null equality", "#if($nope == $other)y#end#if($nope != 1)z#end", "yz"},
		{"bool equality", "#if(true == true && true != false)y#end", "y"},
		{"mixed equality", `#if("1" == 1)y#end`, "y"},
		{"descending range", "#foreach($i in [3..1])$i#end", "321"},
		{"bad range", "#foreach($i in ['a'..2])$i#end[]", "[]"},
		{"foreach non list", "#foreach($i in 5)x#end.", "."},
		{"foreach restores var", "#set($i = 9)#foreach($i in [1..2])#end$i", "9"},
		{"velocityCount", "#foreach($i in [5..6])$velocityCount#end", "12"},
		{"set list index", "#set($l = [1, 2])#set($l[0] = 7)$l", "[7, 2]"},
		{"set map index", `#set($m = {})#set($m["k"] = 1)$m.k`, "1"},
		{"index map and missing", `#set($m = {"a": [1]})$m["a"][0]|$m.a[5]|$m.b.c|$name[0]`, "1|||"},
		{
			"string methods",
			`#set($s = " Ab ")$s.trim().toLowerCase()|$s.isEmpty()|$s.contains("A")|$s.startsWith(" ")|` +
				`$s.endsWith("b")|$s.indexOf("b")|$s.lastIndexOf(" ")|$s.equalsIgnoreCase(" ab ")|` +
				`$s.charAt(1)|$s.charAt(99)|$s.substring(1, 3)|$s.substring(9)`,
			"ab|false|true|true|false|2|3|true|A||Ab|",
		},
		{"replaceFirst", `#set($s = "aXbX")$s.replaceFirst("X", "-")$s.replaceFirst("Z", "-")`, "a-bXaXbX"},
		{"string equals", `#if($name.equals("pet"))y#end`, "y"},
		{
			"list methods",
			`#set($l = [1, 2, 3])#set($d = $l.remove(0))#set($d = $l.remove(3))$l $l.indexOf(2) $l.isEmpty() ` +
				`#set($d = $l.addAll([9]))$l #set($d = $l.set(0, 0))$l [$l.get(9)]`,
			"[2, 3] 0 false [2, 3, 9] [0, 3, 9] []",
		},
		{"list remove missing", `#set($l = [1])$l.remove("x") [$l.remove(5)]`, "false []"},
		{
			"map methods",
			`#set($m = {"a": 1})#set($d = $m.putAll({"b": 2}))$m.values() $m.entrySet() $m.remove("a") ` +
				`$m.isEmpty() [$m.remove("zz")]`,
			"[1, 2] [{key=a, value=1}, {key=b, value=2}] 1 false []",
		},
		{"empty property", `#set($l = [])#if($l.empty)y#end`, "y"},
		{"number toString", "$n.toString()", "5"},
		{"unknown method", "[$name.nope()][$n.foo()]", "[][]"},
		{"interpolated stop", `#set($s = "a#stop b")$s`, "a"},
		{"elseif gobble", "#if(false)\na\n#elseif(true)\nb\n#end\n", "b\n"},
		{"return no value", "a#return b", "a"},
		{"block comment gobble", "x\n#* c *#\ny", "x\ny"},
		{"group", "#set($a = (1 + 2) * 3)$a", "9"},
		{"not", "#if(!false && !$nope)y#end", "y"},
		{"null literal", "#set($a = null)[$a]", "[]"},
		{"backslash", `a\b`, `a\b`},
		{"break outside loop", "a#break b", "a"},
		{"hash at end", "a#", "a#"},
		{"dollar at end", "a$", "a$"},
		{"braced unterminated", "${a", "${a"},
		{"hyphenated name", `#set($m = {})#set($m.X-Y = 1)$m`, "{X-Y=1}"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := render(t, c.src, map[string]any{"name": "pet", "n": int64(5)})
			if got != c.want {
				t.Fatalf("render(%q) = %q, want %q", c.src, got, c.want)
			}
		})
	}
}

func TestParseErrors(t *testing.T) {
	for _, src := range []string{
		"#* open", "#[[ open", "#{if", "#set(x)", "#set($x 1)", "#set($x = 1", "#if(true)a#{else}b",
		"#foreach($a.b in [1])#end", "#foreach($a [1])#end", "#foreach($a in [1]", "#return(1",
		"#set($x = [1 2])", "#set($x = [1..2)", "#set($x = {1 2})", "#set($x = {1: 2)", "#set($x = $a.b(1 2))",
		"#set($x = $a[1)", "#set($x = @)", "#set($x = foo)", "#set($x = 'open)", "#set($x = 99999999999999999999)",
		"#set($x = $)", "#if(true)#elseif(", "#set($x = \"#end\")", "#set($x = (1)",
	} {
		if _, err := Parse(src); err == nil {
			t.Errorf("Parse(%q) succeeded, want error", src)
		} else if !strings.Contains(err.Error(), "vtl: parse error") {
			t.Errorf("Parse(%q) error = %v", src, err)
		}
	}
}

func TestStringifyAndJSON(t *testing.T) {
	if Stringify(1e21) != "1000000000000000000000.0" || Stringify(struct{}{}) != "" {
		t.Fatalf("Stringify: %s", Stringify(1e21))
	}

	if got := mustJSON(t, NewList(int64(1), 2.5, true, nil, hostObj{})); got != `[1,2.5,true,null,null]` {
		t.Fatalf("ToJSON list = %s", got)
	}

	for _, bad := range []string{"", "[1,", `{"a":}`, "]"} {
		if _, err := ParseJSON(bad); err == nil {
			t.Errorf("ParseJSON(%q) accepted", bad)
		}
	}

	if v, _ := ParseJSON("1.25"); v != 1.25 {
		t.Fatal("float parse")
	}

	m := NewMap()
	m.Put("a", 1)
	if m.Remove("zz") != nil || m.Len() != 1 || len(m.Keys()) != 1 {
		t.Fatal("map ops")
	}

	if _, ok := NewList().Index(0); ok {
		t.Fatal("list index")
	}
}
