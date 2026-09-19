package serverkit

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsdynamodb "github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	awsec2 "github.com/aws/aws-sdk-go-v2/service/ec2"

	"github.com/stackshy/cloudemu/v2/server/admin"
)

const (
	mrWest = "us-west-2"
	mrEast = "us-east-1"
)

// newMultiRegionApp builds an admin-enabled AWS App and returns it with an
// httptest server fronting its AWS backend (admin control + region mux).
func newMultiRegionApp(t *testing.T) (*App, *httptest.Server) {
	t.Helper()

	app := newTestApp(t, Config{
		Providers: []string{"aws"},
		Host:      "127.0.0.1",
		Ports:     map[string]string{"aws": "0"},
		Admin:     true,
		Out:       io.Discard,
	})

	ts := httptest.NewServer(app.handlerFor(app.backends["aws"], app.seedFor("aws")))
	t.Cleanup(ts.Close)

	return app, ts
}

func mrDynamo(t *testing.T, url, region string) *awsdynamodb.Client {
	t.Helper()

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion(region),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	cfg.BaseEndpoint = aws.String(url)

	return awsdynamodb.NewFromConfig(cfg)
}

func mrEC2(t *testing.T, url, region string) *awsec2.Client {
	t.Helper()

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion(region),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	cfg.BaseEndpoint = aws.String(url)

	return awsec2.NewFromConfig(cfg)
}

func mrCreateTable(t *testing.T, c *awsdynamodb.Client, name string) {
	t.Helper()

	if _, err := c.CreateTable(context.Background(), &awsdynamodb.CreateTableInput{
		TableName:   aws.String(name),
		BillingMode: ddbtypes.BillingModePayPerRequest,
		AttributeDefinitions: []ddbtypes.AttributeDefinition{
			{AttributeName: aws.String("id"), AttributeType: ddbtypes.ScalarAttributeTypeS},
		},
		KeySchema: []ddbtypes.KeySchemaElement{{AttributeName: aws.String("id"), KeyType: ddbtypes.KeyTypeHash}},
	}); err != nil {
		t.Fatalf("CreateTable(%s): %v", name, err)
	}
}

func mrListTables(t *testing.T, c *awsdynamodb.Client) []string {
	t.Helper()

	out, err := c.ListTables(context.Background(), &awsdynamodb.ListTablesInput{})
	if err != nil {
		t.Fatalf("ListTables: %v", err)
	}

	return out.TableNames
}

func mrRunInstance(t *testing.T, c *awsec2.Client) {
	t.Helper()

	if _, err := c.RunInstances(context.Background(), &awsec2.RunInstancesInput{
		ImageId: aws.String("ami-123"), MinCount: aws.Int32(1), MaxCount: aws.Int32(1),
	}); err != nil {
		t.Fatalf("RunInstances: %v", err)
	}
}

// TestMultiRegionSnapshotResetRestore proves the whole cycle: state in two
// regions is captured, reset wipes every region, and restore brings both back —
// including a region (us-west-2) that has NO live provider at restore time and
// must be recreated from the snapshot.
func TestMultiRegionSnapshotResetRestore(t *testing.T) {
	app, ts := newMultiRegionApp(t)

	west := mrDynamo(t, ts.URL, mrWest)
	east := mrDynamo(t, ts.URL, mrEast)

	mrCreateTable(t, west, "west-table")
	mrCreateTable(t, east, "east-table")

	snap, err := app.snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	// Sanity: the snapshot carries per-region AWS keys.
	var parsed struct {
		Providers map[string]json.RawMessage `json:"providers"`
	}
	if err := json.Unmarshal(snap, &parsed); err != nil {
		t.Fatalf("parse snapshot: %v", err)
	}
	for _, key := range []string{"aws", "aws@" + mrWest, "aws@" + mrEast} {
		if _, ok := parsed.Providers[key]; !ok {
			t.Fatalf("snapshot missing provider key %q; have %v", key, keysOf(parsed.Providers))
		}
	}

	// Reset wipes every region.
	app.Rebuild()

	if got := mrListTables(t, west); len(got) != 0 {
		t.Fatalf("after reset west ListTables = %v, want empty", got)
	}
	if got := mrListTables(t, east); len(got) != 0 {
		t.Fatalf("after reset east ListTables = %v, want empty", got)
	}

	// Restore brings both regions back — us-west-2 has no live provider yet.
	if err := app.restore(snap); err != nil {
		t.Fatalf("restore: %v", err)
	}

	if got := mrListTables(t, west); len(got) != 1 || got[0] != "west-table" {
		t.Fatalf("after restore west ListTables = %v, want [west-table] (lazy region recreated)", got)
	}
	if got := mrListTables(t, east); len(got) != 1 || got[0] != "east-table" {
		t.Fatalf("after restore east ListTables = %v, want [east-table]", got)
	}
}

// TestAdminResetClearsAllRegions proves POST /_cloudemu/reset wipes every
// region, not only the default one.
func TestAdminResetClearsAllRegions(t *testing.T) {
	_, ts := newMultiRegionApp(t)

	west := mrDynamo(t, ts.URL, mrWest)
	east := mrDynamo(t, ts.URL, mrEast)
	mrCreateTable(t, west, "w")
	mrCreateTable(t, east, "e")

	resp, err := http.Post(ts.URL+admin.Prefix+"reset", "application/json", nil)
	if err != nil {
		t.Fatalf("reset POST: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reset status = %d, want 200", resp.StatusCode)
	}

	if got := mrListTables(t, west); len(got) != 0 {
		t.Fatalf("west after reset = %v, want empty", got)
	}
	if got := mrListTables(t, east); len(got) != 0 {
		t.Fatalf("east after reset = %v, want empty", got)
	}
}

// TestCrossRegionCostAggregation proves GET /_cloudemu/cost sums resources
// across regions: one instance in each region yields both instances (and their
// root volumes) in the estimate, whereas a single-region view would show half.
func TestCrossRegionCostAggregation(t *testing.T) {
	_, ts := newMultiRegionApp(t)

	mrRunInstance(t, mrEC2(t, ts.URL, mrWest))
	mrRunInstance(t, mrEC2(t, ts.URL, mrEast))

	resp, err := http.Get(ts.URL + admin.Prefix + "cost")
	if err != nil {
		t.Fatalf("cost GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("cost status = %d, want 200", resp.StatusCode)
	}

	var out struct {
		EstimatedMonthlyUSD float64 `json:"estimatedMonthlyUsd"`
		Resources           []struct {
			Provider string `json:"provider"`
		} `json:"resources"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode cost: %v", err)
	}

	// 2 instances + their 2 root volumes across the two regions.
	if len(out.Resources) != 4 {
		t.Fatalf("cost aggregated %d resources, want 4 (2 instances + 2 volumes across regions)", len(out.Resources))
	}
	if out.EstimatedMonthlyUSD <= 0 {
		t.Fatalf("aggregated cost = $%.2f, want > 0", out.EstimatedMonthlyUSD)
	}
	for _, r := range out.Resources {
		if r.Provider != "aws" {
			t.Fatalf("resource provider = %q, want aws", r.Provider)
		}
	}
}

func keysOf(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}

	return out
}
