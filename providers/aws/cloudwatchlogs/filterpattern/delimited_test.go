package filterpattern

import (
	"maps"
	"testing"
)

// Space-delimited example events from the filter pattern syntax reference
// and the TestMetricFilter API examples.
const (
	apacheHTML = `127.0.0.1 Prod frank [10/Oct/2000:13:25:15 -0700] "GET /index.html HTTP/1.0" 404 1534`
	apache200  = `127.0.0.1 - frank [10/Oct/2000:13:25:15 -0700] "GET /apache_pb.gif HTTP/1.0" 200 1534`
	apache500  = `127.0.0.1 - frank [10/Oct/2000:13:35:22 -0700] "GET /apache_pb.gif HTTP/1.0" 500 5324`
	apacheIdx  = `127.0.0.1 - frank [10/Oct/2000:13:25:15 -0700] "GET /index.html HTTP/1.0" 404 1534`
	apacheAbt  = `127.0.0.1 - frank [10/Oct/2000:13:35:22 -0700] "GET /about-us/index.html HTTP/1.0" 200 5324`

	infoGet  = "INFO 09/25/2014 12:00:00 GET /service/resource/67 1200"
	infoPost = "INFO 09/25/2014 12:00:01 POST /service/resource/67/part/111 1310"
	warnLine = "WARNING 09/25/2014 12:00:02 Invalid user request"
	errLine  = "ERROR 09/25/2014 12:00:02 Failed to process request"
)

func TestSpaceDelimitedMatch(t *testing.T) {
	tests := []struct {
		pattern, event string
		want           bool
	}{
		{`[ip=%127\.0\.0\.[1-9]%, user, username, timestamp, request =*.html*, status_code = 4*, bytes]`, apacheHTML, true},
		{`[ip=%127\.0\.0\.[1-9]%, user, username, timestamp, request =*.html*, status_code = 5*, bytes]`, apacheHTML, false},
		{`[..., request =*.html*, status_code = 4*, bytes]`, apacheHTML, true},
		{`[ip, user, username, timestamp, request =*.html*, status_code = 404 || status_code = 410, bytes]`, apacheHTML, true},
		{`[ip, server, username, timestamp, request, status_code, bytes > 1000]`, apacheHTML, true},
		{`[ip, server, username, timestamp, request, status_code, bytes > 2000]`, apacheHTML, false},
		{`[ip, server, username, timestamp, request, status_code, bytes >= 1534]`, apacheHTML, true},
		{`[ip, server, username, timestamp, request, status_code != 404, bytes]`, apacheHTML, false},
		{`[ip, server]`, apacheHTML, true},
		{`[ip, server, username, timestamp, request, status_code, bytes, extra]`, apacheHTML, false},
		{`[w1=ERROR, w2]`, errLine, true},
		{`[w1=ERROR, w2]`, warnLine, false},
		{`[w1=ERROR]`, errLine, false},
		{`[w1=ERROR || w1=WARNING, w2]`, warnLine, true},
		{`[w1=ERROR || w1=%WARN%, w2]`, warnLine, true},
		{`[w1!=ERROR && w1!=WARNING, w2]`, infoGet, true},
		{`[w1!=ERROR && w1!=WARNING, w2]`, errLine, false},
		{`[logLevel, date, time, method, url=%/service/resource/[0-9]+$%, response_time]`, infoGet, true},
		{`[logLevel, date, time, method, url=%/service/resource/[0-9]+$%, response_time]`, infoPost, false},
		{`[logLevel, date, time, method, url=%/service/resource/[0-9]+/part/[0-9]+$%, response_time]`, infoPost, true},
		{`[..., status_code=200, size]`, apache200, true},
		{`[..., status_code=200, size]`, apache500, false},
		{`[..., request=*.html*, status_code=4*,]`, apacheIdx, true},
		{`[..., request=*.html*, status_code=4*,]`, apacheAbt, false},
		{`[]`, apache200, true},
		{`[a, ..., b="1534"]`, apache200, true},
	}

	for _, tc := range tests {
		t.Run(tc.pattern+" "+tc.event, func(t *testing.T) {
			if got := mustParse(t, tc.pattern).Match(tc.event); got != tc.want {
				t.Fatalf("Match = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestSpaceDelimitedExtractedValues follows the TestMetricFilter API examples.
func TestSpaceDelimitedExtractedValues(t *testing.T) {
	tests := []struct {
		pattern string
		want    map[string]string
	}{
		{`[ip, identity, user_id, timestamp, request, status_code, size]`, map[string]string{
			"$ip": "127.0.0.1", "$identity": "-", "$user_id": "frank", "$timestamp": "10/Oct/2000:13:25:15 -0700",
			"$request": "GET /apache_pb.gif HTTP/1.0", "$status_code": "200", "$size": "1534",
		}},
		{`[..., size]`, map[string]string{
			"$1": "127.0.0.1", "$2": "-", "$3": "frank", "$4": "10/Oct/2000:13:25:15 -0700",
			"$5": "GET /apache_pb.gif HTTP/1.0", "$6": "200", "$size": "1534",
		}},
		{`[]`, map[string]string{
			"$1": "127.0.0.1", "$2": "-", "$3": "frank", "$4": "10/Oct/2000:13:25:15 -0700",
			"$5": "GET /apache_pb.gif HTTP/1.0", "$6": "200", "$7": "1534",
		}},
		{`[..., status_code=200, size]`, map[string]string{
			"$1": "127.0.0.1", "$2": "-", "$3": "frank", "$4": "10/Oct/2000:13:25:15 -0700",
			"$5": "GET /apache_pb.gif HTTP/1.0", "$status_code": "200", "$size": "1534",
		}},
	}

	for _, tc := range tests {
		t.Run(tc.pattern, func(t *testing.T) {
			got := mustParse(t, tc.pattern).ExtractedValues(apache200)
			if !maps.Equal(got, tc.want) {
				t.Fatalf("ExtractedValues = %v\nwant %v", got, tc.want)
			}
		})
	}
}

func TestValueReferences(t *testing.T) {
	delim := mustParse(t, `[ip, user, ..., status, bytes>1000]`)

	if v, ok := delim.Value(apache200, "$bytes"); !ok || v != "1534" {
		t.Fatalf("$bytes = %q, %v", v, ok)
	}

	if v, ok := delim.Value(apache200, "$3"); !ok || v != "frank" {
		t.Fatalf("$3 = %q, %v", v, ok)
	}

	if _, ok := delim.Value(apache200, "$missing"); ok {
		t.Fatal("$missing resolved")
	}

	js := mustParse(t, `{ $.level = "ERROR" }`)

	if v, ok := js.Value(`{"level":"ERROR","latency":42}`, "$.latency"); !ok || v != "42" {
		t.Fatalf("$.latency = %q, %v", v, ok)
	}

	if _, ok := js.Value(`{"level":"ERROR"}`, "$.latency"); ok {
		t.Fatal("absent $.latency resolved")
	}
}

func TestRegexRejectsLiteralSpace(t *testing.T) {
	if _, err := Parse(`%shutting down%`); err == nil {
		t.Fatal("regex with a literal space accepted")
	}

	p := mustParse(t, `%shutting\sdown%`)
	if !p.Match("shutting down now") {
		t.Fatal(`\s did not match a space`)
	}

	if !mustParse(t, `%shutting\x20down%`).Match("shutting down") {
		t.Fatal(`\x20 did not match a space`)
	}
}
