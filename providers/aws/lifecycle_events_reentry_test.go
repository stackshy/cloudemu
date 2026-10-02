package aws_test

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/providers/aws"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
	ecsdriver "github.com/stackshy/cloudemu/v2/services/ecs/driver"
	ebdriver "github.com/stackshy/cloudemu/v2/services/eventbus/driver"
	sdriver "github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

// reentryTimeout bounds the re-entrant scenario: it finishes in milliseconds
// when correct and never finishes when it deadlocks.
const reentryTimeout = 10 * time.Second

// TestECSTaskEventHandlerReentersSameServiceWithoutDeadlock pins that a task
// state-change event is never published while the service's reconcile lock is
// held. A StopTask on a service task reconciles the service and launches a
// replacement; that replacement's RUNNING event invokes a Lambda target
// synchronously, and the handler stops a sibling task of the same service,
// re-entering reconciliation for the same service. Publishing under the lock
// blocks that re-entry on the non-reentrant mutex forever.
func TestECSTaskEventHandlerReentersSameServiceWithoutDeadlock(t *testing.T) {
	p := aws.New()
	ctx := context.Background()

	if _, err := p.ECS.CreateCluster(ctx, ecsdriver.CreateClusterInput{Name: "prod"}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	if _, err := p.ECS.RegisterTaskDefinition(ctx, ecsdriver.RegisterTaskDefinitionInput{
		Family: "web", ContainerDefinitions: []ecsdriver.ContainerDefinition{{Name: "c", Image: "nginx"}},
	}); err != nil {
		t.Fatalf("RegisterTaskDefinition: %v", err)
	}

	if _, err := p.ECS.CreateService(ctx, ecsdriver.CreateServiceInput{
		ServiceName: "api", Cluster: "prod", TaskDefinition: "web", DesiredCount: 2, LaunchType: "EXTERNAL",
	}); err != nil {
		t.Fatalf("CreateService: %v", err)
	}

	tasks, err := p.ECS.ListTasks(ctx, "prod", "", "", "api")
	if err != nil || len(tasks) != 2 {
		t.Fatalf("ListTasks: %v (%d tasks), want 2", err, len(tasks))
	}

	fn, err := p.Lambda.CreateFunction(ctx, sdriver.FunctionConfig{Name: "on-task-running"})
	if err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	// The handler is armed only for the replacement launch, and stops the
	// sibling exactly once, forwarding its ctx like a well-behaved handler.
	var armed atomic.Bool

	p.Lambda.RegisterHandler("on-task-running", func(hctx context.Context, _ []byte) ([]byte, error) {
		if armed.CompareAndSwap(true, false) {
			if _, err := p.ECS.StopTask(hctx, "prod", tasks[1].ARN, "handler"); err != nil {
				return nil, err
			}
		}

		return nil, nil
	})

	if _, err := p.EventBridge.PutRule(ctx, &ebdriver.RuleConfig{
		Name:         "running",
		EventPattern: `{"source":["aws.ecs"],"detail-type":["ECS Task State Change"],"detail":{"lastStatus":["RUNNING"]}}`,
	}); err != nil {
		t.Fatalf("PutRule: %v", err)
	}

	if err := p.EventBridge.PutTargets(ctx, "", "running", []ebdriver.Target{{ID: "fn", ARN: fn.ARN}}); err != nil {
		t.Fatalf("PutTargets: %v", err)
	}

	armed.Store(true)

	done := make(chan error, 1)

	go func() {
		_, stopErr := p.ECS.StopTask(ctx, "prod", tasks[0].ARN, "test")
		done <- stopErr
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("StopTask: %v", err)
		}
	case <-time.After(reentryTimeout):
		t.Fatal("StopTask deadlocked: a task event handler re-entered the same service's reconciliation")
	}

	if armed.Load() {
		t.Fatal("the replacement task's RUNNING event never reached the handler")
	}

	svc, _, err := p.ECS.DescribeServices(ctx, "prod", []string{"api"})
	if err != nil || len(svc) != 1 {
		t.Fatalf("DescribeServices: %v", err)
	}

	if svc[0].RunningCount != svc[0].DesiredCount {
		t.Fatalf("service running %d, desired %d after re-entrant reconciliation", svc[0].RunningCount, svc[0].DesiredCount)
	}
}

// reentrySetup builds a cluster + task definition and a Lambda target wired to
// a default-bus rule for the given pattern. The handler, once armed, runs act
// with the event's taskArn exactly once.
func reentrySetup(t *testing.T, pattern string, act func(ctx context.Context, taskArn string)) (*aws.Provider, *atomic.Bool) {
	t.Helper()

	p := aws.New()
	ctx := context.Background()

	if _, err := p.ECS.CreateCluster(ctx, ecsdriver.CreateClusterInput{Name: "prod"}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	if _, err := p.ECS.RegisterTaskDefinition(ctx, ecsdriver.RegisterTaskDefinitionInput{
		Family: "web", ContainerDefinitions: []ecsdriver.ContainerDefinition{{Name: "c", Image: "nginx"}},
	}); err != nil {
		t.Fatalf("RegisterTaskDefinition: %v", err)
	}

	fn, err := p.Lambda.CreateFunction(ctx, sdriver.FunctionConfig{Name: "reactor"})
	if err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	armed := &atomic.Bool{}

	p.Lambda.RegisterHandler("reactor", func(hctx context.Context, payload []byte) ([]byte, error) {
		var ev struct {
			Detail struct {
				TaskArn string `json:"taskArn"`
			} `json:"detail"`
		}

		if err := json.Unmarshal(payload, &ev); err != nil {
			return nil, err
		}

		if armed.CompareAndSwap(true, false) {
			act(hctx, ev.Detail.TaskArn)
		}

		return nil, nil
	})

	if _, err := p.EventBridge.PutRule(ctx, &ebdriver.RuleConfig{Name: "react", EventPattern: pattern}); err != nil {
		t.Fatalf("PutRule: %v", err)
	}

	if err := p.EventBridge.PutTargets(ctx, "", "react", []ebdriver.Target{{ID: "fn", ARN: fn.ARN}}); err != nil {
		t.Fatalf("PutTargets: %v", err)
	}

	return p, armed
}

// runWithTimeout runs op, failing the test if it does not return in time.
func runWithTimeout(t *testing.T, name string, op func() error) {
	t.Helper()

	done := make(chan error, 1)

	go func() { done <- op() }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	case <-time.After(reentryTimeout):
		t.Fatalf("%s deadlocked under a re-entrant event handler", name)
	}
}

// requireServiceConverged asserts the service's actual RUNNING tasks (read from
// the task store, not the service's own counters) equal its desired count, and
// that its runningCount agrees.
func requireServiceConverged(t *testing.T, p *aws.Provider, want int) {
	t.Helper()

	ctx := context.Background()

	arns, err := p.ECS.ListTasks(ctx, "prod", "", "", "api")
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}

	tasks, _, err := p.ECS.DescribeTasks(ctx, "prod", taskARNs(arns))
	if err != nil {
		t.Fatalf("DescribeTasks: %v", err)
	}

	running := 0

	for i := range tasks {
		if tasks[i].LastStatus == "RUNNING" {
			running++
		}
	}

	svc, _, err := p.ECS.DescribeServices(ctx, "prod", []string{"api"})
	if err != nil || len(svc) != 1 {
		t.Fatalf("DescribeServices: %v", err)
	}

	if running != want || svc[0].RunningCount != want || svc[0].DesiredCount != want {
		t.Fatalf("actual running tasks %d, service runningCount %d, desired %d; want all %d",
			running, svc[0].RunningCount, svc[0].DesiredCount, want)
	}
}

func taskARNs(tasks []ecsdriver.Task) []string {
	out := make([]string, 0, len(tasks))
	for i := range tasks {
		out = append(out, tasks[i].ARN)
	}

	return out
}

// TestECSCreateServiceReentrantStopDoesNotOverProvision pins that a service's
// launch events are published only after CreateService commits the service: a
// RUNNING-rule handler that stops the first launched task must see the
// committed service, so reconciliation replaces exactly that one task.
func TestECSCreateServiceReentrantStopDoesNotOverProvision(t *testing.T) {
	var p *aws.Provider

	p, armed := reentrySetup(t,
		`{"source":["aws.ecs"],"detail-type":["ECS Task State Change"],"detail":{"lastStatus":["RUNNING"]}}`,
		func(ctx context.Context, taskArn string) {
			_, _ = p.ECS.StopTask(ctx, "prod", taskArn, "handler")
		})

	armed.Store(true)

	runWithTimeout(t, "CreateService", func() error {
		_, err := p.ECS.CreateService(context.Background(), ecsdriver.CreateServiceInput{
			ServiceName: "api", Cluster: "prod", TaskDefinition: "web", DesiredCount: 2, LaunchType: "EXTERNAL",
		})

		return err
	})

	if armed.Load() {
		t.Fatal("the handler never ran")
	}

	requireServiceConverged(t, p, 2)
}

// TestECSForceNewDeploymentReentrantStopDoesNotOverProvision pins that the
// drain's STOPPED events are published only after UpdateService commits the
// redeployed service: a STOPPED-rule handler that stops a sibling task must not
// reconcile the pre-deployment record and launch extra replacements.
func TestECSForceNewDeploymentReentrantStopDoesNotOverProvision(t *testing.T) {
	var (
		p        *aws.Provider
		siblings []string
	)

	p, armed := reentrySetup(t,
		`{"source":["aws.ecs"],"detail-type":["ECS Task State Change"],"detail":{"lastStatus":["STOPPED"]}}`,
		func(ctx context.Context, taskArn string) {
			for _, s := range siblings {
				if s != taskArn {
					_, _ = p.ECS.StopTask(ctx, "prod", s, "handler")

					return
				}
			}
		})

	ctx := context.Background()

	if _, err := p.ECS.CreateService(ctx, ecsdriver.CreateServiceInput{
		ServiceName: "api", Cluster: "prod", TaskDefinition: "web", DesiredCount: 2, LaunchType: "EXTERNAL",
	}); err != nil {
		t.Fatalf("CreateService: %v", err)
	}

	original, err := p.ECS.ListTasks(ctx, "prod", "", "", "api")
	if err != nil || len(original) != 2 {
		t.Fatalf("ListTasks: %v (%d)", err, len(original))
	}

	siblings = taskARNs(original)

	armed.Store(true)

	runWithTimeout(t, "UpdateService", func() error {
		_, err := p.ECS.UpdateService(ctx, ecsdriver.UpdateServiceInput{
			Service: "api", Cluster: "prod", ForceNewDeployment: true,
		})

		return err
	})

	if armed.Load() {
		t.Fatal("the handler never ran")
	}

	requireServiceConverged(t, p, 2)
}

// TestEC2TerminatedEventAfterVolumeDetach pins that the "terminated" state-change
// event is published only after TerminateInstances has released the instance's
// volumes, so a handler reacting to it (e.g. deleting the volume) never sees the
// volume still in use.
func TestEC2TerminatedEventAfterVolumeDetach(t *testing.T) {
	p := aws.New()
	ctx := context.Background()

	insts, err := p.EC2.RunInstances(ctx, computedriver.InstanceConfig{ImageID: "ami-12345678", InstanceType: "t3.micro"}, 1)
	if err != nil {
		t.Fatalf("RunInstances: %v", err)
	}

	vol, err := p.EC2.CreateVolume(ctx, computedriver.VolumeConfig{Size: 8})
	if err != nil {
		t.Fatalf("CreateVolume: %v", err)
	}

	if err = p.EC2.AttachVolume(ctx, vol.ID, insts[0].ID, "/dev/sdf"); err != nil {
		t.Fatalf("AttachVolume: %v", err)
	}

	fn, err := p.Lambda.CreateFunction(ctx, sdriver.FunctionConfig{Name: "on-terminated"})
	if err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	var seenState atomic.Value

	p.Lambda.RegisterHandler("on-terminated", func(hctx context.Context, _ []byte) ([]byte, error) {
		vols, derr := p.EC2.DescribeVolumes(hctx, []string{vol.ID})
		if derr == nil && len(vols) == 1 {
			seenState.Store(vols[0].State)
		}

		return nil, nil
	})

	if _, err = p.EventBridge.PutRule(ctx, &ebdriver.RuleConfig{
		Name:         "terminated",
		EventPattern: `{"source":["aws.ec2"],"detail":{"state":["terminated"]}}`,
	}); err != nil {
		t.Fatalf("PutRule: %v", err)
	}

	if err = p.EventBridge.PutTargets(ctx, "", "terminated", []ebdriver.Target{{ID: "fn", ARN: fn.ARN}}); err != nil {
		t.Fatalf("PutTargets: %v", err)
	}

	if err = p.EC2.TerminateInstances(ctx, []string{insts[0].ID}); err != nil {
		t.Fatalf("TerminateInstances: %v", err)
	}

	if got, _ := seenState.Load().(string); got != "available" {
		t.Fatalf("volume state seen by the terminated-event handler = %q, want available", got)
	}
}
