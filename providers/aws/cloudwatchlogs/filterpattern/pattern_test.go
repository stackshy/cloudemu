package filterpattern

import (
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/errors"
)

// Example events from the filter pattern syntax reference.
const (
	err400  = "[ERROR 400] BAD REQUEST"
	err401  = "[ERROR 401] UNAUTHORIZED REQUEST"
	err419  = "[ERROR 419] MISSING ARGUMENTS"
	err420  = "[ERROR 420] INVALID ARGUMENTS"
	err500  = "[ERROR 500] INTERNAL SERVER ERROR"
	success = "[SUCCESS 200] AUTHORIZED REQUEST"
	info    = "[INFO] REQUEST FAILED"

	trailEvent = `{"eventType":"UpdateTrail","sourceIPAddress":"111.111.111.111","arrayKey":["value","another value"],` +
		`"objectList":[{"name":"a","id":1},{"name":"b","id":2}],"SomeObject":null,"cluster.name":"c"}`
	userEvent = `{"user":{"id":1,"email":"John.Stiles@example.com"},` +
		`"users":[{"id":2,"email":"John.Doe@example.com"},{"id":3,"email":"Jane.Doe@example.com"}],` +
		`"actions":["GET","PUT","DELETE"],"coordinates":[[0,1,2],[4,5,6],[7,8,9]]}`
	metricEvent = `{"bandwidth":80,"latency":40,"refreshRate":60,"responseTime":5,"errorCode":400,` +
		`"number":[1000],"ok":true,"flag":false}`
)

func mustParse(t *testing.T, pattern string) *Pattern {
	t.Helper()

	p, err := Parse(pattern)
	if err != nil {
		t.Fatalf("Parse(%q): %v", pattern, err)
	}

	return p
}

func TestUnstructuredPatterns(t *testing.T) {
	events := []string{err400, err401, err419, err420, err500, success, info}

	tests := []struct {
		pattern string
		want    []string
	}{
		{"ERROR", []string{err400, err401, err419, err420, err500}},
		{"ERROR ARGUMENTS", []string{err419, err420}},
		{"?ERROR ?ARGUMENTS", []string{err400, err401, err419, err420, err500}},
		{"?ERROR ?ARGUMENTS REQUEST", []string{err400, err401, success, info}},
		{`"INTERNAL SERVER ERROR"`, []string{err500}},
		{"ERROR -ARGUMENTS", []string{err400, err401, err500}},
		{`" "`, events},
		{"", events},
		{"%AUTHORIZED%", []string{err401, success}},
		{"%^\\[ERROR\\s4%", []string{err400, err401, err419, err420}},
		{"%BAD|INVALID%", []string{err400, err420}},
		{"error", nil},
	}

	for _, tc := range tests {
		t.Run(tc.pattern, func(t *testing.T) {
			p := mustParse(t, tc.pattern)

			var got []string

			for _, e := range events {
				if p.Match(e) {
					got = append(got, e)
				}
			}

			if strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Fatalf("%q matched %q, want %q", tc.pattern, got, tc.want)
			}
		})
	}
}

func TestJSONPatterns(t *testing.T) {
	tests := []struct {
		pattern, event string
		want           bool
	}{
		{`{ $.eventType = "UpdateTrail" }`, trailEvent, true},
		{`{ $.eventType = "Other" }`, trailEvent, false},
		{`{ $.sourceIPAddress != 123.123.* }`, trailEvent, true},
		{`{ $.sourceIPAddress != 111.111.* }`, trailEvent, false},
		{`{ $.arrayKey[0] = "value" }`, trailEvent, true},
		{`{ $.eventType = %Trail% }`, trailEvent, true},
		{`{ $.arrayKey[*] = %val.{2}% }`, trailEvent, true},
		{`{ $.* = %111\.111\.111\.1[0-9]{1,2}% }`, trailEvent, true},
		{`{ $.['cluster.name'] = "c" }`, trailEvent, true},
		{`{ $.SomeObject IS NULL }`, trailEvent, true},
		{`{ $.eventType IS NULL }`, trailEvent, false},
		{`{ $.SomeOtherObject NOT EXISTS }`, trailEvent, true},
		{`{ $.SomeObject NOT EXISTS }`, trailEvent, false},
		{`{ $.objectList = "a" }`, trailEvent, false},
		{`{ $.objectList[1].id = 2 }`, trailEvent, true},
		{`{ $.eventType = "*" && $.sourceIPAddress != 123.123.* }`, trailEvent, true},
		{`{ ($.user.id = 1) && ($.users[0].email = "John.Doe@example.com") }`, userEvent, true},
		{`{ $.user.email = "John.Stiles@example.com" || $.coordinates[0][1] = "nonmatch" && $.actions[2] = "nonmatch" }`, userEvent, true},
		{`{ ($.user.email = "John.Stiles@example.com" || $.coordinates[0][1] = "nonmatch") && $.actions[2] = "nonmatch" }`, userEvent, false},
		{`{ ($.user.id = 2 && $.users[0].email = "nonmatch") || $.actions[2] = "GET" }`, userEvent, false},
		{`{ $.bandwidth > 75 }`, metricEvent, true},
		{`{ $.latency < 50 }`, metricEvent, true},
		{`{ $.refreshRate >= 60 }`, metricEvent, true},
		{`{ $.responseTime <= 5 }`, metricEvent, true},
		{`{ $.errorCode = 400 }`, metricEvent, true},
		{`{ $.errorCode != 500 }`, metricEvent, true},
		{`{ $.number[0] = 1e+3 }`, metricEvent, true},
		{`{ $.number[0] != 1e-3 }`, metricEvent, true},
		{`{ $.ok IS TRUE }`, metricEvent, true},
		{`{ $.flag IS FALSE }`, metricEvent, true},
		{`{ $.ok IS FALSE }`, metricEvent, false},
		{`{$.latency=40}`, metricEvent, true},
		{`{ $.latency = 40 }`, "latency 40", false},
	}

	for _, tc := range tests {
		t.Run(tc.pattern, func(t *testing.T) {
			if got := mustParse(t, tc.pattern).Match(tc.event); got != tc.want {
				t.Fatalf("Match = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestInvalidPatterns(t *testing.T) {
	for _, pattern := range []string{
		`{ $.a IS NOT NULL }`,
		`{ $.a EXISTS }`,
		`{ $.a = 1`,
		`{ $.a = }`,
		`{ $.a > abc }`,
		`{ $.a & $.b }`,
		`{ $.["a"] = 1 }`,
		`{ $.*.b[*] = 1 }`,
		`{ $.a[*] = 1 && $.b[*] = 1 && $.c[*] = 1 && $.d[*] = 1 }`,
		`{ $.a = %x% && $.b = %y% && $.c = %z% }`,
		`{ ($.a = 1 }`,
		`{ a = 1 }`,
		`%(ab)%`,
		`%ab!%`,
		`%unterminated`,
		`"unterminated`,
		`error:{boom}`,
		`[ip, user`,
		`[a, b=%x%, c=%y%, d=%z%]`,
		`[a, b=1 || c=2]`,
		`[a, b>abc]`,
		`[a-b]`,
		strings.Repeat("a", maxPatternLength+1),
	} {
		_, err := Parse(pattern)
		if err == nil || !errors.IsInvalidArgument(err) {
			t.Errorf("Parse(%.40q) = %v, want InvalidArgument", pattern, err)
		}
	}
}

// TestRegexAllowsLiteralSpace follows the syntax page's own example, which
// has a space inside %...%.
func TestRegexAllowsLiteralSpace(t *testing.T) {
	p := mustParse(t, `%^starting|^initializing|^shutting down%`)

	if !p.Match("shutting down") || !p.Match("starting up") || p.Match("skipping initializing") {
		t.Fatal("regex with a literal space did not match like the documented example")
	}
}

func TestQuotasAccepted(t *testing.T) {
	for _, pattern := range []string{
		`{ $.a[*] = 1 && $.b[*] = 1 && $.c[*] = 1 }`,
		`{ $.a = %x% && $.b = %y% }`,
	} {
		if _, err := Parse(pattern); err != nil {
			t.Errorf("Parse(%q): %v", pattern, err)
		}
	}
}

func TestHasRegexAndExtractedValues(t *testing.T) {
	if mustParse(t, "ERROR").HasRegex() || !mustParse(t, "%ERR%").HasRegex() || !mustParse(t, `{ $.a = %x% }`).HasRegex() {
		t.Fatal("HasRegex mismatch")
	}

	got := mustParse(t, `{ $.latency = * && $.arr[*] = 1 }`).ExtractedValues(`{"latency":50,"arr":[1]}`)
	if len(got) != 1 || got["$.latency"] != "50" {
		t.Fatalf("ExtractedValues = %v, want only $.latency=50", got)
	}

	if v := mustParse(t, "ERROR").ExtractedValues("ERROR"); len(v) != 0 {
		t.Fatalf("unstructured ExtractedValues = %v, want empty", v)
	}
}
