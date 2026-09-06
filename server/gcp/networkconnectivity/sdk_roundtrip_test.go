package networkconnectivity_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	networkconnectivity "google.golang.org/api/networkconnectivity/v1"
	"google.golang.org/api/option"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

func newSDKClient(t *testing.T) (*networkconnectivity.Service, string) {
	t.Helper()

	cloud := cloudemu.NewGCP()
	srv := gcpserver.NewFromProvider(cloud)

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	svc, err := networkconnectivity.NewService(context.Background(),
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("networkconnectivity.NewService: %v", err)
	}

	return svc, "mock-project"
}

func TestSDKHubLifecycle(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()
	parent := "projects/" + project + "/locations/global"
	name := parent + "/hubs/h1"

	op, err := svc.Projects.Locations.Global.Hubs.Create(parent, &networkconnectivity.Hub{
		Description: "primary hub",
		Labels:      map[string]string{"team": "net"},
	}).HubId("h1").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Hubs.Create: %v", err)
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

	got, err := svc.Projects.Locations.Global.Hubs.Get(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Hubs.Get: %v", err)
	}

	if got.Name != name || got.UniqueId == "" || got.State != "ACTIVE" ||
		got.CreateTime == "" || got.UpdateTime == "" {
		t.Fatalf("computed fields wrong: name=%q uniqueId=%q state=%q create=%q update=%q",
			got.Name, got.UniqueId, got.State, got.CreateTime, got.UpdateTime)
	}

	if got.Description != "primary hub" || got.Labels["team"] != "net" {
		t.Fatalf("body not round-tripped: %+v", got)
	}

	// Computed fields stable across reads (no Terraform drift).
	second, err := svc.Projects.Locations.Global.Hubs.Get(name).Do()
	if err != nil {
		t.Fatalf("second Get: %v", err)
	}

	if second.UniqueId != got.UniqueId || second.CreateTime != got.CreateTime ||
		second.State != got.State {
		t.Fatalf("computed fields unstable across reads (drift): %+v vs %+v", got, second)
	}

	// Patch description + labels via updateMask -> LRO, applied.
	patchOp, err := svc.Projects.Locations.Global.Hubs.Patch(name, &networkconnectivity.Hub{
		Description: "primary hub v2",
		Labels:      map[string]string{"team": "net", "env": "prod"},
	}).UpdateMask("description,labels").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Hubs.Patch: %v", err)
	}

	if !patchOp.Done {
		t.Fatalf("patch operation not done")
	}

	updated, err := svc.Projects.Locations.Global.Hubs.Get(name).Do()
	if err != nil {
		t.Fatalf("Get after patch: %v", err)
	}

	if updated.Description != "primary hub v2" || updated.Labels["env"] != "prod" {
		t.Fatalf("patch not applied: %+v", updated)
	}

	// uniqueId/createTime survive a patch.
	if updated.UniqueId != got.UniqueId || updated.CreateTime != got.CreateTime {
		t.Fatalf("patch mutated immutable computed fields")
	}
}

func TestSDKSpokeLifecycle(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()
	global := "projects/" + project + "/locations/global"
	parent := "projects/" + project + "/locations/us-central1"
	spokeName := parent + "/spokes/s1"

	if _, err := svc.Projects.Locations.Global.Hubs.Create(global, &networkconnectivity.Hub{}).
		HubId("h1").Do(); err != nil {
		t.Fatalf("seed hub: %v", err)
	}

	want := &networkconnectivity.Spoke{
		Hub:         global + "/hubs/h1",
		Description: "vpc spoke",
		Labels:      map[string]string{"env": "prod"},
		LinkedVpcNetwork: &networkconnectivity.LinkedVpcNetwork{
			Uri:                 "projects/" + project + "/global/networks/default",
			ExcludeExportRanges: []string{"10.0.0.0/24"},
		},
	}

	op, err := svc.Projects.Locations.Spokes.Create(parent, want).SpokeId("s1").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Spokes.Create: %v", err)
	}

	if !op.Done {
		t.Fatalf("create operation not done")
	}

	got, err := svc.Projects.Locations.Spokes.Get(spokeName).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Spokes.Get: %v", err)
	}

	if got.Name != spokeName || got.UniqueId == "" || got.State != "ACTIVE" || got.CreateTime == "" {
		t.Fatalf("computed fields wrong: %+v", got)
	}

	if got.Hub != global+"/hubs/h1" || got.Description != "vpc spoke" {
		t.Fatalf("body not round-tripped: %+v", got)
	}

	if got.LinkedVpcNetwork == nil || !strings.HasSuffix(got.LinkedVpcNetwork.Uri, "/networks/default") ||
		len(got.LinkedVpcNetwork.ExcludeExportRanges) != 1 {
		t.Fatalf("linkedVpcNetwork not round-tripped: %+v", got.LinkedVpcNetwork)
	}

	// List (scoped to the spoke's region).
	list, err := svc.Projects.Locations.Spokes.List(parent).Do()
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(list.Spokes) != 1 || !strings.HasSuffix(list.Spokes[0].Name, "/s1") {
		t.Fatalf("list = %+v", list.Spokes)
	}

	// Patch description -> LRO; linkedVpcNetwork untouched.
	patchOp, err := svc.Projects.Locations.Spokes.Patch(spokeName, &networkconnectivity.Spoke{
		Description: "vpc spoke v2",
	}).UpdateMask("description").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Spokes.Patch: %v", err)
	}

	if !patchOp.Done {
		t.Fatalf("patch operation not done")
	}

	updated, err := svc.Projects.Locations.Spokes.Get(spokeName).Do()
	if err != nil {
		t.Fatalf("Get after patch: %v", err)
	}

	if updated.Description != "vpc spoke v2" || updated.LinkedVpcNetwork == nil {
		t.Fatalf("masked patch wrong: %+v", updated)
	}

	delOp, err := svc.Projects.Locations.Spokes.Delete(spokeName).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Spokes.Delete: %v", err)
	}

	if !delOp.Done {
		t.Fatalf("delete operation not done")
	}

	if _, err := svc.Projects.Locations.Spokes.Get(spokeName).Do(); err == nil {
		t.Fatalf("expected 404 after delete")
	}
}

func TestSDKSpokeLinkOneofRejected(t *testing.T) {
	svc, project := newSDKClient(t)
	parent := "projects/" + project + "/locations/us-central1"

	// Zero linked_* blocks -> 400.
	if _, err := svc.Projects.Locations.Spokes.Create(parent, &networkconnectivity.Spoke{
		Hub: "projects/" + project + "/locations/global/hubs/h1",
	}).SpokeId("nolink").Do(); err == nil {
		t.Fatalf("expected 400 for a spoke with no linked_* block")
	}

	// Two linked_* blocks -> 400.
	if _, err := svc.Projects.Locations.Spokes.Create(parent, &networkconnectivity.Spoke{
		Hub:              "projects/" + project + "/locations/global/hubs/h1",
		LinkedVpcNetwork: &networkconnectivity.LinkedVpcNetwork{Uri: "projects/x/global/networks/default"},
		LinkedVpnTunnels: &networkconnectivity.LinkedVpnTunnels{Uris: []string{"t1"}},
	}).SpokeId("twolinks").Do(); err == nil {
		t.Fatalf("expected 400 for a spoke with multiple linked_* blocks")
	}
}

func TestSDKHubNotFoundAndDuplicate(t *testing.T) {
	svc, project := newSDKClient(t)
	parent := "projects/" + project + "/locations/global"

	if _, err := svc.Projects.Locations.Global.Hubs.Get(parent + "/hubs/ghost").Do(); err == nil {
		t.Fatalf("expected NOT_FOUND")
	}

	if _, err := svc.Projects.Locations.Global.Hubs.Create(parent, &networkconnectivity.Hub{}).
		HubId("dup").Do(); err != nil {
		t.Fatalf("first create: %v", err)
	}

	if _, err := svc.Projects.Locations.Global.Hubs.Create(parent, &networkconnectivity.Hub{}).
		HubId("dup").Do(); err == nil {
		t.Fatalf("expected ALREADY_EXISTS on duplicate create")
	}
}
