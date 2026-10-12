package ecs_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	awsecs "github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// TestMetrics_VisibleThroughCloudWatch drives ECS and CloudWatch over the wire
// through the full AWS server: ECS metrics land in the same CloudWatch a user
// queries.
func TestMetrics_VisibleThroughCloudWatch(t *testing.T) {
	cloud := cloudemu.NewAWS()
	srv := awsserver.NewFromProvider(cloud)

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatal(err)
	}

	ecsClient := awsecs.NewFromConfig(cfg, func(o *awsecs.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	cw := cloudwatch.NewFromConfig(cfg, func(o *cloudwatch.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	ctx := context.Background()

	if _, err := ecsClient.CreateCluster(ctx, &awsecs.CreateClusterInput{
		ClusterName: aws.String("prod"),
		Settings: []ecstypes.ClusterSetting{{
			Name: ecstypes.ClusterSettingNameContainerInsights, Value: aws.String("enabled"),
		}},
	}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	netCfg := fargateSetup(t, ecsClient)
	if _, err := ecsClient.CreateService(ctx, &awsecs.CreateServiceInput{
		Cluster: aws.String("prod"), ServiceName: aws.String("web"), TaskDefinition: aws.String("fg"),
		LaunchType: ecstypes.LaunchTypeFargate, NetworkConfiguration: netCfg, DesiredCount: aws.Int32(2),
	}); err != nil {
		t.Fatalf("CreateService: %v", err)
	}

	insights, err := cw.ListMetrics(ctx, &cloudwatch.ListMetricsInput{Namespace: aws.String("ECS/ContainerInsights")})
	if err != nil {
		t.Fatalf("ListMetrics: %v", err)
	}

	have := map[string]bool{}
	for _, m := range insights.Metrics {
		have[aws.ToString(m.MetricName)] = true
	}

	for _, want := range []string{"RunningTaskCount", "DesiredTaskCount", "TaskCount", "ServiceCount"} {
		if !have[want] {
			t.Fatalf("ECS/ContainerInsights is missing %s: %v", want, have)
		}
	}

	stats, err := cw.GetMetricStatistics(ctx, &cloudwatch.GetMetricStatisticsInput{
		Namespace: aws.String("ECS/ContainerInsights"), MetricName: aws.String("RunningTaskCount"),
		Dimensions: []cwtypes.Dimension{
			{Name: aws.String("ClusterName"), Value: aws.String("prod")},
			{Name: aws.String("ServiceName"), Value: aws.String("web")},
		},
		StartTime: aws.Time(time.Now().Add(-time.Hour)), EndTime: aws.Time(time.Now().Add(time.Hour)),
		Period: aws.Int32(60), Statistics: []cwtypes.Statistic{cwtypes.StatisticMaximum},
	})
	if err != nil || len(stats.Datapoints) == 0 || aws.ToFloat64(stats.Datapoints[0].Maximum) != 2 {
		t.Fatalf("RunningTaskCount = %+v, err %v", stats, err)
	}

	live, err := cw.ListMetrics(ctx, &cloudwatch.ListMetricsInput{Namespace: aws.String("AWS/ECS")})
	if err != nil {
		t.Fatal(err)
	}

	found := false

	for _, m := range live.Metrics {
		if aws.ToString(m.MetricName) == "LiveTaskCount" {
			found = true
		}
	}

	if !found {
		t.Fatal("AWS/ECS LiveTaskCount missing")
	}
}
