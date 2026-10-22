package insights

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func accessRecords() []Record {
	return eventsAt("prod/api", []string{
		"user=alice action=login status=200",
		"user=bob action=logout status=500",
		"no match here",
	}, []time.Duration{0, time.Second, 2 * time.Second})
}

func TestParseCommand(t *testing.T) {
	tests := []struct {
		q, field, want string
	}{
		{`parse @message "user=* action=* status=*" as u, a, s | sort u asc | display u, a, s`, "a", "login|logout"},
		{`parse @message "user=* action=* status=*" as u, a, s | sort u asc | display s`, "s", "200|500"},
		{`parse "user=* action=*" as u, a | sort u asc | display a`, "a", "login status=200|logout status=500"},
		{`parse @message "user=* " as u | fields u`, "u", "bob|alice"},
		{`parse "user=* action=*" as u, a | fields @message`, "@message", "no match here|user=bob action=logout status=500|user=alice action=login status=200"},
		{`parse @message /user=(?<who>\w+) action=(?<what>\w+)/ | filter ispresent(who) | sort who asc | display who, what`,
			"what", "login|logout"},
		{`parse /status=(\d+)/ as code | stats sum(code) as total`, "total", "700"},
		{`parse @message /(?<code>\d{3})$/ | filter code >= 500 | display code`, "code", "500"},
		{`parse @logStream "*/*" as env, svc | stats count(*) as n by env, svc | display svc`, "svc", "api"},
		{`parse @message "user=* " as u | parse u "*l*" as head, tail | filter ispresent(head) | display head`, "head", "a"},
		{`parse @message /a\/b|user=(?<u>[a-z]+)/ | sort u asc | display u`, "u", "alice|bob"},
		{`fields @message | parse @message "action=* " as a | stats count(*) as n by a | sort a asc | display a`, "a", "login|logout"},
	}

	for _, tc := range tests {
		t.Run(tc.q, func(t *testing.T) {
			got := strings.Join(col(runOn(t, tc.q, accessRecords()).Rows, tc.field), "|")
			if got != tc.want {
				t.Fatalf("%s = %q, want %q", tc.field, got, tc.want)
			}
		})
	}
}

func TestParseGlobRegex(t *testing.T) {
	if got := globRegex("[*] *"); got != `(?s)\[(.*?)\] (.*)` {
		t.Fatalf("globRegex = %s", got)
	}

	if got := globRegex("a=*;"); got != `(?s)a=(.*?);` {
		t.Fatalf("globRegex = %s", got)
	}
}

func TestParseCompileErrors(t *testing.T) {
	for _, q := range []string{
		`parse`,
		`parse @message`,
		`parse @message "user=*"`,
		`parse @message "user=* action=*" as u`,
		`parse @message "no wildcards" as u`,
		`parse @message /x(y)/`,
		`parse @message /(?<a>x)/ as a, b`,
		`parse @message /(?<a>x/`,
		`parse @message logfmt as lf`,
		`parse csv as a, b`,
		`parse @message /(?<a>x)/ multi`,
		`parse @message "a=*" as`,
		`parse @message "a=*" as x y`,
		`stats count(*) as n | parse @message "a=*" as x`,
	} {
		_, err := Parse(q)

		var ce *CompileError
		if !errors.As(err, &ce) {
			t.Errorf("Parse(%q) = %v, want a CompileError", q, err)
			continue
		}

		if ce.Start < 0 || ce.End < ce.Start || ce.End > len(q) {
			t.Errorf("Parse(%q) offsets %d..%d out of range", q, ce.Start, ce.End)
		}
	}
}

func TestParseKeepsDivision(t *testing.T) {
	res := run(t, "fields latency / 2 as half | filter ispresent(half) | sort half asc | display half")
	if got := strings.Join(col(res.Rows, "half"), "|"); got != "7.5|40|60" {
		t.Fatalf("half = %s", got)
	}
}
