package dockerengine_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"google.golang.org/api/option"
	run "google.golang.org/api/run/v2"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/contrib/dockerengine"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

// TestCloudRunJobE2E runs the exact flow a real user runs against GCP Cloud Run
// Jobs: create a job with the real Cloud Run Admin v2 SDK (one alpine container
// echoing a marker), Run it, read the resulting Execution, and assert
// succeededCount==1 — all against CloudEmu backed by a real Docker container (no
// cloud account). succeededCount==1 proves the real container ran to completion
// with exit 0.
func TestCloudRunJobE2E(t *testing.T) {
	if !dockerUp() {
		t.Skip("docker daemon not available")
	}

	eng := dockerengine.NewContainers()
	t.Cleanup(func() { _ = eng.Close() })

	cloud := cloudemu.NewGCP(config.WithContainerEngine(eng))
	ts := httptest.NewServer(gcpserver.New(gcpserver.Drivers{CloudRun: cloud.CloudRun}))
	t.Cleanup(ts.Close)

	ctx := context.Background()

	svc, err := run.NewService(ctx,
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("run.NewService: %v", err)
	}

	const (
		project  = "my-project"
		location = "us-central1"
		jobID    = "batch"
		marker   = "cloudemu-run-marker-9"
	)

	parent := "projects/" + project + "/locations/" + location
	jobName := parent + "/jobs/" + jobID

	// 1. Create the job — like `gcloud run jobs create`.
	createOp, err := svc.Projects.Locations.Jobs.Create(parent, &run.GoogleCloudRunV2Job{
		Template: &run.GoogleCloudRunV2ExecutionTemplate{
			TaskCount: 1,
			Template: &run.GoogleCloudRunV2TaskTemplate{
				Containers: []*run.GoogleCloudRunV2Container{{
					Image:   "alpine:3.20",
					Command: []string{"echo"},
					Args:    []string{marker},
				}},
			},
		},
	}).JobId(jobID).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Jobs.Create: %v", err)
	}

	if !createOp.Done {
		t.Fatalf("create op not done: %+v", createOp)
	}

	// 2. Run the job — like `gcloud run jobs execute`. This runs the real
	//    container to completion via the Docker engine.
	runOp, err := svc.Projects.Locations.Jobs.Run(jobName, &run.GoogleCloudRunV2RunJobRequest{}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Jobs.Run: %v", err)
	}

	if !runOp.Done {
		t.Fatalf("run op not done: %+v", runOp)
	}

	if runOp.Error != nil {
		t.Fatalf("run op reported an error: %+v", runOp.Error)
	}

	// The Run LRO inlines the finished Execution as its response.
	var exec run.GoogleCloudRunV2Execution
	if err := json.Unmarshal(runOp.Response, &exec); err != nil {
		t.Fatalf("decode run response: %v (raw: %s)", err, runOp.Response)
	}

	if exec.Name == "" {
		t.Fatalf("run response carried no execution name: %s", runOp.Response)
	}

	if exec.SucceededCount != 1 {
		t.Fatalf("succeededCount = %d, want 1 (failed=%d) — the real container did not run to completion with exit 0",
			exec.SucceededCount, exec.FailedCount)
	}

	// 3. Read the Execution back by name — it must report the same success.
	got, err := svc.Projects.Locations.Jobs.Executions.Get(exec.Name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Executions.Get(%s): %v", exec.Name, err)
	}

	if got.SucceededCount != 1 {
		t.Fatalf("Executions.Get succeededCount = %d, want 1", got.SucceededCount)
	}

	// 4. Delete the job — the real container is torn down and no leak remains.
	if _, err := svc.Projects.Locations.Jobs.Delete(jobName).Context(ctx).Do(); err != nil {
		t.Fatalf("Jobs.Delete: %v", err)
	}
}
