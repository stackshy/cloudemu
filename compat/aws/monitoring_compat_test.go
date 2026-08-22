package aws

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/internal/compat"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

const (
	monitoringService = "monitoring"
	metricNamespace   = "MyApp"
	metricName        = "RequestCount"
	metricValue       = 42.0
	alarmName         = "HighCPU"
	alarmNamespace    = "AWS/EC2"
	alarmMetricName   = "CPUUtilization"
	alarmThreshold    = 80.0
	alarmPeriodSec    = 60
	alarmEvalPeriods  = 2
	statsWindowHours  = 1
)

// TestCloudWatchCompat drives a real aws-sdk-go-v2 CloudWatch client against
// CloudEmu's in-process wire server and records one compat result per portable
// monitoring operation the server routes.
func TestCloudWatchCompat(t *testing.T) {
	provider := cloudemu.NewAWS()
	sess := compat.BootAWS(t, awsserver.Drivers{CloudWatch: provider.CloudWatch})

	cw := cloudwatch.NewFromConfig(sess.Config(), func(o *cloudwatch.Options) {
		o.BaseEndpoint = aws.String(sess.Endpoint())
	})
	ctx := context.Background()

	sess.Op(monitoringService, "PutMetricData", func() error {
		_, err := cw.PutMetricData(ctx, &cloudwatch.PutMetricDataInput{
			Namespace: aws.String(metricNamespace),
			MetricData: []cwtypes.MetricDatum{{
				MetricName: aws.String(metricName),
				Value:      aws.Float64(metricValue),
				Unit:       cwtypes.StandardUnitCount,
			}},
		})

		return err
	})

	sess.Op(monitoringService, "ListMetrics", func() error {
		_, err := cw.ListMetrics(ctx, &cloudwatch.ListMetricsInput{
			Namespace: aws.String(metricNamespace),
		})

		return err
	})

	sess.Op(monitoringService, "GetMetricData", func() error {
		end := time.Now().UTC()
		_, err := cw.GetMetricStatistics(ctx, &cloudwatch.GetMetricStatisticsInput{
			Namespace:  aws.String(metricNamespace),
			MetricName: aws.String(metricName),
			StartTime:  aws.Time(end.Add(-statsWindowHours * time.Hour)),
			EndTime:    aws.Time(end),
			Period:     aws.Int32(alarmPeriodSec),
			Statistics: []cwtypes.Statistic{cwtypes.StatisticAverage},
		})

		return err
	})

	sess.Op(monitoringService, "CreateAlarm", func() error {
		_, err := cw.PutMetricAlarm(ctx, &cloudwatch.PutMetricAlarmInput{
			AlarmName:          aws.String(alarmName),
			Namespace:          aws.String(alarmNamespace),
			MetricName:         aws.String(alarmMetricName),
			ComparisonOperator: cwtypes.ComparisonOperatorGreaterThanThreshold,
			Threshold:          aws.Float64(alarmThreshold),
			Period:             aws.Int32(alarmPeriodSec),
			EvaluationPeriods:  aws.Int32(alarmEvalPeriods),
			Statistic:          cwtypes.StatisticAverage,
		})

		return err
	})

	sess.Op(monitoringService, "DescribeAlarms", func() error {
		_, err := cw.DescribeAlarms(ctx, &cloudwatch.DescribeAlarmsInput{
			AlarmNames: []string{alarmName},
		})

		return err
	})

	sess.Op(monitoringService, "DeleteAlarm", func() error {
		_, err := cw.DeleteAlarms(ctx, &cloudwatch.DeleteAlarmsInput{
			AlarmNames: []string{alarmName},
		})

		return err
	})
}
