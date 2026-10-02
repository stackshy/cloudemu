package apigateway_test

import (
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/apigateway"
	"github.com/stackshy/cloudemu/v2/providers/aws/cloudwatch"
	"github.com/stackshy/cloudemu/v2/services/apigateway/driver"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// TestInvokeRouteRequestMetrics pins the AWS/ApiGateway request metrics a
// data-plane request publishes: Count, 4XXError, 5XXError (Count) and Latency /
// IntegrationLatency (Milliseconds), on both {ApiName} and {ApiName, Stage}.
func TestInvokeRouteRequestMetrics(t *testing.T) {
	clk := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(clk), config.WithRegion("us-east-1"), config.WithAccountID("000000000000"))
	m := apigateway.New(opts)
	cw := cloudwatch.New(opts)
	m.SetMonitoring(cw)

	inv := &fakeInvoker{output: []byte(`{"statusCode":200,"body":"ok"}`)}
	m.SetLambdaInvoker(inv)

	apiID, _, _ := deployProxyAPI(t, m, "hello", "GET", lambdaURI)

	send := func(path string) {
		t.Helper()

		if _, err := m.InvokeRoute(ctx(), &driver.ProxyRequest{
			RestAPIID: apiID, StageName: "prod", HTTPMethod: "GET", Path: path,
		}); err != nil {
			t.Fatalf("InvokeRoute: %v", err)
		}
	}

	send("/hello") // 200 from the Lambda backend
	send("/nope")  // 403 Missing Authentication Token (no route)

	inv.fnErr = "Unhandled"
	send("/hello") // 502: the function raised

	// A request to an unknown stage belongs to no deployed stage: no metrics.
	if _, err := m.InvokeRoute(ctx(), &driver.ProxyRequest{
		RestAPIID: apiID, StageName: "missing", HTTPMethod: "GET", Path: "/hello",
	}); err != nil {
		t.Fatalf("InvokeRoute: %v", err)
	}

	for _, dims := range []map[string]string{
		{"ApiName": "petstore"},
		{"ApiName": "petstore", "Stage": "prod"},
	} {
		checks := []struct {
			name, stat string
			want       float64
			unit       string
		}{
			{"Count", "SampleCount", 3, "Count"},
			{"4XXError", "Sum", 1, "Count"},
			{"5XXError", "Sum", 1, "Count"},
			{"Latency", "SampleCount", 3, "Milliseconds"},
			{"IntegrationLatency", "SampleCount", 2, "Milliseconds"},
		}

		for _, c := range checks {
			res, err := cw.GetMetricData(ctx(), mondriver.GetMetricInput{
				Namespace: "AWS/ApiGateway", MetricName: c.name, Dimensions: dims,
				StartTime: clk.Now().Add(-time.Minute), EndTime: clk.Now().Add(time.Minute),
				Period: 60, Stat: c.stat,
			})
			if err != nil {
				t.Fatalf("GetMetricData %s: %v", c.name, err)
			}

			if len(res.Values) != 1 || res.Values[0] != c.want || res.Unit != c.unit {
				t.Errorf("%s %v %s = %v %q, want %v %q", c.name, dims, c.stat, res.Values, res.Unit, c.want, c.unit)
			}
		}
	}
}
