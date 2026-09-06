package batch_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/aws/batch"
	"github.com/stackshy/cloudemu/v2/services/batch/driver"
)

func newMock(t *testing.T) *batch.Mock {
	t.Helper()

	return batch.New(config.NewOptions(
		config.WithRegion("us-east-1"),
		config.WithAccountID("000000000000"),
	))
}

func fargateResources(maxvCpus int) json.RawMessage {
	b, _ := json.Marshal(map[string]any{
		"type":             "FARGATE",
		"maxvCpus":         maxvCpus,
		"subnets":          []string{"subnet-1", "subnet-2"},
		"securityGroupIds": []string{"sg-1"},
	})

	return b
}

func mustCreateCE(t *testing.T, m *batch.Mock, name string) *driver.ComputeEnvironment {
	t.Helper()

	ce, err := m.CreateComputeEnvironment(context.Background(), driver.CreateComputeEnvironmentInput{
		Name:             name,
		Type:             driver.CETypeManaged,
		ComputeResources: fargateResources(16),
	})
	if err != nil {
		t.Fatalf("CreateComputeEnvironment: %v", err)
	}

	return ce
}

func TestCreateComputeEnvironmentSyncValid(t *testing.T) {
	m := newMock(t)
	ce := mustCreateCE(t, m, "ce1")

	if ce.Status != driver.StatusValid {
		t.Fatalf("want VALID immediately, got %q", ce.Status)
	}

	if ce.State != driver.StateEnabled {
		t.Fatalf("want default state ENABLED, got %q", ce.State)
	}

	if ce.EcsClusterARN == "" || ce.UUID == "" || ce.ARN == "" {
		t.Fatalf("synthesized fields missing: %+v", ce)
	}
}

func TestCreateComputeEnvironmentValidation(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	if _, err := m.CreateComputeEnvironment(ctx, driver.CreateComputeEnvironmentInput{Type: driver.CETypeManaged}); !errors.IsInvalidArgument(err) {
		t.Fatalf("missing name should be InvalidArgument, got %v", err)
	}

	if _, err := m.CreateComputeEnvironment(ctx, driver.CreateComputeEnvironmentInput{Name: "x", Type: "BOGUS"}); !errors.IsInvalidArgument(err) {
		t.Fatalf("bad type should be InvalidArgument, got %v", err)
	}

	if _, err := m.CreateComputeEnvironment(ctx, driver.CreateComputeEnvironmentInput{Name: "x", Type: driver.CETypeManaged}); !errors.IsInvalidArgument(err) {
		t.Fatalf("MANAGED without computeResources should be InvalidArgument, got %v", err)
	}

	mustCreateCE(t, m, "dup")
	if _, err := m.CreateComputeEnvironment(ctx, driver.CreateComputeEnvironmentInput{
		Name: "dup", Type: driver.CETypeManaged, ComputeResources: fargateResources(4),
	}); !errors.IsAlreadyExists(err) {
		t.Fatalf("duplicate should be AlreadyExists, got %v", err)
	}
}

func TestUpdateComputeEnvironmentMergesResources(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	mustCreateCE(t, m, "ce")

	if _, err := m.UpdateComputeEnvironment(ctx, driver.UpdateComputeEnvironmentInput{
		Name:             "ce",
		ComputeResources: json.RawMessage(`{"maxvCpus":64}`),
	}); err != nil {
		t.Fatalf("update: %v", err)
	}

	got, _ := m.DescribeComputeEnvironments(ctx, []string{"ce"})

	var cr map[string]any
	if err := json.Unmarshal(got[0].ComputeResources, &cr); err != nil {
		t.Fatalf("unmarshal computeResources: %v", err)
	}

	if cr["maxvCpus"].(float64) != 64 {
		t.Fatalf("maxvCpus not merged: %v", cr)
	}

	if subnets, ok := cr["subnets"].([]any); !ok || len(subnets) != 2 {
		t.Fatalf("merge dropped unchanged subnets: %v", cr)
	}
}

func TestDeleteComputeEnvironmentRequiresDisabledAndUnreferenced(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	mustCreateCE(t, m, "ce")

	if err := m.DeleteComputeEnvironment(ctx, "ce"); !errors.IsFailedPrecondition(err) {
		t.Fatalf("delete of ENABLED CE should fail, got %v", err)
	}

	// Reference it from a queue, disable, and confirm the reference blocks delete.
	if _, err := m.CreateJobQueue(ctx, driver.CreateJobQueueInput{
		Name:                    "q",
		Priority:                1,
		ComputeEnvironmentOrder: []driver.ComputeEnvironmentOrder{{Order: 1, ComputeEnvironment: "ce"}},
	}); err != nil {
		t.Fatalf("create queue: %v", err)
	}

	disabled := driver.StateDisabled
	if _, err := m.UpdateComputeEnvironment(ctx, driver.UpdateComputeEnvironmentInput{Name: "ce", State: disabled}); err != nil {
		t.Fatalf("disable: %v", err)
	}

	if err := m.DeleteComputeEnvironment(ctx, "ce"); !errors.IsFailedPrecondition(err) {
		t.Fatalf("referenced CE delete should fail, got %v", err)
	}

	// Disable + delete the queue, then the CE deletes.
	state := driver.StateDisabled
	if _, err := m.UpdateJobQueue(ctx, driver.UpdateJobQueueInput{Name: "q", State: &state}); err != nil {
		t.Fatalf("disable queue: %v", err)
	}

	if err := m.DeleteJobQueue(ctx, "q"); err != nil {
		t.Fatalf("delete queue: %v", err)
	}

	if err := m.DeleteComputeEnvironment(ctx, "ce"); err != nil {
		t.Fatalf("delete CE: %v", err)
	}
}

func TestJobQueueRequiresValidComputeEnvironment(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	if _, err := m.CreateJobQueue(ctx, driver.CreateJobQueueInput{
		Name:                    "q",
		Priority:                1,
		ComputeEnvironmentOrder: []driver.ComputeEnvironmentOrder{{Order: 1, ComputeEnvironment: "missing"}},
	}); !errors.IsInvalidArgument(err) {
		t.Fatalf("queue referencing missing CE should fail, got %v", err)
	}
}

func TestRegisterJobDefinitionRevisionIncrements(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	in := driver.RegisterJobDefinitionInput{
		Name:                "jd",
		Type:                driver.JDTypeContainer,
		ContainerProperties: json.RawMessage(`{"image":"alpine"}`),
	}

	first, err := m.RegisterJobDefinition(ctx, in)
	if err != nil {
		t.Fatalf("register 1: %v", err)
	}

	if first.Revision != 1 || first.Status != driver.JDStatusActive {
		t.Fatalf("first revision/status wrong: %+v", first)
	}

	if len(first.PlatformCapabilities) != 1 || first.PlatformCapabilities[0] != "EC2" {
		t.Fatalf("platformCapabilities default should be [EC2], got %v", first.PlatformCapabilities)
	}

	second, err := m.RegisterJobDefinition(ctx, in)
	if err != nil {
		t.Fatalf("register 2: %v", err)
	}

	if second.Revision != 2 {
		t.Fatalf("re-register should bump to 2, got %d", second.Revision)
	}

	if second.ARN == first.ARN {
		t.Fatalf("revision 2 must have a new ARN")
	}

	// Both revisions ACTIVE and describable by name.
	all, _ := m.DescribeJobDefinitions(ctx, driver.DescribeJobDefinitionsInput{Name: "jd"})
	if len(all) != 2 {
		t.Fatalf("want 2 ACTIVE revisions, got %d", len(all))
	}

	// Deregister revision 1 -> INACTIVE.
	if err := m.DeregisterJobDefinition(ctx, first.ARN); err != nil {
		t.Fatalf("deregister: %v", err)
	}

	active, _ := m.DescribeJobDefinitions(ctx, driver.DescribeJobDefinitionsInput{Name: "jd"})
	if len(active) != 1 || active[0].Revision != 2 {
		t.Fatalf("after deregister want only revision 2, got %+v", active)
	}
}

func TestTagsRoundTripAcrossKinds(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	ce := mustCreateCE(t, m, "ce")

	if err := m.TagResource(ctx, ce.ARN, map[string]string{"env": "prod", "team": "core"}); err != nil {
		t.Fatalf("tag: %v", err)
	}

	tags, err := m.ListTagsForResource(ctx, ce.ARN)
	if err != nil {
		t.Fatalf("list tags: %v", err)
	}

	if tags["env"] != "prod" || tags["team"] != "core" {
		t.Fatalf("tags did not round-trip: %v", tags)
	}

	if err := m.UntagResource(ctx, ce.ARN, []string{"team"}); err != nil {
		t.Fatalf("untag: %v", err)
	}

	tags, _ = m.ListTagsForResource(ctx, ce.ARN)
	if _, ok := tags["team"]; ok {
		t.Fatalf("untag did not remove key: %v", tags)
	}

	if _, err := m.ListTagsForResource(ctx, "arn:aws:batch:us-east-1:000000000000:job-queue/nope"); !errors.IsNotFound(err) {
		t.Fatalf("tags on missing resource should be NotFound, got %v", err)
	}
}

func TestConcurrentRegisterUniqueRevisions(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	const n = 20

	seen := make(chan int32, n)
	done := make(chan struct{})

	for i := 0; i < n; i++ {
		go func() {
			jd, err := m.RegisterJobDefinition(ctx, driver.RegisterJobDefinitionInput{
				Name: "race", Type: driver.JDTypeContainer, ContainerProperties: json.RawMessage(`{"image":"x"}`),
			})
			if err == nil {
				seen <- jd.Revision
			}

			done <- struct{}{}
		}()
	}

	for i := 0; i < n; i++ {
		<-done
	}

	close(seen)

	revs := map[int32]bool{}
	for r := range seen {
		if revs[r] {
			t.Fatalf("duplicate revision %d allocated under concurrency", r)
		}

		revs[r] = true
	}

	if len(revs) != n {
		t.Fatalf("want %d unique revisions, got %d", n, len(revs))
	}
}
