package ecs

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/services/ecs/driver"
)

// deploymentFixture creates cluster "prod", Fargate task definitions fg:1 and
// fg:2, and an ECS-controller service "web" with no tasks (desired 0) so the
// deployment history can be exercised without placing tasks.
func deploymentFixture(t *testing.T, m *Mock) *driver.NetworkConfiguration {
	t.Helper()

	netCfg := fargateFixture(t, m)

	_, err := m.RegisterTaskDefinition(context.Background(), driver.RegisterTaskDefinitionInput{
		Family:                  "fg",
		ContainerDefinitions:    []driver.ContainerDefinition{{Name: "c", Image: "img2", Essential: true}},
		CPU:                     "256",
		Memory:                  "512",
		NetworkMode:             networkModeAwsvpc,
		RequiresCompatibilities: []string{launchFargate},
	})
	require.NoError(t, err)

	_, err = m.CreateService(context.Background(), driver.CreateServiceInput{
		ServiceName: "web", Cluster: "prod", TaskDefinition: "fg:1", LaunchType: launchFargate, NetworkConfiguration: netCfg,
	})
	require.NoError(t, err)

	return netCfg
}

func listDeployments(t *testing.T, m *Mock, in driver.ListServiceDeploymentsInput) ([]driver.ServiceDeployment, string) {
	t.Helper()

	in.Cluster, in.Service = "prod", "web"

	out, next, err := m.ListServiceDeployments(context.Background(), in)
	require.NoError(t, err)

	return out, next
}

func forceDeploy(t *testing.T, m *Mock) {
	t.Helper()

	_, err := m.UpdateService(context.Background(), driver.UpdateServiceInput{
		Service: "web", Cluster: "prod", ForceNewDeployment: true,
	})
	require.NoError(t, err)
}

func TestCreateService_RecordsDeploymentAndRevision(t *testing.T) {
	m := newTestMock()
	deploymentFixture(t, m)

	deps, next := listDeployments(t, m, driver.ListServiceDeploymentsInput{})
	require.Len(t, deps, 1)
	assert.Empty(t, next)

	dep := deps[0]
	assert.Equal(t, driver.DeploymentStatusSuccessful, dep.Status)
	assert.Contains(t, dep.ARN, ":service-deployment/prod/web/")
	assert.Contains(t, dep.TargetServiceRevision.ARN, ":service-revision/prod/web/")
	assert.NotEmpty(t, dep.StartedAt)
	assert.NotEmpty(t, dep.FinishedAt)
	assert.Empty(t, dep.SourceServiceRevisions, "the first deployment has no source revision")

	revs, failures, err := m.DescribeServiceRevisions(context.Background(), []string{dep.TargetServiceRevision.ARN})
	require.NoError(t, err)
	require.Empty(t, failures)
	require.Len(t, revs, 1)
	assert.Contains(t, revs[0].TaskDefinition, "task-definition/fg:1")
	assert.Equal(t, launchFargate, revs[0].LaunchType)
	assert.Equal(t, "DISABLED", revs[0].NetworkConfiguration.AwsVpcConfiguration.AssignPublicIP)
}

func TestUpdateService_NewDeploymentOnChange(t *testing.T) {
	m := newTestMock()
	deploymentFixture(t, m)
	ctx := context.Background()

	first, _ := listDeployments(t, m, driver.ListServiceDeploymentsInput{})
	require.Len(t, first, 1)

	_, err := m.UpdateService(ctx, driver.UpdateServiceInput{Service: "web", Cluster: "prod", TaskDefinition: "fg:2"})
	require.NoError(t, err)

	deps, _ := listDeployments(t, m, driver.ListServiceDeploymentsInput{})
	require.Len(t, deps, 2, "a task-definition change is a new deployment")
	assert.NotEqual(t, first[0].ARN, deps[0].ARN)
	require.Len(t, deps[0].SourceServiceRevisions, 1)
	assert.Equal(t, first[0].TargetServiceRevision.ARN, deps[0].SourceServiceRevisions[0].ARN)

	two := 0
	_, err = m.UpdateService(ctx, driver.UpdateServiceInput{Service: "web", Cluster: "prod", DesiredCount: &two})
	require.NoError(t, err)

	deps, _ = listDeployments(t, m, driver.ListServiceDeploymentsInput{})
	assert.Len(t, deps, 2, "a desired-count-only change is not a deployment")

	forceDeploy(t, m)

	deps, _ = listDeployments(t, m, driver.ListServiceDeploymentsInput{})
	assert.Len(t, deps, 3, "force-new-deployment is a deployment")
}

func TestListServiceDeployments_FilterAndPaging(t *testing.T) {
	m := newTestMock()
	deploymentFixture(t, m)

	clock, ok := m.opts.Clock.(*config.FakeClock)
	require.True(t, ok)

	for range 4 {
		clock.Advance(time.Hour)
		forceDeploy(t, m)
	}

	all, _ := listDeployments(t, m, driver.ListServiceDeploymentsInput{})
	require.Len(t, all, 5)

	for i := 1; i < len(all); i++ {
		assert.GreaterOrEqual(t, all[i-1].CreatedAt, all[i].CreatedAt, "newest first")
	}

	var got []string

	token := ""

	for pages := 0; pages < 10; pages++ {
		page, next := listDeployments(t, m, driver.ListServiceDeploymentsInput{MaxResults: 2, NextToken: token})
		for i := range page {
			got = append(got, page[i].ARN)
		}

		if next == "" {
			break
		}

		token = next
	}

	require.Len(t, got, 5)

	for i := range all {
		assert.Equal(t, all[i].ARN, got[i], "pages resume exactly in the same order")
	}

	stopped, _ := listDeployments(t, m, driver.ListServiceDeploymentsInput{Statuses: []string{driver.DeploymentStatusStopped}})
	assert.Empty(t, stopped)

	successful, _ := listDeployments(t, m, driver.ListServiceDeploymentsInput{Statuses: []string{driver.DeploymentStatusSuccessful}})
	assert.Len(t, successful, 5)

	cutoff := all[2].CreatedAt
	after, _ := listDeployments(t, m, driver.ListServiceDeploymentsInput{CreatedAtAfter: cutoff})
	assert.Len(t, after, 2, "strictly after the cutoff")

	before, _ := listDeployments(t, m, driver.ListServiceDeploymentsInput{CreatedAtBefore: cutoff})
	assert.Len(t, before, 2, "strictly before the cutoff")

	for _, bad := range []driver.ListServiceDeploymentsInput{
		{MaxResults: 101}, {MaxResults: -1}, {NextToken: "!!not-a-token"}, {CreatedAtAfter: "yesterday"},
	} {
		bad.Cluster, bad.Service = "prod", "web"
		_, _, err := m.ListServiceDeployments(context.Background(), bad)
		require.Error(t, err, bad)
		assert.Equal(t, excInvalidParameter, ecsException(t, err), bad)
	}

	_, _, err := m.ListServiceDeployments(context.Background(), driver.ListServiceDeploymentsInput{Cluster: "ghost", Service: "web"})
	assert.Equal(t, excClusterNotFound, ecsException(t, err))

	_, _, err = m.ListServiceDeployments(context.Background(), driver.ListServiceDeploymentsInput{Cluster: "prod", Service: "ghost"})
	assert.Equal(t, excServiceNotFound, ecsException(t, err))
}

func TestDescribeServiceDeployments_AndRevisions(t *testing.T) {
	m := newTestMock()
	deploymentFixture(t, m)
	ctx := context.Background()

	_, err := m.UpdateService(ctx, driver.UpdateServiceInput{Service: "web", Cluster: "prod", TaskDefinition: "fg:2"})
	require.NoError(t, err)

	deps, _ := listDeployments(t, m, driver.ListServiceDeploymentsInput{})
	require.Len(t, deps, 2)

	got, failures, err := m.DescribeServiceDeployments(ctx, []string{deps[0].ARN, deps[1].ARN, "arn:aws:ecs:us-east-1:000000000000:service-deployment/prod/web/nope"})
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Len(t, failures, 1)
	assert.Equal(t, "MISSING", failures[0].Reason)
	assert.Equal(t, deps[0].TargetServiceRevision.ARN, got[0].TargetServiceRevision.ARN)

	revs, failures, err := m.DescribeServiceRevisions(ctx, []string{
		deps[0].TargetServiceRevision.ARN, deps[1].TargetServiceRevision.ARN, "bogus",
	})
	require.NoError(t, err)
	require.Len(t, revs, 2)
	require.Len(t, failures, 1)
	assert.Contains(t, revs[0].TaskDefinition, "fg:2")
	assert.Contains(t, revs[1].TaskDefinition, "fg:1")

	tooMany := make([]string, 21)
	for i := range tooMany {
		tooMany[i] = deps[0].ARN
	}

	_, _, err = m.DescribeServiceDeployments(ctx, tooMany)
	assert.Equal(t, excInvalidParameter, ecsException(t, err), "at most 20 ARNs")

	_, _, err = m.DescribeServiceRevisions(ctx, tooMany)
	assert.Equal(t, excInvalidParameter, ecsException(t, err), "at most 20 ARNs")

	_, _, err = m.DescribeServiceDeployments(ctx, nil)
	assert.Equal(t, excInvalidParameter, ecsException(t, err), "ARNs are required")
}

// asyncDeploymentFixture is deploymentFixture on a mock with AsyncSettle, plus a
// second deployment (fg:2) still IN_PROGRESS.
func asyncDeploymentFixture(t *testing.T) (*Mock, *config.FakeClock, []driver.ServiceDeployment) {
	t.Helper()

	m, clock := newSettleMock()
	deploymentFixture(t, m)

	clock.Advance(time.Minute)

	_, err := m.UpdateService(context.Background(), driver.UpdateServiceInput{Service: "web", Cluster: "prod", TaskDefinition: "fg:2"})
	require.NoError(t, err)

	deps, _ := listDeployments(t, m, driver.ListServiceDeploymentsInput{})
	require.Len(t, deps, 2)
	require.Equal(t, driver.DeploymentStatusInProgress, deps[0].Status)
	require.Equal(t, driver.DeploymentStatusSuccessful, deps[1].Status)

	return m, clock, deps
}

func TestStopServiceDeployment_Rollback_AsyncWindow(t *testing.T) {
	m, clock, deps := asyncDeploymentFixture(t)
	ctx := context.Background()

	arn, err := m.StopServiceDeployment(ctx, deps[0].ARN, driver.StopTypeRollback)
	require.NoError(t, err)
	assert.Equal(t, deps[0].ARN, arn)

	got, _, err := m.DescribeServiceDeployments(ctx, []string{deps[0].ARN})
	require.NoError(t, err)
	assert.Equal(t, driver.DeploymentStatusRollbackInProgress, got[0].Status)

	// A repeated stop while it is rolling back continues as-is.
	_, err = m.StopServiceDeployment(ctx, deps[0].ARN, driver.StopTypeRollback)
	require.NoError(t, err)

	clock.Advance(time.Minute)

	got, _, err = m.DescribeServiceDeployments(ctx, []string{deps[0].ARN})
	require.NoError(t, err)
	assert.Equal(t, driver.DeploymentStatusRollbackSuccessful, got[0].Status)
	require.NotNil(t, got[0].Rollback)
	assert.Equal(t, deps[1].TargetServiceRevision.ARN, got[0].Rollback.ServiceRevisionARN)

	svc, _, err := m.DescribeServices(ctx, "prod", []string{"web"})
	require.NoError(t, err)
	assert.Contains(t, svc[0].TaskDefinition, "task-definition/fg:1", "the service is back on the source revision")

	after, _ := listDeployments(t, m, driver.ListServiceDeploymentsInput{})
	assert.Len(t, after, 2, "a rollback does not record a new deployment")
}

func TestStopServiceDeployment_Abort_AsyncWindow(t *testing.T) {
	m, clock, deps := asyncDeploymentFixture(t)
	ctx := context.Background()

	_, err := m.StopServiceDeployment(ctx, deps[0].ARN, driver.StopTypeAbort)
	require.NoError(t, err)

	got, _, err := m.DescribeServiceDeployments(ctx, []string{deps[0].ARN})
	require.NoError(t, err)
	assert.Equal(t, driver.DeploymentStatusStopRequested, got[0].Status)

	clock.Advance(time.Minute)

	got, _, err = m.DescribeServiceDeployments(ctx, []string{deps[0].ARN})
	require.NoError(t, err)
	assert.Equal(t, driver.DeploymentStatusStopped, got[0].Status)
	assert.NotEmpty(t, got[0].StoppedAt)
}

func TestStopServiceDeployment_ConflictAndNotFound(t *testing.T) {
	m := newTestMock()
	deploymentFixture(t, m)
	ctx := context.Background()

	deps, _ := listDeployments(t, m, driver.ListServiceDeploymentsInput{})
	require.Len(t, deps, 1)

	_, err := m.StopServiceDeployment(ctx, deps[0].ARN, driver.StopTypeRollback)
	require.Error(t, err)
	assert.Equal(t, excConflict, ecsException(t, err), "a completed deployment cannot be stopped")

	_, err = m.StopServiceDeployment(ctx, "arn:aws:ecs:us-east-1:000000000000:service-deployment/prod/web/nope", driver.StopTypeAbort)
	assert.Equal(t, excServiceDeploymentNotFound, ecsException(t, err))

	_, err = m.StopServiceDeployment(ctx, deps[0].ARN, "HALT")
	assert.Equal(t, excInvalidParameter, ecsException(t, err))
}

func TestDeleteService_RemovesDeploymentsAndCapsHistory(t *testing.T) {
	m := newTestMock()
	deploymentFixture(t, m)
	ctx := context.Background()

	for range maxServiceDeployments + 5 {
		forceDeploy(t, m)
	}

	deps, _ := listDeployments(t, m, driver.ListServiceDeploymentsInput{MaxResults: 100})
	assert.Len(t, deps, maxServiceDeployments, "history is capped, oldest dropped")
	// The oldest retained deployment still references its source revision.
	assert.LessOrEqual(t, m.serviceRevisions.Len(), maxServiceDeployments+1)

	_, err := m.DeleteService(ctx, "prod", "web", true)
	require.NoError(t, err)

	assert.Equal(t, 0, m.serviceDeployments.Len())
	assert.Equal(t, 0, m.serviceRevisions.Len())
}

func TestRestore_ServiceWithoutDeployments(t *testing.T) {
	m := newTestMock()
	deploymentFixture(t, m)

	// A snapshot written before deployment records existed restores a service
	// with none: it must still report its current deployment.
	m.serviceDeployments.Clear()
	m.serviceRevisions.Clear()

	deps, _ := listDeployments(t, m, driver.ListServiceDeploymentsInput{})
	require.Len(t, deps, 1)
	assert.Equal(t, driver.DeploymentStatusSuccessful, deps[0].Status)

	revs, _, err := m.DescribeServiceRevisions(context.Background(), []string{deps[0].TargetServiceRevision.ARN})
	require.NoError(t, err)
	assert.Len(t, revs, 1)

	again, _ := listDeployments(t, m, driver.ListServiceDeploymentsInput{})
	require.Len(t, again, 1)
	assert.Equal(t, deps[0].ARN, again[0].ARN, "synthesized once, then stable")
}

func TestLargeCollections_PagedAndBounded(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	netCfg := fargateFixture(t, m)

	const services = 5

	for i := range services {
		name := "svc" + string(rune('a'+i))

		_, err := m.CreateService(ctx, driver.CreateServiceInput{
			ServiceName: name, Cluster: "prod", TaskDefinition: "fg", LaunchType: launchFargate, NetworkConfiguration: netCfg,
			ServiceConnect: &driver.ServiceConnectConfiguration{Namespace: "bulk"},
		})
		require.NoError(t, err)

		for range maxServiceDeployments + 20 {
			_, err := m.UpdateService(ctx, driver.UpdateServiceInput{Service: name, Cluster: "prod", ForceNewDeployment: true})
			require.NoError(t, err)
		}

		var seen []string

		token := ""

		for {
			page, next, err := m.ListServiceDeployments(ctx, driver.ListServiceDeploymentsInput{
				Cluster: "prod", Service: name, MaxResults: 7, NextToken: token,
			})
			require.NoError(t, err)

			for i := range page {
				seen = append(seen, page[i].ARN)
			}

			if next == "" {
				break
			}

			token = next
		}

		assert.Len(t, seen, maxServiceDeployments)
		assert.Len(t, uniqueStrings(seen), maxServiceDeployments, "every deployment appears exactly once")
	}

	var all []string

	token := ""

	for {
		page, next, err := m.ListServicesByNamespace(ctx, "bulk", 2, token)
		require.NoError(t, err)

		all = append(all, page...)

		if next == "" {
			break
		}

		token = next
	}

	assert.Len(t, all, services)
	assert.Len(t, uniqueStrings(all), services)
}

func uniqueStrings(in []string) map[string]bool {
	out := make(map[string]bool, len(in))
	for _, s := range in {
		out[s] = true
	}

	return out
}

func TestMalformedAndForeignIdentifiers_Deployments(t *testing.T) {
	m := newTestMock()
	deploymentFixture(t, m)
	ctx := context.Background()

	deps, _ := listDeployments(t, m, driver.ListServiceDeploymentsInput{})
	require.Len(t, deps, 1)

	got, failures, err := m.DescribeServiceDeployments(ctx, []string{"not-an-arn", "arn:aws:ecs:us-east-1:111111111111:service-deployment/prod/web/x"})
	require.NoError(t, err)
	assert.Empty(t, got)
	assert.Len(t, failures, 2, "malformed and other-account ARNs are MISSING, never someone else's data")
}

func TestListServicesByNamespace(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	netCfg := fargateFixture(t, m)

	_, err := m.CreateCluster(ctx, driver.CreateClusterInput{Name: "second"})
	require.NoError(t, err)

	mk := func(cluster, name, ns string) {
		in := driver.CreateServiceInput{
			ServiceName: name, Cluster: cluster, TaskDefinition: "fg", LaunchType: launchFargate, NetworkConfiguration: netCfg,
		}
		if ns != "" {
			in.ServiceConnect = &driver.ServiceConnectConfiguration{Namespace: ns, Raw: []byte(`{"enabled":true,"namespace":"` + ns + `"}`)}
		}

		_, err := m.CreateService(ctx, in)
		require.NoError(t, err)
	}

	mk("prod", "b", "ns1")
	mk("second", "a", "ns1")
	mk("prod", "c", "ns2")
	mk("prod", "d", "")

	got, next, err := m.ListServicesByNamespace(ctx, "ns1", 0, "")
	require.NoError(t, err)
	assert.Empty(t, next)
	require.Len(t, got, 2)
	assert.Contains(t, got[0], "service/prod/b", "sorted by ARN, across clusters")
	assert.Contains(t, got[1], "service/second/a")

	deleted, err := m.DeleteService(ctx, "prod", "b", true)
	require.NoError(t, err)
	assert.Equal(t, statusInactive, deleted.Status)

	got, _, err = m.ListServicesByNamespace(ctx, "ns1", 0, "")
	require.NoError(t, err)
	assert.Len(t, got, 1, "deleted services are not listed")
}

func TestListServicesByNamespace_PagingAndUnknown(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	netCfg := fargateFixture(t, m)

	for _, n := range []string{"a", "b", "c"} {
		_, err := m.CreateService(ctx, driver.CreateServiceInput{
			ServiceName: n, Cluster: "prod", TaskDefinition: "fg", LaunchType: launchFargate, NetworkConfiguration: netCfg,
			ServiceConnect: &driver.ServiceConnectConfiguration{Namespace: "ns"},
		})
		require.NoError(t, err)
	}

	first, next, err := m.ListServicesByNamespace(ctx, "ns", 2, "")
	require.NoError(t, err)
	require.Len(t, first, 2)
	require.NotEmpty(t, next)

	second, next, err := m.ListServicesByNamespace(ctx, "ns", 2, next)
	require.NoError(t, err)
	assert.Len(t, second, 1)
	assert.Empty(t, next)

	unknown, next, err := m.ListServicesByNamespace(ctx, "no-such-namespace", 0, "")
	require.NoError(t, err)
	assert.Empty(t, unknown, "an unknown namespace lists nothing (no Cloud Map to consult)")
	assert.Empty(t, next)

	for _, bad := range []int{-1, 101} {
		_, _, err := m.ListServicesByNamespace(ctx, "ns", bad, "")
		require.Error(t, err, bad)
		assert.Equal(t, excInvalidParameter, ecsException(t, err), bad)
	}

	_, _, err = m.ListServicesByNamespace(ctx, "ns", 1, "!!bad")
	assert.Equal(t, excInvalidParameter, ecsException(t, err))

	_, _, err = m.ListServicesByNamespace(ctx, "", 0, "")
	assert.Equal(t, excInvalidParameter, ecsException(t, err), "namespace is required")
}
