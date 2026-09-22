package aws_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/providers/aws"
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
// synchronously, and the handler stops a sibling task of the same service —
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
