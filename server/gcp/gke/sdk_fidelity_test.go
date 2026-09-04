// Reproduction tests for the AUDIT_GCP.md `## gke` findings: full operation
// shape, absolute selfLinks, a non-sentinel control-plane endpoint, populated
// cluster identity/network fields, and getServerConfig.

package gke_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"google.golang.org/api/container/v1"
	"google.golang.org/api/googleapi"
)

const selfLinkHost = "https://container.googleapis.com/v1/"

// TestSDKGKEOperationFullShape proves operations.get returns GKE's richer
// operation (operationType/targetLink/selfLink/zone/timestamps), not just the
// {name,done,status} the shared LRO poller used to shadow it with.
func TestSDKGKEOperationFullShape(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()
	loc := "us-central1"

	op, err := svc.Projects.Locations.Clusters.Create(parent(project, loc), &container.CreateClusterRequest{
		Cluster: &container.Cluster{Name: "prod"},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := svc.Projects.Locations.Operations.Get(parent(project, loc) + "/operations/" + op.Name).
		Context(ctx).Do()
	if err != nil {
		t.Fatalf("operations.get: %v", err)
	}

	if got.OperationType != "CREATE_CLUSTER" {
		t.Fatalf("operationType = %q, want CREATE_CLUSTER", got.OperationType)
	}

	if got.Status != "DONE" {
		t.Fatalf("status = %q, want DONE", got.Status)
	}

	if got.Zone != loc {
		t.Fatalf("zone = %q, want %q", got.Zone, loc)
	}

	if got.StartTime == "" || got.EndTime == "" {
		t.Fatalf("startTime/endTime empty: %q / %q", got.StartTime, got.EndTime)
	}

	if !strings.HasPrefix(got.SelfLink, selfLinkHost) {
		t.Fatalf("selfLink = %q, want %s prefix", got.SelfLink, selfLinkHost)
	}

	if !strings.HasPrefix(got.TargetLink, selfLinkHost) || !strings.Contains(got.TargetLink, "/clusters/prod") {
		t.Fatalf("targetLink = %q, want absolute URL ending /clusters/prod", got.TargetLink)
	}
}

// TestSDKGKEForeignOperationNotFound proves the store-aware Matches lets an
// operation the GKE mock never recorded fall through to the shared LRO poller
// (instead of GKE greedily claiming and 404ing it), and that the shared poller
// now 404s an operation name no service registered — real GCP returns NOT_FOUND
// for an unknown operation id rather than masking it as done.
func TestSDKGKEForeignOperationNotFound(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()
	loc := "us-central1"

	_, err := svc.Projects.Locations.Operations.Get(parent(project, loc) + "/operations/operation-not-gke").
		Context(ctx).Do()
	if err == nil {
		t.Fatal("operations.get (foreign): want 404, got success")
	}

	var gerr *googleapi.Error
	if !errors.As(err, &gerr) || gerr.Code != http.StatusNotFound {
		t.Fatalf("operations.get (foreign): want 404, got %v", err)
	}
}

// TestSDKGKEAbsoluteSelfLinks proves cluster and node-pool selfLinks are full
// https://container.googleapis.com/... URLs, not bare project paths.
func TestSDKGKEAbsoluteSelfLinks(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()
	loc := "us-central1"

	if _, err := svc.Projects.Locations.Clusters.Create(parent(project, loc), &container.CreateClusterRequest{
		Cluster: &container.Cluster{Name: "links"},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := svc.Projects.Locations.Clusters.Get(clusterName(project, loc, "links")).Context(ctx).Do()
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	wantCluster := selfLinkHost + "projects/" + project + "/locations/" + loc + "/clusters/links"
	if got.SelfLink != wantCluster {
		t.Fatalf("cluster selfLink = %q, want %q", got.SelfLink, wantCluster)
	}

	if len(got.NodePools) == 0 {
		t.Fatal("expected a default node pool")
	}

	if !strings.HasPrefix(got.NodePools[0].SelfLink, selfLinkHost) {
		t.Fatalf("nodePool selfLink = %q, want %s prefix", got.NodePools[0].SelfLink, selfLinkHost)
	}
}

// TestSDKGKEEndpointNotSentinel proves a fresh cluster reports a non-sentinel,
// IPv4-shaped control-plane endpoint (not GKE-DATAPLANE-NOT-IMPLEMENTED).
func TestSDKGKEEndpointNotSentinel(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()
	loc := "us-central1"

	if _, err := svc.Projects.Locations.Clusters.Create(parent(project, loc), &container.CreateClusterRequest{
		Cluster: &container.Cluster{Name: "reachable"},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := svc.Projects.Locations.Clusters.Get(clusterName(project, loc, "reachable")).Context(ctx).Do()
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if strings.Contains(got.Endpoint, "NOT-IMPLEMENTED") {
		t.Fatalf("endpoint still the sentinel: %q", got.Endpoint)
	}

	if strings.Count(got.Endpoint, ".") != 3 {
		t.Fatalf("endpoint = %q, want a dotted IPv4 address", got.Endpoint)
	}
}

// TestSDKGKEClusterIdentityFields proves id/zone/servicesIpv4Cidr/
// clusterIpv4Cidr/nodeIpv4CidrSize/currentNodeCount are populated.
func TestSDKGKEClusterIdentityFields(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()
	loc := "us-central1"

	if _, err := svc.Projects.Locations.Clusters.Create(parent(project, loc), &container.CreateClusterRequest{
		Cluster: &container.Cluster{Name: "fields", InitialNodeCount: 3},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := svc.Projects.Locations.Clusters.Get(clusterName(project, loc, "fields")).Context(ctx).Do()
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if got.Id == "" {
		t.Fatal("cluster id empty")
	}

	if got.Zone != loc {
		t.Fatalf("zone = %q, want %q", got.Zone, loc)
	}

	if got.ServicesIpv4Cidr == "" {
		t.Fatal("servicesIpv4Cidr empty")
	}

	if got.ClusterIpv4Cidr == "" {
		t.Fatal("clusterIpv4Cidr empty")
	}

	if got.NodeIpv4CidrSize == 0 {
		t.Fatal("nodeIpv4CidrSize zero")
	}

	if got.CurrentNodeCount == 0 {
		t.Fatal("currentNodeCount zero")
	}
}

// TestSDKGKEGetServerConfig proves getServerConfig returns valid/default
// versions instead of the old 501.
func TestSDKGKEGetServerConfig(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()
	loc := "us-central1"

	cfg, err := svc.Projects.Locations.GetServerConfig(parent(project, loc)).Context(ctx).Do()
	if err != nil {
		t.Fatalf("getServerConfig: %v", err)
	}

	if cfg.DefaultClusterVersion == "" {
		t.Fatal("defaultClusterVersion empty")
	}

	if len(cfg.ValidMasterVersions) == 0 {
		t.Fatal("validMasterVersions empty")
	}

	if len(cfg.ValidNodeVersions) == 0 {
		t.Fatal("validNodeVersions empty")
	}
}

// TestSDKGKEErrorMessageOmitsCodePrefix proves a GKE error's wire message
// carries only the human-readable text, not the internal cerrors code-name
// prefix (e.g. "cluster ... not found", not "NotFound: cluster ... not
// found") — real GKE never leaks its internal error taxonomy into the message
// an SDK surfaces to the caller. A black-box audit found every case in
// writeErr using err.Error() (which includes the "NotFound: "/"AlreadyExists:
// "/etc. prefix from cerrors.Error.Error()) instead of cerrors.Message(err).
func TestSDKGKEErrorMessageOmitsCodePrefix(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()
	loc := "us-central1"

	_, err := svc.Projects.Locations.Clusters.Get(parent(project, loc) + "/clusters/does-not-exist").
		Context(ctx).Do()
	if err == nil {
		t.Fatal("clusters.get (missing): want error, got success")
	}

	var gerr *googleapi.Error
	if !errors.As(err, &gerr) || gerr.Code != http.StatusNotFound {
		t.Fatalf("clusters.get (missing): want 404, got %v", err)
	}

	for _, prefix := range []string{"NotFound:", "AlreadyExists:", "InvalidArgument:", "FailedPrecondition:", "Internal:"} {
		if strings.Contains(gerr.Message, prefix) {
			t.Fatalf("error message %q leaks internal code prefix %q", gerr.Message, prefix)
		}
	}

	if gerr.Message != `cluster "does-not-exist" not found in "us-central1"` {
		t.Fatalf("unexpected error message: %q", gerr.Message)
	}
}

// TestSDKGKEListNodePoolsMissingClusterNotFound proves nodePools.list on a
// cluster that doesn't exist 404s, matching real GKE and every other GKE
// verb that reads a cluster/pool. A black-box audit found the wire handler
// silently answering an empty collection (200) instead.
func TestSDKGKEListNodePoolsMissingClusterNotFound(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()
	loc := "us-central1"

	_, err := svc.Projects.Locations.Clusters.NodePools.List(
		parent(project, loc) + "/clusters/does-not-exist").Context(ctx).Do()
	if err == nil {
		t.Fatal("nodePools.list (missing cluster): want 404, got success")
	}

	var gerr *googleapi.Error
	if !errors.As(err, &gerr) || gerr.Code != http.StatusNotFound {
		t.Fatalf("nodePools.list (missing cluster): want 404, got %v", err)
	}
}
