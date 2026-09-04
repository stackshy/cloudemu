// These tests drive Firestore aggregation queries (:runAggregationQuery)
// end to end. COUNT/SUM/AVG are exercised through the real
// cloud.google.com/go/firestore REST SDK; the count(up_to) bound and the
// per-(project, database) namespace isolation of aggregation are exercised over
// raw REST (the SDK's NewAggregationQuery has no up_to knob and its constructor
// hard-codes the "(default)" database).
package firestore_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	gcpfirestore "cloud.google.com/go/firestore"
	firestorepb "cloud.google.com/go/firestore/apiv1/firestorepb"
	"google.golang.org/api/option"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
	dbdriver "github.com/stackshy/cloudemu/v2/services/database/driver"
)

// newAggServer boots a fresh emulator + GCP server with the given collections
// pre-created as driver tables, and returns both the raw httptest server and a
// real Firestore REST SDK client pointed at it (aggregation tests need both the
// SDK and raw REST for count(up_to)).
func newAggServer(t *testing.T, project string, colls ...string) (context.Context, *httptest.Server, *gcpfirestore.Client) {
	t.Helper()

	ctx := context.Background()
	cloudP := cloudemu.NewGCP()

	for _, c := range colls {
		if err := cloudP.Firestore.CreateTable(ctx, dbdriver.TableConfig{Name: c, PartitionKey: "\x00id"}); err != nil {
			t.Fatalf("CreateTable(%s): %v", c, err)
		}
	}

	srv := gcpserver.New(gcpserver.Drivers{Firestore: cloudP.Firestore})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	client, err := gcpfirestore.NewRESTClient(ctx, project,
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
		option.WithHTTPClient(ts.Client()),
	)
	if err != nil {
		t.Fatalf("NewRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })

	return ctx, ts, client
}

// seedAggregationCities writes a small, mixed-type cities collection through the
// real SDK: population is always an integer, area always a double, and score is
// present on only two docs (one int, one double) to exercise
// missing-field-skipping and the integer/double result typing of SUM.
func seedAggregationCities(t *testing.T, ctx context.Context, coll *gcpfirestore.CollectionRef) {
	t.Helper()

	docs := map[string]map[string]any{
		"SF":  {"population": 800000, "region": "us", "area": 121.4, "score": 10},
		"LA":  {"population": 4000000, "region": "us", "area": 1302.0, "score": 2.5},
		"NYC": {"population": 8000000, "region": "us", "area": 783.8},
		"BER": {"population": 3600000, "region": "eu", "area": 891.7},
	}

	for id, fields := range docs {
		if _, err := coll.Doc(id).Set(ctx, fields); err != nil {
			t.Fatalf("Set %s: %v", id, err)
		}
	}
}

func aggInteger(t *testing.T, res map[string]interface{}, alias string) int64 {
	t.Helper()

	v, ok := res[alias].(*firestorepb.Value)
	if !ok {
		t.Fatalf("aggregate %q: value type %T, want *firestorepb.Value", alias, res[alias])
	}

	if _, ok := v.GetValueType().(*firestorepb.Value_IntegerValue); !ok {
		t.Fatalf("aggregate %q: value %v is not an integerValue", alias, v)
	}

	return v.GetIntegerValue()
}

func aggDouble(t *testing.T, res map[string]interface{}, alias string) float64 {
	t.Helper()

	v, ok := res[alias].(*firestorepb.Value)
	if !ok {
		t.Fatalf("aggregate %q: value type %T, want *firestorepb.Value", alias, res[alias])
	}

	if _, ok := v.GetValueType().(*firestorepb.Value_DoubleValue); !ok {
		t.Fatalf("aggregate %q: value %v is not a doubleValue", alias, v)
	}

	return v.GetDoubleValue()
}

// TestDatabaseAggregation drives COUNT/SUM/AVG over the whole collection and a
// filtered subset through the real SDK, asserting both the values and their
// integer-vs-double result typing.
func TestDatabaseAggregation(t *testing.T) {
	ctx, _, client := newAggServer(t, dbProject, "cities")
	coll := client.Collection("cities")
	seedAggregationCities(t, ctx, coll)

	// Whole-collection aggregates in a single query.
	res, err := coll.NewAggregationQuery().
		WithCount("count").
		WithSum("population", "pop_sum").
		WithAvg("population", "pop_avg").
		WithSum("area", "area_sum").
		Get(ctx)
	if err != nil {
		t.Fatalf("aggregation Get: %v", err)
	}

	if got := aggInteger(t, res, "count"); got != 4 {
		t.Errorf("count = %d, want 4", got)
	}

	// All populations are integers -> integer result.
	if got := aggInteger(t, res, "pop_sum"); got != 16_400_000 {
		t.Errorf("sum(population) = %d, want 16400000", got)
	}

	if got := aggDouble(t, res, "pop_avg"); got != 4_100_000 {
		t.Errorf("avg(population) = %v, want 4100000", got)
	}

	// All areas are doubles -> double result.
	if got := aggDouble(t, res, "area_sum"); math.Abs(got-3098.9) > 1e-6 {
		t.Errorf("sum(area) = %v, want 3098.9", got)
	}

	// SUM/AVG over a field present on only two docs, mixing an int and a double
	// summand -> double result, missing docs skipped.
	mres, err := coll.NewAggregationQuery().WithSum("score", "score_sum").WithAvg("score", "score_avg").Get(ctx)
	if err != nil {
		t.Fatalf("mixed aggregation Get: %v", err)
	}

	if got := aggDouble(t, mres, "score_sum"); math.Abs(got-12.5) > 1e-6 {
		t.Errorf("sum(score) = %v, want 12.5 (double, mixed int+float)", got)
	}

	if got := aggDouble(t, mres, "score_avg"); math.Abs(got-6.25) > 1e-6 {
		t.Errorf("avg(score) = %v, want 6.25 (only two docs have score)", got)
	}

	// Filtered aggregation: region == "us" keeps SF/LA/NYC.
	usQuery := coll.Where("region", "==", "us")
	fres, err := usQuery.NewAggregationQuery().WithCount("count").WithSum("population", "pop_sum").Get(ctx)
	if err != nil {
		t.Fatalf("filtered aggregation Get: %v", err)
	}

	if got := aggInteger(t, fres, "count"); got != 3 {
		t.Errorf("filtered count = %d, want 3", got)
	}

	if got := aggInteger(t, fres, "pop_sum"); got != 12_800_000 {
		t.Errorf("filtered sum(population) = %d, want 12800000", got)
	}
}

// TestDatabaseAggregationEmptyAndNull asserts Firestore's empty-set semantics:
// COUNT and SUM over no matching documents are integer 0, but AVG over a set
// with no numeric values is NULL (not 0).
func TestDatabaseAggregationEmptyAndNull(t *testing.T) {
	ctx, _, client := newAggServer(t, dbProject, "cities")
	coll := client.Collection("cities")
	seedAggregationCities(t, ctx, coll)

	emptyQuery := coll.Where("region", "==", "antarctica")
	empty := emptyQuery.NewAggregationQuery()

	res, err := empty.WithCount("count").WithSum("population", "pop_sum").WithAvg("population", "pop_avg").Get(ctx)
	if err != nil {
		t.Fatalf("empty aggregation Get: %v", err)
	}

	if got := aggInteger(t, res, "count"); got != 0 {
		t.Errorf("count over empty set = %d, want 0", got)
	}

	if got := aggInteger(t, res, "pop_sum"); got != 0 {
		t.Errorf("sum over empty set = %d, want integer 0", got)
	}

	v, ok := res["pop_avg"].(*firestorepb.Value)
	if !ok {
		t.Fatalf("avg value type %T, want *firestorepb.Value", res["pop_avg"])
	}

	if _, isNull := v.GetValueType().(*firestorepb.Value_NullValue); !isNull {
		t.Errorf("avg over empty numeric set = %v, want nullValue", v)
	}
}

// restAggregateCount posts a raw :runAggregationQuery counting a collection,
// optionally with a count(up_to) bound, and returns the integer count.
func restAggregateCount(t *testing.T, ts *httptest.Server, project, database, coll, upTo string) int64 {
	t.Helper()

	count := map[string]any{}
	if upTo != "" {
		count["upTo"] = upTo
	}

	body := map[string]any{
		"structuredAggregationQuery": map[string]any{
			"structuredQuery": map[string]any{
				"from": []map[string]any{{"collectionId": coll}},
			},
			"aggregations": []map[string]any{
				{"alias": "c", "count": count},
			},
		},
	}

	buf, _ := json.Marshal(body)
	url := ts.URL + "/v1/projects/" + project + "/databases/" + database + "/documents:runAggregationQuery"

	resp, err := ts.Client().Post(url, "application/json", bytes.NewReader(buf))
	if err != nil {
		t.Fatalf("runAggregationQuery %s/%s: %v", project, database, err)
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		out, _ := io.ReadAll(resp.Body)
		t.Fatalf("runAggregationQuery %s/%s: status %d: %s", project, database, resp.StatusCode, out)
	}

	var decoded []struct {
		Result struct {
			AggregateFields map[string]struct {
				IntegerValue string `json:"integerValue"`
			} `json:"aggregateFields"`
		} `json:"result"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode aggregation response: %v", err)
	}

	if len(decoded) != 1 {
		t.Fatalf("aggregation response has %d rows, want 1", len(decoded))
	}

	raw := decoded[0].Result.AggregateFields["c"].IntegerValue

	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		t.Fatalf("parse count %q: %v", raw, err)
	}

	return n
}

// TestDatabaseAggregationCountUpTo asserts count(up_to) caps the returned count
// at the requested bound (raw REST, since the SDK has no up_to knob).
func TestDatabaseAggregationCountUpTo(t *testing.T) {
	ctx, ts, client := newAggServer(t, dbProject, "cities")
	coll := client.Collection("cities")
	seedAggregationCities(t, ctx, coll)

	// Four docs; an unbounded count sees all four, a count(up_to: 2) caps at 2.
	if got := restAggregateCount(t, ts, dbProject, "(default)", "cities", ""); got != 4 {
		t.Errorf("unbounded count = %d, want 4", got)
	}

	if got := restAggregateCount(t, ts, dbProject, "(default)", "cities", "2"); got != 2 {
		t.Errorf("count(up_to: 2) = %d, want 2", got)
	}

	// A bound above the true count returns the true count, not the bound.
	if got := restAggregateCount(t, ts, dbProject, "(default)", "cities", "10"); got != 4 {
		t.Errorf("count(up_to: 10) = %d, want 4", got)
	}
}

// TestAggregationCrossDatabaseIsolation runs :runAggregationQuery against the
// same collection under two databases of one project and asserts each database
// counts only its own documents — confirming the #969 (project, database)
// namespace keying still holds on the aggregation path.
func TestAggregationCrossDatabaseIsolation(t *testing.T) {
	cloudP := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.Drivers{Firestore: cloudP.Firestore})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	const project = "agg-iso-project"

	// Two docs under (default), three under named1.
	restCreate(t, ts, project, "(default)", "cities", "SF", map[string]any{"db": "default"})
	restCreate(t, ts, project, "(default)", "cities", "LA", map[string]any{"db": "default"})
	restCreate(t, ts, project, "named1", "cities", "A", map[string]any{"db": "named1"})
	restCreate(t, ts, project, "named1", "cities", "B", map[string]any{"db": "named1"})
	restCreate(t, ts, project, "named1", "cities", "C", map[string]any{"db": "named1"})

	if got := restAggregateCount(t, ts, project, "(default)", "cities", ""); got != 2 {
		t.Errorf("(default) db count = %d, want 2 (cross-database bleed)", got)
	}

	if got := restAggregateCount(t, ts, project, "named1", "cities", ""); got != 3 {
		t.Errorf("named1 db count = %d, want 3 (cross-database bleed)", got)
	}
}
