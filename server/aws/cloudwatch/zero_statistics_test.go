package cloudwatch_test

import (
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscw "github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"

	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

func allStatistics() []cwtypes.Statistic {
	return []cwtypes.Statistic{
		cwtypes.StatisticSampleCount, cwtypes.StatisticAverage, cwtypes.StatisticSum,
		cwtypes.StatisticMinimum, cwtypes.StatisticMaximum,
	}
}

func statsRequest(ns, name string, d []cwtypes.Dimension, stats []cwtypes.Statistic, now time.Time) *awscw.GetMetricStatisticsInput {
	return &awscw.GetMetricStatisticsInput{
		Namespace: aws.String(ns), MetricName: aws.String(name), Dimensions: d,
		StartTime: aws.Time(now.Add(-time.Hour)), EndTime: aws.Time(now.Add(time.Hour)),
		Period: aws.Int32(60), Statistics: stats,
	}
}

func requireStat(t *testing.T, name string, got *float64, want float64) {
	t.Helper()

	if got == nil {
		t.Fatalf("%s is absent, want %v", name, want)
	}

	if *got != want {
		t.Fatalf("%s = %v, want %v", name, *got, want)
	}
}

func requireAbsent(t *testing.T, name string, got *float64) {
	t.Helper()

	if got != nil {
		t.Fatalf("%s = %v, want absent because it was not requested", name, *got)
	}
}

// TestGetMetricStatisticsZeroValues checks that a requested statistic of 0 is
// still returned. Both codecs used to drop it through float64 omitempty.
func TestGetMetricStatisticsZeroValues(t *testing.T) {
	for _, p := range cwProtocols() {
		t.Run(p.name+"/all statistics", func(t *testing.T) {
			w := p.build(t, nil)
			now := putZero(t, w)

			out := w.getStats(t, statsRequest("T/App", "B", dims("Env", "dev"), allStatistics(), now))
			if len(out.Datapoints) != 1 {
				t.Fatalf("datapoints = %d, want 1", len(out.Datapoints))
			}

			dp := out.Datapoints[0]
			requireStat(t, "Sum", dp.Sum, 0)
			requireStat(t, "Average", dp.Average, 0)
			requireStat(t, "Minimum", dp.Minimum, 0)
			requireStat(t, "Maximum", dp.Maximum, 0)
			requireStat(t, "SampleCount", dp.SampleCount, 1)
		})

		t.Run(p.name+"/sum only", func(t *testing.T) {
			w := p.build(t, nil)
			now := putZero(t, w)

			out := w.getStats(t, statsRequest("T/App", "B", dims("Env", "dev"), []cwtypes.Statistic{cwtypes.StatisticSum}, now))
			if len(out.Datapoints) != 1 {
				t.Fatalf("datapoints = %d, want 1", len(out.Datapoints))
			}

			dp := out.Datapoints[0]
			requireStat(t, "Sum", dp.Sum, 0)
			requireAbsent(t, "Average", dp.Average)
			requireAbsent(t, "Minimum", dp.Minimum)
			requireAbsent(t, "Maximum", dp.Maximum)
			requireAbsent(t, "SampleCount", dp.SampleCount)
		})
	}
}

func putZero(t *testing.T, w cwWire) time.Time {
	t.Helper()

	now := time.Now().UTC()
	w.put(t, &awscw.PutMetricDataInput{
		Namespace: aws.String("T/App"),
		MetricData: []cwtypes.MetricDatum{{
			MetricName: aws.String("B"), Value: aws.Float64(0), Timestamp: aws.Time(now), Dimensions: dims("Env", "dev"),
		}},
	})

	return now
}

// TestGetMetricStatisticsIPAMParity checks the derived AWS/IPAM datapoint on
// both protocols. The query path had no IPAM branch and returned nothing.
func TestGetMetricStatisticsIPAMParity(t *testing.T) {
	ipam := fakeIPAM{
		{Namespace: netdriver.IpamMetricNamespace, MetricName: "VpcIPUsage", Value: 7, Unit: "Percent",
			Dimensions: map[string]string{"IpamId": "ipam-1"}},
		{Namespace: netdriver.IpamMetricNamespace, MetricName: "FreeIPs", Value: 0, Unit: "Count",
			Dimensions: map[string]string{"IpamId": "ipam-1"}},
	}

	stats := []cwtypes.Statistic{cwtypes.StatisticSum, cwtypes.StatisticMaximum}

	for _, p := range cwProtocols() {
		t.Run(p.name, func(t *testing.T) {
			w := p.build(t, ipam)
			now := time.Now().UTC()

			out := w.getStats(t, statsRequest("AWS/IPAM", "VpcIPUsage", dims("IpamId", "ipam-1"), stats, now))
			if len(out.Datapoints) != 1 {
				t.Fatalf("datapoints = %d, want 1", len(out.Datapoints))
			}

			dp := out.Datapoints[0]
			requireStat(t, "Sum", dp.Sum, 7)
			requireStat(t, "Maximum", dp.Maximum, 7)
			requireAbsent(t, "Average", dp.Average)

			if dp.Unit != cwtypes.StandardUnitPercent {
				t.Fatalf("unit = %q, want Percent", dp.Unit)
			}

			free := w.getStats(t, statsRequest("AWS/IPAM", "FreeIPs", nil, stats, now))
			if len(free.Datapoints) != 1 {
				t.Fatalf("FreeIPs datapoints = %d, want 1", len(free.Datapoints))
			}

			requireStat(t, "FreeIPs Sum", free.Datapoints[0].Sum, 0)
		})
	}
}
