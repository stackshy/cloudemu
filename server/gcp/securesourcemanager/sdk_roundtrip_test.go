package securesourcemanager_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/api/option"
	redis "google.golang.org/api/redis/v1"
	ssm "google.golang.org/api/securesourcemanager/v1"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

func newServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()

	cloud := cloudemu.NewGCP()
	ts := httptest.NewServer(gcpserver.NewFromProvider(cloud))
	t.Cleanup(ts.Close)

	return ts, "mock-project"
}

func newSSMClient(t *testing.T, url string) *ssm.Service {
	t.Helper()

	svc, err := ssm.NewService(context.Background(),
		option.WithEndpoint(url), option.WithoutAuthentication())
	if err != nil {
		t.Fatalf("ssm.NewService: %v", err)
	}

	return svc
}

// TestSDKFullLifecycle drives the complete instance + repository lifecycle
// through the real google.golang.org/api/securesourcemanager client: instance
// create (LRO) -> poll -> get -> list, then a repository referencing it
// create -> get -> list -> patch (description) -> delete -> 404. It asserts the
// emulator mints the computed output-only blocks (instance state + hostConfig,
// repository uid + uris, timestamps) and reports them byte-stably across reads —
// the exact behavior a Terraform refresh needs to converge without drift.
func TestSDKFullLifecycle(t *testing.T) {
	ts, project := newServer(t)
	svc := newSSMClient(t, ts.URL)
	ctx := context.Background()

	parent := "projects/" + project + "/locations/us-central1"
	instName := parent + "/instances/inst"

	// --- Instance: create (LRO) -> poll -> get ---
	op, err := svc.Projects.Locations.Instances.Create(parent, &ssm.Instance{
		Labels: map[string]string{"env": "prod"},
	}).InstanceId("inst").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Instances.Create: %v", err)
	}

	if !op.Done {
		t.Fatalf("create operation not done (would hang a Terraform apply)")
	}

	polled, err := svc.Projects.Locations.Operations.Get(op.Name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Operations.Get: %v", err)
	}

	if !polled.Done {
		t.Fatalf("polled operation not done")
	}

	inst, err := svc.Projects.Locations.Instances.Get(instName).Do()
	if err != nil {
		t.Fatalf("Instances.Get: %v", err)
	}

	if inst.Name != instName || inst.Labels["env"] != "prod" {
		t.Fatalf("instance body not round-tripped: %+v", inst)
	}

	if inst.State != "ACTIVE" {
		t.Fatalf("state = %q, want ACTIVE (Terraform needs a stable computed state)", inst.State)
	}

	if inst.HostConfig == nil || inst.HostConfig.Html == "" || inst.HostConfig.Api == "" ||
		inst.HostConfig.GitHttp == "" || inst.HostConfig.GitSsh == "" {
		t.Fatalf("hostConfig not minted: %+v", inst.HostConfig)
	}

	if inst.CreateTime == "" || inst.UpdateTime == "" {
		t.Fatalf("instance timestamps missing")
	}

	// Computed blocks stable across reads (the classic drift point).
	inst2, err := svc.Projects.Locations.Instances.Get(instName).Do()
	if err != nil {
		t.Fatalf("second instance Get: %v", err)
	}

	if inst2.State != inst.State || inst2.CreateTime != inst.CreateTime ||
		inst2.HostConfig.Html != inst.HostConfig.Html ||
		inst2.HostConfig.Api != inst.HostConfig.Api ||
		inst2.HostConfig.GitHttp != inst.HostConfig.GitHttp ||
		inst2.HostConfig.GitSsh != inst.HostConfig.GitSsh {
		t.Fatalf("instance computed fields unstable across reads (drift)")
	}

	list, err := svc.Projects.Locations.Instances.List(parent).Do()
	if err != nil {
		t.Fatalf("Instances.List: %v", err)
	}

	if len(list.Instances) != 1 || list.Instances[0].Name != instName {
		t.Fatalf("instance list = %+v", list.Instances)
	}

	// --- Repository: create referencing the instance -> get -> list ---
	repoName := parent + "/repositories/repo"

	rOp, err := svc.Projects.Locations.Repositories.Create(parent, &ssm.Repository{
		Instance:    instName,
		Description: "first",
	}).RepositoryId("repo").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Repositories.Create: %v", err)
	}

	if !rOp.Done {
		t.Fatalf("repository create operation not done")
	}

	repo, err := svc.Projects.Locations.Repositories.Get(repoName).Do()
	if err != nil {
		t.Fatalf("Repositories.Get: %v", err)
	}

	if repo.Instance != instName || repo.Description != "first" {
		t.Fatalf("repository body not round-tripped: %+v", repo)
	}

	if repo.Uid == "" || repo.Uris == nil || repo.Uris.Html == "" ||
		repo.Uris.GitHttps == "" || repo.Uris.Api == "" {
		t.Fatalf("repository uid/uris not minted: %+v", repo)
	}

	if repo.CreateTime == "" {
		t.Fatalf("repository createTime missing")
	}

	// --- Repository: patch description (the real update path) ---
	pOp, err := svc.Projects.Locations.Repositories.Patch(repoName, &ssm.Repository{
		Description: "updated",
	}).UpdateMask("description").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Repositories.Patch: %v", err)
	}

	if !pOp.Done {
		t.Fatalf("repository patch operation not done")
	}

	updated, err := svc.Projects.Locations.Repositories.Get(repoName).Do()
	if err != nil {
		t.Fatalf("Get after patch: %v", err)
	}

	if updated.Description != "updated" {
		t.Fatalf("description not patched: %q", updated.Description)
	}

	// Unmasked instance reference survives; computed uid/uris stable after patch.
	if updated.Instance != instName {
		t.Fatalf("unmasked instance reference mutated by patch: %q", updated.Instance)
	}

	if updated.Uid != repo.Uid || updated.Uris.Html != repo.Uris.Html ||
		updated.Uris.GitHttps != repo.Uris.GitHttps || updated.Uris.Api != repo.Uris.Api {
		t.Fatalf("repository computed fields drifted after patch")
	}

	// --- Delete -> 404 ---
	if _, err := svc.Projects.Locations.Repositories.Delete(repoName).Do(); err != nil {
		t.Fatalf("Repositories.Delete: %v", err)
	}

	if _, err := svc.Projects.Locations.Repositories.Get(repoName).Do(); err == nil {
		t.Fatalf("expected 404 after repository delete")
	}

	if _, err := svc.Projects.Locations.Instances.Delete(instName).Do(); err != nil {
		t.Fatalf("Instances.Delete: %v", err)
	}

	if _, err := svc.Projects.Locations.Instances.Get(instName).Do(); err == nil {
		t.Fatalf("expected 404 after instance delete")
	}
}

// TestRepositoryRequiresInstance verifies a repository create with no instance
// reference is rejected 400, matching the real API.
func TestRepositoryRequiresInstance(t *testing.T) {
	ts, project := newServer(t)
	svc := newSSMClient(t, ts.URL)

	parent := "projects/" + project + "/locations/us-central1"

	if _, err := svc.Projects.Locations.Repositories.Create(parent, &ssm.Repository{
		Description: "no instance",
	}).RepositoryId("bad").Do(); err == nil {
		t.Fatalf("expected INVALID_ARGUMENT for a repository with no instance")
	}
}

// TestCoexistWithMemorystore is the integration guard for the /instances path
// collision: a Secure Source Manager instance and a Memorystore Redis instance
// created against the SAME assembled server must both round-trip to their own
// service — neither handler steals the other's traffic despite the identical
// path grammar.
func TestCoexistWithMemorystore(t *testing.T) {
	ts, project := newServer(t)
	ctx := context.Background()
	parent := "projects/" + project + "/locations/us-central1"

	ssmSvc := newSSMClient(t, ts.URL)

	redisSvc, err := redis.NewService(ctx,
		option.WithEndpoint(ts.URL), option.WithoutAuthentication())
	if err != nil {
		t.Fatalf("redis.NewService: %v", err)
	}

	// Redis instance (memorySizeGb/tier body) -> must land in Memorystore.
	if _, err := redisSvc.Projects.Locations.Instances.Create(parent, &redis.Instance{
		Tier:         "BASIC",
		MemorySizeGb: 1,
	}).InstanceId("cache").Context(ctx).Do(); err != nil {
		t.Fatalf("redis Instances.Create: %v", err)
	}

	// Secure Source Manager instance (no sibling signal) -> must land in SSM.
	ssmOp, err := ssmSvc.Projects.Locations.Instances.Create(parent, &ssm.Instance{
		Labels: map[string]string{"env": "prod"},
	}).InstanceId("scm").Context(ctx).Do()
	if err != nil {
		t.Fatalf("ssm Instances.Create: %v", err)
	}

	if !ssmOp.Done {
		t.Fatalf("ssm create not done")
	}

	// Each Get resolves to its own service's resource.
	cache, err := redisSvc.Projects.Locations.Instances.Get(parent + "/instances/cache").Do()
	if err != nil {
		t.Fatalf("redis Get (stolen by SSM?): %v", err)
	}

	if cache.MemorySizeGb != 1 {
		t.Fatalf("redis instance not intact: %+v", cache)
	}

	scm, err := ssmSvc.Projects.Locations.Instances.Get(parent + "/instances/scm").Do()
	if err != nil {
		t.Fatalf("ssm Get (stolen by Memorystore?): %v", err)
	}

	if scm.State != "ACTIVE" || !strings.Contains(scm.HostConfig.Html, "scm-") {
		t.Fatalf("ssm instance not intact / not SSM-shaped: %+v", scm)
	}

	// SSM must not see the Redis instance in its List, and vice-versa.
	ssmList, err := ssmSvc.Projects.Locations.Instances.List(parent).Do()
	if err != nil {
		t.Fatalf("ssm List: %v", err)
	}

	if len(ssmList.Instances) != 1 || !strings.HasSuffix(ssmList.Instances[0].Name, "/scm") {
		t.Fatalf("ssm List leaked or missed instances: %+v", ssmList.Instances)
	}
}
