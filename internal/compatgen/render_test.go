package main

import (
	"strings"
	"testing"
)

// renderOne renders a single service block for the given rows.
func renderOne(service string, rows []entry) string {
	var b strings.Builder

	renderService(&b, service, rows)

	return b.String()
}

func TestRenderService_PerLanguageSummary(t *testing.T) {
	cases := []struct {
		name       string
		rows       []entry
		wantLines  []string // substrings that must appear
		absentText []string // substrings that must NOT appear
	}{
		{
			name: "go only renders a single language row",
			rows: []entry{
				{Service: "cache", Operation: "CreateCache", Providers: map[string]cell{
					"aws": {Native: "ElastiCache", Clients: map[string]string{"sdkGo": sdkPass}},
					"gcp": {Native: "Memorystore", Clients: map[string]string{"sdkGo": sdkPass}},
				}},
				{Service: "cache", Operation: "Get", Providers: map[string]cell{
					"aws": {Native: "ElastiCache"},
					"gcp": {Native: "Memorystore"},
				}},
			},
			wantLines: []string{
				"**cache verified per language:**",
				"- Go: AWS 1/2 · GCP 1/2",
			},
			absentText: []string{"Python", "verified via Go SDK"},
		},
		{
			name: "multiple languages each get their own row, untested omitted",
			rows: []entry{
				{Service: "storage", Operation: "PutObject", Providers: map[string]cell{
					"aws": {Native: "S3", Clients: map[string]string{"sdkGo": sdkPass, "sdkPython": sdkPass}},
				}},
				{Service: "storage", Operation: "GetObject", Providers: map[string]cell{
					"aws": {Native: "S3", Clients: map[string]string{"sdkGo": sdkPass}},
				}},
			},
			wantLines: []string{
				"- Go: AWS 2/2",
				"- Python: AWS 1/2",
			},
			// Go is listed before Python (clientOrder); Java has no data so no row.
			absentText: []string{"Java", ".NET"},
		},
		{
			name: "unknown client falls back to its raw key after known ones",
			rows: []entry{
				{Service: "queue", Operation: "SendMessage", Providers: map[string]cell{
					"aws": {Native: "SQS", Clients: map[string]string{"sdkGo": sdkPass, "sdkRust": sdkPass}},
				}},
			},
			wantLines: []string{
				"- Go: AWS 1/1",
				"- sdkRust: AWS 1/1",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := renderOne(strings.Split(tc.rows[0].Service, "|")[0], tc.rows)

			for _, want := range tc.wantLines {
				if !strings.Contains(out, want) {
					t.Errorf("rendered output missing %q\n---\n%s", want, out)
				}
			}

			for _, absent := range tc.absentText {
				if strings.Contains(out, absent) {
					t.Errorf("rendered output unexpectedly contains %q\n---\n%s", absent, out)
				}
			}
		})
	}
}

// TestRenderService_CellSymbols asserts a verified cell shows ✅ and an
// untested-but-supported cell shows the middot, and that only verified cells
// count toward the per-language totals.
func TestRenderService_CellSymbols(t *testing.T) {
	rows := []entry{
		{Service: "dns", Operation: "CreateZone", Providers: map[string]cell{
			"aws": {Native: "Route53", Clients: map[string]string{"sdkGo": sdkPass}},
		}},
		{Service: "dns", Operation: "ListZones", Providers: map[string]cell{
			"aws": {Native: "Route53"},
		}},
	}

	out := renderOne("dns", rows)

	if !strings.Contains(out, "| CreateZone | ✅ |") {
		t.Errorf("verified cell should render ✅\n%s", out)
	}

	if !strings.Contains(out, "| ListZones | · |") {
		t.Errorf("supported-but-untested cell should render middot\n%s", out)
	}

	if !strings.Contains(out, "- Go: AWS 1/2") {
		t.Errorf("only the verified operation should count\n%s", out)
	}
}

// TestOrderedClients keeps known clients in clientOrder and sorts unknowns last.
func TestOrderedClients(t *testing.T) {
	counts := map[string]map[string]int{
		"sdkPython": {"aws": 1},
		"sdkGo":     {"aws": 1},
		"sdkZeta":   {"aws": 1},
		"sdkAlpha":  {"aws": 1},
	}

	got := orderedClients(counts)
	want := []string{"sdkGo", "sdkPython", "sdkAlpha", "sdkZeta"}

	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("orderedClients = %v, want %v", got, want)
	}
}
