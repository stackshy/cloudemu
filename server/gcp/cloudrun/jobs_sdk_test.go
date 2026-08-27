package cloudrun_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/api/option"
	run "google.golang.org/api/run/v2"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

const parent = "projects/" + project + "/locations/" + location

// newRun boots an in-process GCP server backed by the real cloudemu Cloud Run
// driver and returns a run/v2 SDK client pointed at it.
func newRun(t *testing.T, eng config.ContainerEngine) *run.Service {
	t.Helper()

	cloud := cloudemu.NewGCP(config.WithContainerEngine(eng))
	ts := httptest.NewServer(gcpserver.New(gcpserver.Drivers{CloudRun: cloud.CloudRun}))
	t.Cleanup(ts.Close)

	svc, err := run.NewService(context.Background(),
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("run.NewService: %v", err)
	}

	return svc
}

func sdkJob() *run.GoogleCloudRunV2Job {
	return &run.GoogleCloudRunV2Job{
		Template: &run.GoogleCloudRunV2ExecutionTemplate{
			TaskCount:   2,
			Parallelism: 1,
			Template: &run.GoogleCloudRunV2TaskTemplate{
				MaxRetries:           3,
				Timeout:              "600s",
				ServiceAccount:       "runner@demo-project.iam.gserviceaccount.com",
				ExecutionEnvironment: "EXECUTION_ENVIRONMENT_GEN2",
				VpcAccess: &run.GoogleCloudRunV2VpcAccess{
					Connector: "projects/demo-project/locations/us-central1/connectors/c1",
					Egress:    "ALL_TRAFFIC",
				},
				Containers: []*run.GoogleCloudRunV2Container{{Image: "busybox"}},
			},
		},
	}
}

// TestSDKJobTemplateFieldsRoundTrip covers the [MEDIUM] finding: create with
// maxRetries/serviceAccount/timeout/parallelism/executionEnvironment/vpcAccess
// then Get must round-trip them.
func TestSDKJobTemplateFieldsRoundTrip(t *testing.T) {
	svc := newRun(t, nil)
	ctx := context.Background()

	if _, err := svc.Projects.Locations.Jobs.Create(parent, sdkJob()).JobId("batch").Context(ctx).Do(); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := svc.Projects.Locations.Jobs.Get(parent + "/jobs/batch").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	tt := got.Template.Template
	if tt.MaxRetries != 3 {
		t.Errorf("maxRetries = %d, want 3", tt.MaxRetries)
	}

	if tt.Timeout != "600s" {
		t.Errorf("timeout = %q, want 600s", tt.Timeout)
	}

	if tt.ServiceAccount != "runner@demo-project.iam.gserviceaccount.com" {
		t.Errorf("serviceAccount = %q", tt.ServiceAccount)
	}

	if tt.ExecutionEnvironment != "EXECUTION_ENVIRONMENT_GEN2" {
		t.Errorf("executionEnvironment = %q", tt.ExecutionEnvironment)
	}

	if got.Template.Parallelism != 1 {
		t.Errorf("parallelism = %d, want 1", got.Template.Parallelism)
	}

	if tt.VpcAccess == nil || tt.VpcAccess.Connector == "" || tt.VpcAccess.Egress != "ALL_TRAFFIC" {
		t.Errorf("vpcAccess = %+v", tt.VpcAccess)
	}
}

// TestSDKJobGetStatusFields covers the [MEDIUM] finding: Get returns
// terminalCondition/etag/observedGeneration/reconciling and, after a run,
// latestCreatedExecution.
func TestSDKJobGetStatusFields(t *testing.T) {
	eng := &fakeEngine{statuses: []config.ContainerStatus{{State: "exited", ExitCode: 0}}}
	svc := newRun(t, eng)
	ctx := context.Background()

	if _, err := svc.Projects.Locations.Jobs.Create(parent, sdkJob()).JobId("batch").Context(ctx).Do(); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := svc.Projects.Locations.Jobs.Run(parent+"/jobs/batch", &run.GoogleCloudRunV2RunJobRequest{}).
		Context(ctx).Do(); err != nil {
		t.Fatalf("Run: %v", err)
	}

	got, err := svc.Projects.Locations.Jobs.Get(parent + "/jobs/batch").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.TerminalCondition == nil || got.TerminalCondition.Type != "Ready" {
		t.Errorf("terminalCondition = %+v, want type Ready", got.TerminalCondition)
	}

	if got.Etag == "" {
		t.Error("etag is empty")
	}

	if got.ObservedGeneration != 1 {
		t.Errorf("observedGeneration = %d, want 1", got.ObservedGeneration)
	}

	if got.LatestCreatedExecution == nil || !strings.Contains(got.LatestCreatedExecution.Name, "/executions/batch-") {
		t.Errorf("latestCreatedExecution = %+v", got.LatestCreatedExecution)
	}
}

// TestSDKJobPatch covers the [HIGH] finding: Jobs.Patch returns an LRO with the
// mutated job rather than 405.
func TestSDKJobPatch(t *testing.T) {
	svc := newRun(t, nil)
	ctx := context.Background()

	if _, err := svc.Projects.Locations.Jobs.Create(parent, sdkJob()).JobId("batch").Context(ctx).Do(); err != nil {
		t.Fatalf("Create: %v", err)
	}

	patched := sdkJob()
	patched.Template.TaskCount = 5
	patched.Template.Template.MaxRetries = 7

	op, err := svc.Projects.Locations.Jobs.Patch(parent+"/jobs/batch", patched).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}

	if !op.Done {
		t.Fatal("patch operation not done")
	}

	got, err := svc.Projects.Locations.Jobs.Get(parent + "/jobs/batch").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Template.TaskCount != 5 || got.Template.Template.MaxRetries != 7 {
		t.Errorf("after patch taskCount=%d maxRetries=%d, want 5/7",
			got.Template.TaskCount, got.Template.Template.MaxRetries)
	}

	if got.Generation != 2 {
		t.Errorf("generation = %d, want 2 after patch", got.Generation)
	}
}

// TestSDKJobIam covers the [HIGH] finding: getIamPolicy/setIamPolicy round-trip
// and testIamPermissions returns the held subset.
func TestSDKJobIam(t *testing.T) {
	svc := newRun(t, nil)
	ctx := context.Background()

	if _, err := svc.Projects.Locations.Jobs.Create(parent, sdkJob()).JobId("batch").Context(ctx).Do(); err != nil {
		t.Fatalf("Create: %v", err)
	}

	resource := parent + "/jobs/batch"

	set := &run.GoogleIamV1SetIamPolicyRequest{Policy: &run.GoogleIamV1Policy{
		Bindings: []*run.GoogleIamV1Binding{{
			Role:    "roles/run.invoker",
			Members: []string{"user:dev@example.com"},
		}},
	}}
	if _, err := svc.Projects.Locations.Jobs.SetIamPolicy(resource, set).Context(ctx).Do(); err != nil {
		t.Fatalf("SetIamPolicy: %v", err)
	}

	pol, err := svc.Projects.Locations.Jobs.GetIamPolicy(resource).Context(ctx).Do()
	if err != nil {
		t.Fatalf("GetIamPolicy: %v", err)
	}

	if len(pol.Bindings) != 1 || pol.Bindings[0].Role != "roles/run.invoker" {
		t.Fatalf("policy bindings = %+v", pol.Bindings)
	}

	test := &run.GoogleIamV1TestIamPermissionsRequest{Permissions: []string{"run.jobs.run", "run.jobs.get"}}

	res, err := svc.Projects.Locations.Jobs.TestIamPermissions(resource, test).Context(ctx).Do()
	if err != nil {
		t.Fatalf("TestIamPermissions: %v", err)
	}

	if len(res.Permissions) != 2 {
		t.Errorf("held permissions = %v, want 2", res.Permissions)
	}
}

// TestSDKJobExecutionsList covers the [HIGH] finding: the executions collection
// returns {executions,...}, not the Job.
func TestSDKJobExecutionsList(t *testing.T) {
	eng := &fakeEngine{statuses: []config.ContainerStatus{{State: "exited", ExitCode: 0}}}
	svc := newRun(t, eng)
	ctx := context.Background()

	if _, err := svc.Projects.Locations.Jobs.Create(parent, sdkJob()).JobId("batch").Context(ctx).Do(); err != nil {
		t.Fatalf("Create: %v", err)
	}

	for range 2 {
		if _, err := svc.Projects.Locations.Jobs.Run(parent+"/jobs/batch", &run.GoogleCloudRunV2RunJobRequest{}).
			Context(ctx).Do(); err != nil {
			t.Fatalf("Run: %v", err)
		}
	}

	list, err := svc.Projects.Locations.Jobs.Executions.List(parent + "/jobs/batch").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Executions.List: %v", err)
	}

	if len(list.Executions) != 2 {
		t.Fatalf("executions = %d, want 2", len(list.Executions))
	}

	if !strings.Contains(list.Executions[0].Name, "/jobs/batch/executions/") {
		t.Errorf("execution name = %q", list.Executions[0].Name)
	}
}

// TestSDKJobsListPagination covers the [LOW] finding: pageSize/pageToken paginate.
func TestSDKJobsListPagination(t *testing.T) {
	svc := newRun(t, nil)
	ctx := context.Background()

	for _, id := range []string{"a", "b"} {
		if _, err := svc.Projects.Locations.Jobs.Create(parent, sdkJob()).JobId(id).Context(ctx).Do(); err != nil {
			t.Fatalf("Create %s: %v", id, err)
		}
	}

	page1, err := svc.Projects.Locations.Jobs.List(parent).PageSize(1).Context(ctx).Do()
	if err != nil {
		t.Fatalf("List page1: %v", err)
	}

	if len(page1.Jobs) != 1 || page1.NextPageToken == "" {
		t.Fatalf("page1 jobs=%d token=%q, want 1 job + token", len(page1.Jobs), page1.NextPageToken)
	}

	page2, err := svc.Projects.Locations.Jobs.List(parent).PageSize(1).PageToken(page1.NextPageToken).Context(ctx).Do()
	if err != nil {
		t.Fatalf("List page2: %v", err)
	}

	if len(page2.Jobs) != 1 || page2.NextPageToken != "" {
		t.Fatalf("page2 jobs=%d token=%q, want 1 job + no token", len(page2.Jobs), page2.NextPageToken)
	}
}

// TestSDKJobRunSucceededCount covers the [LOW] finding: a completed synthetic
// (no-engine) execution reports succeededCount == taskCount, distinguishing
// success from failure.
func TestSDKJobRunSucceededCount(t *testing.T) {
	svc := newRun(t, nil)
	ctx := context.Background()

	if _, err := svc.Projects.Locations.Jobs.Create(parent, sdkJob()).JobId("batch").Context(ctx).Do(); err != nil {
		t.Fatalf("Create: %v", err)
	}

	op, err := svc.Projects.Locations.Jobs.Run(parent+"/jobs/batch", &run.GoogleCloudRunV2RunJobRequest{}).
		Context(ctx).Do()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var exec run.GoogleCloudRunV2Execution
	decodeOpResponse(t, op, &exec)

	if exec.SucceededCount != 2 {
		t.Fatalf("succeededCount = %d, want 2 (taskCount)", exec.SucceededCount)
	}

	if exec.FailedCount != 0 || exec.CompletionTime == "" {
		t.Fatalf("failedCount=%d completionTime=%q, want 0 + set", exec.FailedCount, exec.CompletionTime)
	}
}
