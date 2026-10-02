package route53_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsr53 "github.com/aws/aws-sdk-go-v2/service/route53"
	r53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
)

func getHealthCheckConfig(t *testing.T, client *awsr53.Client, id *string) *r53types.HealthCheckConfig {
	t.Helper()

	out, err := client.GetHealthCheck(context.Background(), &awsr53.GetHealthCheckInput{HealthCheckId: id})
	if err != nil {
		t.Fatalf("GetHealthCheck: %v", err)
	}

	return out.HealthCheck.HealthCheckConfig
}

// TestSDKCalculatedHealthCheckFieldsRoundTrip checks a CALCULATED check stores
// and returns its child checks, threshold and inversion, on create and update.
func TestSDKCalculatedHealthCheckFieldsRoundTrip(t *testing.T) {
	client := newRoute53Client(t)
	ctx := context.Background()

	out, err := client.CreateHealthCheck(ctx, &awsr53.CreateHealthCheckInput{
		CallerReference: aws.String("calc-fields"),
		HealthCheckConfig: &r53types.HealthCheckConfig{
			Type:              r53types.HealthCheckTypeCalculated,
			HealthThreshold:   aws.Int32(2),
			ChildHealthChecks: []string{"child-a", "child-b"},
			Inverted:          aws.Bool(true),
		},
	})
	if err != nil {
		t.Fatalf("CreateHealthCheck: %v", err)
	}

	cfg := getHealthCheckConfig(t, client, out.HealthCheck.Id)
	if aws.ToInt32(cfg.HealthThreshold) != 2 || len(cfg.ChildHealthChecks) != 2 || !aws.ToBool(cfg.Inverted) {
		t.Fatalf("after create: threshold=%d children=%v inverted=%v",
			aws.ToInt32(cfg.HealthThreshold), cfg.ChildHealthChecks, aws.ToBool(cfg.Inverted))
	}

	if _, err := client.UpdateHealthCheck(ctx, &awsr53.UpdateHealthCheckInput{
		HealthCheckId:     out.HealthCheck.Id,
		HealthThreshold:   aws.Int32(1),
		ChildHealthChecks: []string{"child-a"},
		Inverted:          aws.Bool(false),
	}); err != nil {
		t.Fatalf("UpdateHealthCheck: %v", err)
	}

	cfg = getHealthCheckConfig(t, client, out.HealthCheck.Id)
	if aws.ToInt32(cfg.HealthThreshold) != 1 || len(cfg.ChildHealthChecks) != 1 || aws.ToBool(cfg.Inverted) {
		t.Fatalf("after update: threshold=%d children=%v inverted=%v",
			aws.ToInt32(cfg.HealthThreshold), cfg.ChildHealthChecks, aws.ToBool(cfg.Inverted))
	}
}

// TestSDKCloudWatchMetricHealthCheckFieldsRoundTrip checks a CLOUDWATCH_METRIC
// check stores its alarm and insufficient-data status, and needs an alarm.
func TestSDKCloudWatchMetricHealthCheckFieldsRoundTrip(t *testing.T) {
	client := newRoute53Client(t)
	ctx := context.Background()

	_, err := client.CreateHealthCheck(ctx, &awsr53.CreateHealthCheckInput{
		CallerReference:   aws.String("cw-no-alarm"),
		HealthCheckConfig: &r53types.HealthCheckConfig{Type: r53types.HealthCheckTypeCloudwatchMetric},
	})

	var badInput *r53types.InvalidInput
	if !errors.As(err, &badInput) {
		t.Fatalf("CLOUDWATCH_METRIC without alarm: got %v, want InvalidInput", err)
	}

	out, err := client.CreateHealthCheck(ctx, &awsr53.CreateHealthCheckInput{
		CallerReference: aws.String("cw-alarm"),
		HealthCheckConfig: &r53types.HealthCheckConfig{
			Type:                         r53types.HealthCheckTypeCloudwatchMetric,
			AlarmIdentifier:              &r53types.AlarmIdentifier{Region: r53types.CloudWatchRegionUsEast1, Name: aws.String("cpu")},
			InsufficientDataHealthStatus: r53types.InsufficientDataHealthStatusLastKnownStatus,
		},
	})
	if err != nil {
		t.Fatalf("CreateHealthCheck: %v", err)
	}

	if _, err := client.UpdateHealthCheck(ctx, &awsr53.UpdateHealthCheckInput{
		HealthCheckId:                out.HealthCheck.Id,
		InsufficientDataHealthStatus: r53types.InsufficientDataHealthStatusUnhealthy,
	}); err != nil {
		t.Fatalf("UpdateHealthCheck: %v", err)
	}

	cfg := getHealthCheckConfig(t, client, out.HealthCheck.Id)
	if cfg.AlarmIdentifier == nil || aws.ToString(cfg.AlarmIdentifier.Name) != "cpu" ||
		cfg.AlarmIdentifier.Region != r53types.CloudWatchRegionUsEast1 {
		t.Fatalf("alarm identifier = %+v", cfg.AlarmIdentifier)
	}

	if cfg.InsufficientDataHealthStatus != r53types.InsufficientDataHealthStatusUnhealthy {
		t.Fatalf("insufficient data status = %q, want Unhealthy", cfg.InsufficientDataHealthStatus)
	}
}

// TestSDKStringMatchHealthCheckSearchString checks STR_MATCH checks need a
// search string of at most 255 characters, and that it round-trips.
func TestSDKStringMatchHealthCheckSearchString(t *testing.T) {
	client := newRoute53Client(t)
	ctx := context.Background()

	create := func(ref, search string) (*awsr53.CreateHealthCheckOutput, error) {
		cfg := &r53types.HealthCheckConfig{
			Type: r53types.HealthCheckTypeHttpStrMatch, FullyQualifiedDomainName: aws.String("example.com"),
		}
		if search != "" {
			cfg.SearchString = aws.String(search)
		}

		return client.CreateHealthCheck(ctx, &awsr53.CreateHealthCheckInput{
			CallerReference: aws.String(ref), HealthCheckConfig: cfg,
		})
	}

	for _, bad := range []string{"", strings.Repeat("a", 256)} {
		_, err := create("str-bad-"+bad[:min(len(bad), 1)], bad)

		var badInput *r53types.InvalidInput
		if !errors.As(err, &badInput) {
			t.Fatalf("search string of %d chars: got %v, want InvalidInput", len(bad), err)
		}
	}

	out, err := create("str-ok", "healthy")
	if err != nil {
		t.Fatalf("CreateHealthCheck: %v", err)
	}

	if got := aws.ToString(getHealthCheckConfig(t, client, out.HealthCheck.Id).SearchString); got != "healthy" {
		t.Fatalf("search string = %q, want healthy", got)
	}

	if _, err := client.UpdateHealthCheck(ctx, &awsr53.UpdateHealthCheckInput{
		HealthCheckId: out.HealthCheck.Id, SearchString: aws.String("ready"),
	}); err != nil {
		t.Fatalf("UpdateHealthCheck: %v", err)
	}

	if got := aws.ToString(getHealthCheckConfig(t, client, out.HealthCheck.Id).SearchString); got != "ready" {
		t.Fatalf("search string after update = %q, want ready", got)
	}
}
