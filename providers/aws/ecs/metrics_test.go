package ecs

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/cloudwatch"
	"github.com/stackshy/cloudemu/v2/services/ecs/driver"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

const (
	insightsNS = "ECS/ContainerInsights"
	awsECSNS   = "AWS/ECS"
)

// metricsMock builds an ECS mock wired to a real CloudWatch mock on one FakeClock.
func metricsMock(t *testing.T) (*Mock, *cloudwatch.Mock, *config.FakeClock) {
	t.Helper()

	clock := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(clock), config.WithRegion("us-east-1"))
	cw := cloudwatch.New(opts)
	m := New(opts)
	m.SetMonitoring(cw)

	return m, cw, clock
}

// lastValue returns the most recent value of a metric series, or false when
// the series has no data.
func lastValue(t *testing.T, cw *cloudwatch.Mock, clock *config.FakeClock, ns, name string, dims map[string]string) (float64, bool) {
	t.Helper()

	res, err := cw.GetMetricData(context.Background(), mondriver.GetMetricInput{
		Namespace: ns, MetricName: name, Dimensions: dims,
		StartTime: clock.Now().Add(-24 * time.Hour), EndTime: clock.Now().Add(time.Hour), Period: 60, Stat: "Average",
	})
	require.NoError(t, err)

	if len(res.Values) == 0 {
		return 0, false
	}

	return res.Values[len(res.Values)-1], true
}

func metricNames(t *testing.T, cw *cloudwatch.Mock, ns string) []string {
	t.Helper()

	names, err := cw.ListMetrics(context.Background(), ns)
	require.NoError(t, err)

	return names
}

func insightsCluster(t *testing.T, m *Mock, name, value string) {
	t.Helper()

	_, err := m.CreateCluster(context.Background(), driver.CreateClusterInput{
		Name: name, Settings: []driver.Setting{{Name: "containerInsights", Value: value}},
	})
	require.NoError(t, err)
}

func TestContainerInsights_ClusterAndServiceCounts(t *testing.T) {
	m, cw, clock := metricsMock(t)
	ctx := context.Background()

	insightsCluster(t, m, "prod", "enabled")
	registerWeb(t, m, 128, 256)
	m.SeedContainerInstance("prod", "i-1", WithCapacity(2048, 4096))

	clock.Advance(time.Minute)

	_, err := m.CreateService(ctx, driver.CreateServiceInput{ServiceName: "web", Cluster: "prod", TaskDefinition: "web", DesiredCount: 2})
	require.NoError(t, err)

	cluster := map[string]string{"ClusterName": "prod"}
	service := map[string]string{"ClusterName": "prod", "ServiceName": "web"}

	check := func(ns, name string, dims map[string]string, want float64) {
		t.Helper()

		got, ok := lastValue(t, cw, clock, ns, name, dims)
		require.True(t, ok, "%s has data", name)
		assert.InDelta(t, want, got, 0.001, name)
	}

	check(insightsNS, "ContainerInstanceCount", cluster, 1)
	check(insightsNS, "ServiceCount", cluster, 1)
	check(insightsNS, "TaskCount", cluster, 2)
	check(insightsNS, "DesiredTaskCount", service, 2)
	check(insightsNS, "RunningTaskCount", service, 2)
	check(insightsNS, "PendingTaskCount", service, 0)
	check(insightsNS, "DeploymentCount", service, 1)
	check(insightsNS, "TaskSetCount", service, 0)

	clock.Advance(time.Minute)

	one := 1
	_, err = m.UpdateService(ctx, driver.UpdateServiceInput{Service: "web", Cluster: "prod", DesiredCount: &one})
	require.NoError(t, err)

	check(insightsNS, "DesiredTaskCount", service, 1)
	check(insightsNS, "RunningTaskCount", service, 1)
	check(insightsNS, "TaskCount", cluster, 1)

	clock.Advance(time.Minute)

	_, err = m.DeleteService(ctx, "prod", "web", true)
	require.NoError(t, err)

	check(insightsNS, "ServiceCount", cluster, 0)
	check(insightsNS, "TaskCount", cluster, 0)
}

func TestContainerInsights_TaskSetCount(t *testing.T) {
	m, cw, clock := metricsMock(t)
	ctx := context.Background()

	insightsCluster(t, m, "prod", "enhanced")
	netCfg := fargateFixtureNoCluster(t, m)

	_, err := m.CreateService(ctx, driver.CreateServiceInput{
		ServiceName: "ext", Cluster: "prod", DeploymentController: deployControllerExternal, DesiredCount: 2,
	})
	require.NoError(t, err)

	clock.Advance(time.Minute)

	_, err = m.CreateTaskSet(ctx, driver.CreateTaskSetInput{
		Cluster: "prod", Service: "ext", TaskDefinition: "fg", LaunchType: launchFargate, NetworkConfiguration: netCfg,
	})
	require.NoError(t, err)

	got, ok := lastValue(t, cw, clock, insightsNS, "TaskSetCount", map[string]string{"ClusterName": "prod", "ServiceName": "ext"})
	require.True(t, ok)
	assert.InDelta(t, 1, got, 0.001)
}

func TestContainerInsights_OnlyWhenEnabled(t *testing.T) {
	m, cw, clock := metricsMock(t)
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, driver.CreateClusterInput{Name: "prod"})
	require.NoError(t, err)
	registerWeb(t, m, 128, 256)
	m.SeedContainerInstance("prod", "i-1", WithCapacity(2048, 4096))

	_, _, err = m.RunTask(ctx, driver.RunTaskInput{Cluster: "prod", TaskDefinition: "web"})
	require.NoError(t, err)
	assert.Empty(t, metricNames(t, cw, insightsNS), "no cluster setting and no account setting: Container Insights is off")

	// Turning it on publishes immediately.
	clock.Advance(time.Minute)

	_, err = m.UpdateClusterSettings(ctx, "prod", []driver.Setting{{Name: "containerInsights", Value: "enabled"}})
	require.NoError(t, err)

	got, ok := lastValue(t, cw, clock, insightsNS, "TaskCount", map[string]string{"ClusterName": "prod"})
	require.True(t, ok)
	assert.InDelta(t, 1, got, 0.001)

	// Turning it off stops publishing.
	clock.Advance(time.Minute)

	_, err = m.UpdateClusterSettings(ctx, "prod", []driver.Setting{{Name: "containerInsights", Value: "disabled"}})
	require.NoError(t, err)

	clock.Advance(time.Minute)

	_, _, err = m.RunTask(ctx, driver.RunTaskInput{Cluster: "prod", TaskDefinition: "web"})
	require.NoError(t, err)

	got, ok = lastValue(t, cw, clock, insightsNS, "TaskCount", map[string]string{"ClusterName": "prod"})
	require.True(t, ok)
	assert.InDelta(t, 1, got, 0.001, "the series stopped at the last enabled sample")

	// The account setting turns it on for clusters that set nothing themselves.
	_, err = m.CreateCluster(ctx, driver.CreateClusterInput{Name: "inherit"})
	require.NoError(t, err)
	_, err = m.PutAccountSetting(ctx, "containerInsights", "enabled")
	require.NoError(t, err)

	clock.Advance(time.Minute)

	_, err = m.UpdateCluster(ctx, driver.UpdateClusterInput{Cluster: "inherit"})
	require.NoError(t, err)

	_, ok = lastValue(t, cw, clock, insightsNS, "ServiceCount", map[string]string{"ClusterName": "inherit"})
	assert.True(t, ok, "account-level containerInsights applies to a cluster with no setting of its own")
}

func TestAWSECS_ReservationAndLiveTaskCount(t *testing.T) {
	m, cw, clock := metricsMock(t)
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, driver.CreateClusterInput{Name: "prod"})
	require.NoError(t, err)

	_, err = m.RegisterTaskDefinition(ctx, driver.RegisterTaskDefinitionInput{
		Family: "web", CPU: "250", Memory: "500", ContainerDefinitions: []driver.ContainerDefinition{{Name: "c", Image: "img"}},
	})
	require.NoError(t, err)
	m.SeedContainerInstance("prod", "i-1", WithCapacity(1000, 2000))

	clock.Advance(time.Minute)

	_, err = m.CreateService(ctx, driver.CreateServiceInput{ServiceName: "web", Cluster: "prod", TaskDefinition: "web", DesiredCount: 1})
	require.NoError(t, err)

	cpu, ok := lastValue(t, cw, clock, awsECSNS, "CPUReservation", map[string]string{"ClusterName": "prod"})
	require.True(t, ok)
	assert.InDelta(t, 25, cpu, 0.001)

	mem, ok := lastValue(t, cw, clock, awsECSNS, "MemoryReservation", map[string]string{"ClusterName": "prod"})
	require.True(t, ok)
	assert.InDelta(t, 25, mem, 0.001)

	live, ok := lastValue(t, cw, clock, awsECSNS, "LiveTaskCount", map[string]string{"ClusterName": "prod", "ServiceName": "web"})
	require.True(t, ok)
	assert.InDelta(t, 1, live, 0.001)

	for _, name := range metricNames(t, cw, awsECSNS) {
		assert.NotContains(t, []string{"CPUUtilization", "MemoryUtilization"}, name,
			"no workload runs, so no utilization is fabricated")
	}
}

func TestMetrics_EmptyClusterAndNoReservation(t *testing.T) {
	m, cw, clock := metricsMock(t)

	insightsCluster(t, m, "prod", "enabled")

	for _, name := range []string{"ContainerInstanceCount", "ServiceCount", "TaskCount"} {
		got, ok := lastValue(t, cw, clock, insightsNS, name, map[string]string{"ClusterName": "prod"})
		require.True(t, ok, name)
		assert.InDelta(t, 0, got, 0.001, name)
	}

	assert.NotContains(t, metricNames(t, cw, awsECSNS), "CPUReservation", "no instances, no reservation series")
}

// fargateFixtureNoCluster registers the Fargate task definition (the cluster
// already exists) and returns the awsvpc network configuration.
func fargateFixtureNoCluster(t *testing.T, m *Mock) *driver.NetworkConfiguration {
	t.Helper()

	_, err := m.RegisterTaskDefinition(context.Background(), driver.RegisterTaskDefinitionInput{
		Family:                  "fg",
		ContainerDefinitions:    []driver.ContainerDefinition{{Name: "c", Image: "img", Essential: true}},
		CPU:                     "256",
		Memory:                  "512",
		NetworkMode:             networkModeAwsvpc,
		RequiresCompatibilities: []string{launchFargate},
	})
	require.NoError(t, err)

	return &driver.NetworkConfiguration{AwsVpcConfiguration: &driver.AwsVpcConfiguration{Subnets: []string{"subnet-1"}}}
}

// TestMetrics_ConcurrentCreateServices creates services of one cluster in
// parallel with metrics wired: the metric publisher reads every service of the
// cluster while other requests are still converging theirs (a -race regression
// test for the stored name claim being mutated in place).
func TestMetrics_ConcurrentCreateServices(t *testing.T) {
	m, _, _ := metricsMock(t)
	ctx := context.Background()

	insightsCluster(t, m, "prod", "enabled")
	netCfg := fargateFixtureNoCluster(t, m)

	var wg sync.WaitGroup

	for i := range 20 {
		wg.Add(1)

		go func() {
			defer wg.Done()

			_, err := m.CreateService(ctx, driver.CreateServiceInput{
				ServiceName: "svc" + string(rune('a'+i)), Cluster: "prod", TaskDefinition: "fg", LaunchType: launchFargate,
				NetworkConfiguration: netCfg, DesiredCount: 2,
			})
			assert.NoError(t, err)
		}()
	}

	wg.Wait()

	services, err := m.ListServices(ctx, "prod")
	require.NoError(t, err)
	assert.Len(t, services, 20)
}
