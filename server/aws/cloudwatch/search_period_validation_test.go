package cloudwatch_test

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscw "github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/aws/smithy-go"
)

// searchPeriodForm is a GetMetricData query with one expression entry.
func searchPeriodForm(expr string) url.Values {
	return url.Values{
		"Action":                                {"GetMetricData"},
		"StartTime":                             {time.Now().Add(-20 * time.Minute).UTC().Format(time.RFC3339)},
		"EndTime":                               {time.Now().UTC().Format(time.RFC3339)},
		"MetricDataQueries.member.1.Id":         {"e1"},
		"MetricDataQueries.member.1.Expression": {expr},
	}
}

// TestGetMetricDataSearchPeriodValidation is the hang from the review: a
// SEARCH period past the Duration range sent FILL into an endless loop, and a
// period of 2^55 divided by zero. The SEARCH period now follows the MetricStat
// Period rule, and a bad one is a ValidationError. The poster's client timeout
// turns a hang into a failure.
func TestGetMetricDataSearchPeriodValidation(t *testing.T) {
	post := newPeriodPoster(t)

	for _, p := range []string{"9223372037", "36028797018963968", "9223372036854775808", "61", "15", "0"} {
		t.Run("invalid "+p, func(t *testing.T) {
			code, body := post(searchPeriodForm("FILL(SEARCH('{App,InstanceId}','Sum'," + p + "),0)"))
			if code != http.StatusBadRequest || !strings.Contains(body, "<Code>ValidationError</Code>") ||
				!strings.Contains(body, "Error in expression &#39;e1&#39;: Invalid SEARCH period.") {
				t.Fatalf("want 400 ValidationError, got code=%d body=%s", code, body)
			}
		})
	}

	for _, p := range []string{"1", "60", "300"} {
		t.Run("valid "+p, func(t *testing.T) {
			if code, body := post(searchPeriodForm("FILL(SEARCH('{App,InstanceId}','Sum'," + p + "),0)")); code != http.StatusOK {
				t.Fatalf("code=%d body=%s", code, body)
			}
		})
	}
}

// TestSDKGetMetricDataSearchPeriodValidation runs the same check through the
// Go SDK, along with FILL over an entry Period that would overflow the step.
func TestSDKGetMetricDataSearchPeriodValidation(t *testing.T) {
	client, ctx := newCWClient(t)

	ctx, cancel := context.WithTimeout(ctx, periodRequestTimeout)
	defer cancel()

	start := time.Now().UTC().Truncate(time.Minute).Add(-20 * time.Minute)
	putPoint(ctx, t, client, "Rev/App", "Hits", map[string]string{"InstanceId": "i-1"}, 4, start.Add(5*time.Minute))

	get := func(queries ...cwtypes.MetricDataQuery) error {
		_, err := client.GetMetricData(ctx, &awscw.GetMetricDataInput{
			StartTime: aws.Time(start), EndTime: aws.Time(start.Add(20 * time.Minute)), MetricDataQueries: queries,
		})

		return err
	}

	err := get(cwtypes.MetricDataQuery{
		Id: aws.String("e1"), Expression: aws.String("FILL(SEARCH('{Rev/App,InstanceId}','Sum',9223372037),0)"),
	})

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "ValidationError" ||
		!strings.Contains(apiErr.ErrorMessage(), "Invalid SEARCH period") {
		t.Fatalf("want ValidationError, got %v", err)
	}

	m1 := metricQuery("m1", "Rev/App", "Hits")
	m1.MetricStat.Metric.Dimensions = []cwtypes.Dimension{{Name: aws.String("InstanceId"), Value: aws.String("i-1")}}

	err = get(m1, cwtypes.MetricDataQuery{
		Id: aws.String("e1"), Expression: aws.String("FILL(m1,0)"), Period: aws.Int32(-2147483648),
	})
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() == "" {
		t.Fatalf("want an API error for a negative Period, got %v", err)
	}

	if err := get(cwtypes.MetricDataQuery{
		Id: aws.String("e1"), Expression: aws.String("FILL(SEARCH('{Rev/App,InstanceId}','Sum',300),0)"),
	}); err != nil {
		t.Fatalf("valid SEARCH period: %v", err)
	}
}
