package cloudrun

import (
	"context"
	"strings"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/cloudrun/driver"
)

func svcCfg() driver.ServiceConfig {
	return driver.ServiceConfig{
		Name:           "web",
		Location:       "us-central1",
		ServiceAccount: "web@demo.iam.gserviceaccount.com",
		Timeout:        "300s",
		Scaling:        &driver.ServiceScaling{MinInstanceCount: 1, MaxInstanceCount: 3},
		Containers:     []driver.Container{{Image: "gcr.io/demo/web:v1", Ports: []int{8080}}},
	}
}

func TestCreateServiceReconciles(t *testing.T) {
	m := newMock(t, nil)
	ctx := context.Background()

	svc, err := m.CreateService(ctx, svcCfg())
	if err != nil {
		t.Fatalf("CreateService: %v", err)
	}

	if svc.UID == "" || svc.Generation != 1 || svc.ObservedGeneration != 1 {
		t.Fatalf("unexpected service: %+v", svc)
	}

	if !strings.HasPrefix(svc.URI, "https://web-") || !strings.HasSuffix(svc.URI, ".us-central1.run.app") {
		t.Fatalf("uri = %q", svc.URI)
	}

	if !strings.HasPrefix(svc.LatestReadyRevision, "web-00001-") {
		t.Fatalf("latestReadyRevision = %q", svc.LatestReadyRevision)
	}

	if len(svc.Traffic) != 1 || svc.Traffic[0].Percent != 100 {
		t.Fatalf("traffic = %+v", svc.Traffic)
	}

	if svc.TerminalCondition == nil || svc.TerminalCondition.State != "CONDITION_SUCCEEDED" {
		t.Fatalf("terminalCondition = %+v", svc.TerminalCondition)
	}

	if _, err := m.CreateService(ctx, svcCfg()); !cerrors.IsAlreadyExists(err) {
		t.Fatalf("duplicate create err = %v, want AlreadyExists", err)
	}
}

func TestUpdateServiceMaterializesRevision(t *testing.T) {
	m := newMock(t, nil)
	ctx := context.Background()

	if _, err := m.CreateService(ctx, svcCfg()); err != nil {
		t.Fatalf("CreateService: %v", err)
	}

	cfg := svcCfg()
	cfg.Containers[0].Image = "gcr.io/demo/web:v2"

	svc, err := m.UpdateService(ctx, cfg)
	if err != nil {
		t.Fatalf("UpdateService: %v", err)
	}

	if svc.Generation != 2 || !strings.HasPrefix(svc.LatestReadyRevision, "web-00002-") {
		t.Fatalf("after update gen=%d rev=%q", svc.Generation, svc.LatestReadyRevision)
	}

	revs, err := m.ListRevisions(ctx, "web")
	if err != nil || len(revs) != 2 {
		t.Fatalf("ListRevisions: got %d err=%v", len(revs), err)
	}

	if _, err := m.UpdateService(ctx, driver.ServiceConfig{Name: "ghost"}); !cerrors.IsNotFound(err) {
		t.Fatalf("update missing err = %v, want NotFound", err)
	}
}

func TestDeleteServiceRemovesRevisions(t *testing.T) {
	m := newMock(t, nil)
	ctx := context.Background()

	if _, err := m.CreateService(ctx, svcCfg()); err != nil {
		t.Fatalf("CreateService: %v", err)
	}

	if err := m.DeleteService(ctx, "web"); err != nil {
		t.Fatalf("DeleteService: %v", err)
	}

	if _, err := m.GetService(ctx, "web"); !cerrors.IsNotFound(err) {
		t.Fatalf("GetService after delete err = %v, want NotFound", err)
	}

	if _, err := m.ListRevisions(ctx, "web"); !cerrors.IsNotFound(err) {
		t.Fatalf("ListRevisions after delete err = %v, want NotFound", err)
	}
}

func TestUpdateJobBumpsGeneration(t *testing.T) {
	m := newMock(t, nil)
	ctx := context.Background()

	if _, err := m.CreateJob(ctx, jobCfg()); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	cfg := jobCfg()
	cfg.MaxRetries = 7
	cfg.Timeout = "600s"

	job, err := m.UpdateJob(ctx, cfg)
	if err != nil {
		t.Fatalf("UpdateJob: %v", err)
	}

	if job.Generation != 2 || job.MaxRetries != 7 || job.Timeout != "600s" {
		t.Fatalf("after update: gen=%d maxRetries=%d timeout=%q", job.Generation, job.MaxRetries, job.Timeout)
	}

	if _, err := m.UpdateJob(ctx, driver.JobConfig{Name: "ghost"}); !cerrors.IsNotFound(err) {
		t.Fatalf("update missing err = %v, want NotFound", err)
	}
}

func TestListExecutionsFiltersByJob(t *testing.T) {
	m := newMock(t, nil)
	ctx := context.Background()

	if _, err := m.CreateJob(ctx, jobCfg()); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	if _, err := m.RunJob(ctx, "batch"); err != nil {
		t.Fatalf("RunJob: %v", err)
	}

	execs, err := m.ListExecutions(ctx, "batch")
	if err != nil || len(execs) != 1 {
		t.Fatalf("ListExecutions: got %d err=%v", len(execs), err)
	}

	if execs[0].JobName != "batch" {
		t.Fatalf("execution jobName = %q", execs[0].JobName)
	}

	if _, err := m.ListExecutions(ctx, "ghost"); !cerrors.IsNotFound(err) {
		t.Fatalf("ListExecutions missing job err = %v, want NotFound", err)
	}
}
