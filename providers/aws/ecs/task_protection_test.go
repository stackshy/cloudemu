package ecs

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/services/ecs/driver"
)

// protectionFixture creates a Fargate service with the given desired count and
// returns its task ARNs (sorted).
func protectionFixture(t *testing.T, m *Mock, desired int) []string {
	t.Helper()

	netCfg := fargateFixture(t, m)

	_, err := m.CreateService(context.Background(), driver.CreateServiceInput{
		ServiceName: "web", Cluster: "prod", TaskDefinition: "fg", LaunchType: launchFargate,
		NetworkConfiguration: netCfg, DesiredCount: desired,
	})
	require.NoError(t, err)

	tasks, err := m.ListTasks(context.Background(), "prod", "", "", "web")
	require.NoError(t, err)
	require.Len(t, tasks, desired)

	arns := make([]string, 0, len(tasks))
	for i := range tasks {
		arns = append(arns, tasks[i].ARN)
	}

	return arns
}

func protect(t *testing.T, m *Mock, expires *int, arns ...string) ([]driver.ProtectedTask, []driver.Failure) {
	t.Helper()

	out, failures, err := m.UpdateTaskProtection(context.Background(), driver.UpdateTaskProtectionInput{
		Cluster: "prod", Tasks: arns, ProtectionEnabled: true, ExpiresInMinutes: expires,
	})
	require.NoError(t, err)

	return out, failures
}

func TestUpdateTaskProtection_EnableDefaultExpiry(t *testing.T) {
	m := newTestMock()
	arns := protectionFixture(t, m, 1)

	out, failures := protect(t, m, nil, arns...)
	require.Empty(t, failures)
	require.Len(t, out, 1)
	assert.Equal(t, arns[0], out[0].TaskARN)
	assert.True(t, out[0].ProtectionEnabled)
	assert.Equal(t, "2025-01-01T02:00:00Z", out[0].ExpirationDate, "default 120 minutes from the clock")

	got, failures, err := m.GetTaskProtection(context.Background(), "prod", arns)
	require.NoError(t, err)
	require.Empty(t, failures)
	assert.Equal(t, out, got)
}

func TestUpdateTaskProtection_ExpiresBounds(t *testing.T) {
	m := newTestMock()
	arns := protectionFixture(t, m, 1)

	for _, ok := range []int{1, 2880} {
		v := ok
		_, failures := protect(t, m, &v, arns...)
		assert.Empty(t, failures, ok)
	}

	for _, bad := range []int{0, 2881, -1} {
		v := bad
		_, _, err := m.UpdateTaskProtection(context.Background(), driver.UpdateTaskProtectionInput{
			Cluster: "prod", Tasks: arns, ProtectionEnabled: true, ExpiresInMinutes: &v,
		})
		require.Error(t, err, bad)
		assert.Equal(t, excInvalidParameter, ecsException(t, err), bad)
	}
}

func TestTaskProtection_Failures(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	arns := protectionFixture(t, m, 1)

	standalone := runFargate(t, m, &driver.NetworkConfiguration{
		AwsVpcConfiguration: &driver.AwsVpcConfiguration{Subnets: []string{"subnet-1"}},
	}, false)

	_, err := m.CreateCluster(ctx, driver.CreateClusterInput{Name: "other"})
	require.NoError(t, err)

	_, failures := protect(t, m, nil, standalone.ARN, "does-not-exist", arns[0])
	require.Len(t, failures, 2)
	assert.Equal(t, driver.Failure{ARN: standalone.ARN, Reason: "TASK_NOT_VALID", Detail: failures[0].Detail}, failures[0])
	assert.NotEmpty(t, failures[0].Detail)
	assert.Equal(t, "MISSING", failures[1].Reason)
	assert.Equal(t, "does-not-exist", failures[1].ARN)

	// A task of another cluster is invisible, never reported.
	_, failures, err = m.GetTaskProtection(ctx, "other", arns)
	require.NoError(t, err)
	require.Len(t, failures, 1)
	assert.Equal(t, "MISSING", failures[0].Reason)

	_, _, err = m.GetTaskProtection(ctx, "ghost", arns)
	assert.Equal(t, excClusterNotFound, ecsException(t, err))

	tooMany := make([]string, 11)
	for i := range tooMany {
		tooMany[i] = arns[0]
	}

	_, _, err = m.UpdateTaskProtection(ctx, driver.UpdateTaskProtectionInput{
		Cluster: "prod", Tasks: tooMany, ProtectionEnabled: true,
	})
	assert.Equal(t, excInvalidParameter, ecsException(t, err), "update accepts at most 10 tasks")

	_, _, err = m.UpdateTaskProtection(ctx, driver.UpdateTaskProtectionInput{Cluster: "prod", ProtectionEnabled: true})
	assert.Equal(t, excInvalidParameter, ecsException(t, err), "tasks is required")

	hundredOne := make([]string, 101)
	for i := range hundredOne {
		hundredOne[i] = arns[0]
	}

	_, _, err = m.GetTaskProtection(ctx, "prod", hundredOne)
	assert.Equal(t, excInvalidParameter, ecsException(t, err), "get accepts at most 100 tasks")
}

func TestTaskProtection_ExpiryByClock(t *testing.T) {
	m := newTestMock()
	arns := protectionFixture(t, m, 1)
	ten := 10

	protect(t, m, &ten, arns...)

	clock, ok := m.opts.Clock.(*config.FakeClock)
	require.True(t, ok)

	clock.Advance(9 * time.Minute)

	got, _, err := m.GetTaskProtection(context.Background(), "prod", arns)
	require.NoError(t, err)
	assert.True(t, got[0].ProtectionEnabled)

	clock.Advance(2 * time.Minute)

	got, _, err = m.GetTaskProtection(context.Background(), "prod", arns)
	require.NoError(t, err)
	assert.False(t, got[0].ProtectionEnabled)
	assert.Empty(t, got[0].ExpirationDate)
}

func TestTaskProtection_DisableOmitsExpiryAndRepeatResets(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	arns := protectionFixture(t, m, 1)
	ten := 10

	first, _ := protect(t, m, &ten, arns...)

	clock, ok := m.opts.Clock.(*config.FakeClock)
	require.True(t, ok)

	clock.Advance(5 * time.Minute)

	second, _ := protect(t, m, &ten, arns...)
	assert.NotEqual(t, first[0].ExpirationDate, second[0].ExpirationDate, "repeat enable resets the expiry")

	off, failures, err := m.UpdateTaskProtection(ctx, driver.UpdateTaskProtectionInput{
		Cluster: "prod", Tasks: arns, ProtectionEnabled: false,
	})
	require.NoError(t, err)
	require.Empty(t, failures)
	assert.False(t, off[0].ProtectionEnabled)
	assert.Empty(t, off[0].ExpirationDate)
}

func TestScaleIn_SkipsProtectedTasks(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	arns := protectionFixture(t, m, 3)

	protect(t, m, nil, arns[0], arns[1])

	one := 1
	svc, err := m.UpdateService(ctx, driver.UpdateServiceInput{Service: "web", Cluster: "prod", DesiredCount: &one})
	require.NoError(t, err)

	running, err := m.ListTasks(ctx, "prod", "", "RUNNING", "web")
	require.NoError(t, err)

	got := map[string]bool{}
	for i := range running {
		got[running[i].ARN] = true
	}

	assert.True(t, got[arns[0]] && got[arns[1]], "protected tasks survive scale-in")
	assert.False(t, got[arns[2]], "the unprotected task is stopped")
	assert.Len(t, running, 2)
	assert.Equal(t, 2, svc.RunningCount)
	assert.Equal(t, 1, svc.DesiredCount)
	assert.Equal(t, rolloutInProgress, svc.Deployments[0].RolloutState, "more running than desired is not steady state")

	// Once protection is removed the next deployment stops the surplus tasks.
	_, _, err = m.UpdateTaskProtection(ctx, driver.UpdateTaskProtectionInput{
		Cluster: "prod", Tasks: []string{arns[0], arns[1]}, ProtectionEnabled: false,
	})
	require.NoError(t, err)

	svc, err = m.UpdateService(ctx, driver.UpdateServiceInput{Service: "web", Cluster: "prod", ForceNewDeployment: true})
	require.NoError(t, err)
	assert.Equal(t, 1, svc.RunningCount)
	assert.Equal(t, rolloutCompleted, svc.Deployments[0].RolloutState)
}

func TestUpdateTaskProtection_DeploymentBlocked(t *testing.T) {
	m := newTestMock()
	arns := protectionFixture(t, m, 2)

	_, failures := protect(t, m, nil, arns[0])
	require.Empty(t, failures)

	m.services.Update(serviceKey("prod", "web"), func(s *driver.Service) *driver.Service {
		out := cloneService(s)
		out.DesiredCount = 1

		return &out
	})

	_, failures = protect(t, m, nil, arns[1])
	require.Len(t, failures, 1)
	assert.Equal(t, "DEPLOYMENT_BLOCKED", failures[0].Reason)
	assert.Equal(t, arns[1], failures[0].ARN)
}

func TestTaskProtection_ParallelWithStop(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	arns := protectionFixture(t, m, 4)

	var wg sync.WaitGroup

	for i := range 40 {
		wg.Add(1)

		go func() {
			defer wg.Done()

			arn := arns[i%len(arns)]

			switch i % 3 {
			case 0:
				_, _, err := m.UpdateTaskProtection(ctx, driver.UpdateTaskProtectionInput{
					Cluster: "prod", Tasks: []string{arn}, ProtectionEnabled: true,
				})
				assert.NoError(t, err)
			case 1:
				_, _, err := m.GetTaskProtection(ctx, "prod", []string{arn})
				assert.NoError(t, err)
			default:
				_, err := m.StopTask(ctx, "prod", arn, "race")
				assert.NoError(t, err)
			}
		}()
	}

	wg.Wait()
}
