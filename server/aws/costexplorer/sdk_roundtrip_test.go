package costexplorer_test

import (
	"context"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	ce "github.com/aws/aws-sdk-go-v2/service/costexplorer"
	cetypes "github.com/aws/aws-sdk-go-v2/service/costexplorer/types"

	cloudemu "github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
)

// newCostExplorerClient stands up an in-process AWS server with two running
// instances (an always-on cost the inventory pricing reports) and returns a
// real Cost Explorer SDK client pointed at it.
func newCostExplorerClient(t *testing.T) *ce.Client {
	t.Helper()

	cloud := cloudemu.NewAWS()

	if _, err := cloud.EC2.RunInstances(context.Background(),
		computedriver.InstanceConfig{ImageID: "ami-1", InstanceType: "t3.micro"}, 2); err != nil {
		t.Fatalf("run instances: %v", err)
	}

	srv := awsserver.New(awsserver.Drivers{CostExplorer: cloud.ResourceDiscovery})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return ce.NewFromConfig(cfg, func(o *ce.Options) { o.BaseEndpoint = aws.String(ts.URL) })
}

func amount(t *testing.T, s *string) float64 {
	t.Helper()

	v, err := strconv.ParseFloat(aws.ToString(s), 64)
	if err != nil {
		t.Fatalf("parse amount %q: %v", aws.ToString(s), err)
	}

	return v
}

func TestSDKGetCostAndUsageTotal(t *testing.T) {
	ctx := context.Background()
	c := newCostExplorerClient(t)

	out, err := c.GetCostAndUsage(ctx, &ce.GetCostAndUsageInput{
		Granularity: cetypes.GranularityMonthly,
		Metrics:     []string{"UnblendedCost"},
		TimePeriod:  &cetypes.DateInterval{Start: aws.String("2024-01-01"), End: aws.String("2024-03-01")},
	})
	if err != nil {
		t.Fatalf("GetCostAndUsage: %v", err)
	}

	if len(out.ResultsByTime) != 2 {
		t.Fatalf("ResultsByTime = %d buckets, want 2 months", len(out.ResultsByTime))
	}

	for _, rbt := range out.ResultsByTime {
		mv, ok := rbt.Total["UnblendedCost"]
		if !ok {
			t.Fatalf("bucket %v missing UnblendedCost total", aws.ToString(rbt.TimePeriod.Start))
		}

		if amount(t, mv.Amount) <= 0 || aws.ToString(mv.Unit) != "USD" {
			t.Fatalf("bucket total = %s %s, want >0 USD", aws.ToString(mv.Amount), aws.ToString(mv.Unit))
		}
	}
}

func TestSDKGetCostAndUsageGroupByService(t *testing.T) {
	ctx := context.Background()
	c := newCostExplorerClient(t)

	out, err := c.GetCostAndUsage(ctx, &ce.GetCostAndUsageInput{
		Granularity: cetypes.GranularityMonthly,
		Metrics:     []string{"UnblendedCost"},
		TimePeriod:  &cetypes.DateInterval{Start: aws.String("2024-01-01"), End: aws.String("2024-02-01")},
		GroupBy:     []cetypes.GroupDefinition{{Type: cetypes.GroupDefinitionTypeDimension, Key: aws.String("SERVICE")}},
	})
	if err != nil {
		t.Fatalf("GetCostAndUsage grouped: %v", err)
	}

	if len(out.GroupDefinitions) != 1 || aws.ToString(out.GroupDefinitions[0].Key) != "SERVICE" {
		t.Fatalf("GroupDefinitions = %+v, want one SERVICE definition", out.GroupDefinitions)
	}

	if len(out.ResultsByTime) != 1 {
		t.Fatalf("ResultsByTime = %d, want 1 month", len(out.ResultsByTime))
	}

	var found bool

	for _, g := range out.ResultsByTime[0].Groups {
		if len(g.Keys) == 0 {
			t.Fatalf("group with no keys: %+v", g)
		}

		if g.Keys[0] == "Amazon Elastic Compute Cloud - Compute" {
			found = true

			if amount(t, g.Metrics["UnblendedCost"].Amount) <= 0 {
				t.Fatalf("compute group cost = %s, want >0", aws.ToString(g.Metrics["UnblendedCost"].Amount))
			}
		}
	}

	if !found {
		t.Fatalf("no EC2 compute group in %+v", out.ResultsByTime[0].Groups)
	}
}

func TestSDKGetCostForecast(t *testing.T) {
	ctx := context.Background()
	c := newCostExplorerClient(t)

	out, err := c.GetCostForecast(ctx, &ce.GetCostForecastInput{
		Granularity: cetypes.GranularityMonthly,
		Metric:      cetypes.MetricUnblendedCost,
		TimePeriod:  &cetypes.DateInterval{Start: aws.String("2024-01-01"), End: aws.String("2024-04-01")},
	})
	if err != nil {
		t.Fatalf("GetCostForecast: %v", err)
	}

	if out.Total == nil || amount(t, out.Total.Amount) <= 0 {
		t.Fatalf("forecast Total = %v, want >0", out.Total)
	}

	if len(out.ForecastResultsByTime) != 3 {
		t.Fatalf("ForecastResultsByTime = %d, want 3 months", len(out.ForecastResultsByTime))
	}
}

func TestSDKGetDimensionValuesService(t *testing.T) {
	ctx := context.Background()
	c := newCostExplorerClient(t)

	out, err := c.GetDimensionValues(ctx, &ce.GetDimensionValuesInput{
		Dimension:  cetypes.DimensionService,
		TimePeriod: &cetypes.DateInterval{Start: aws.String("2024-01-01"), End: aws.String("2024-02-01")},
	})
	if err != nil {
		t.Fatalf("GetDimensionValues: %v", err)
	}

	var found bool

	for _, dv := range out.DimensionValues {
		if aws.ToString(dv.Value) == "Amazon Elastic Compute Cloud - Compute" {
			found = true
		}
	}

	if !found {
		t.Fatalf("SERVICE dimension values %+v missing EC2 compute", out.DimensionValues)
	}
}
