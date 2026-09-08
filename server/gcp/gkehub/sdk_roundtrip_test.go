package gkehub_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	certificatemanager "google.golang.org/api/certificatemanager/v1"
	gkehub "google.golang.org/api/gkehub/v1"
	"google.golang.org/api/option"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

func newServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()

	cloud := cloudemu.NewGCP()
	srv := gcpserver.NewFromProvider(cloud)

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	return ts, "mock-project"
}

func newSDKClient(t *testing.T) (*gkehub.Service, string) {
	t.Helper()

	ts, project := newServer(t)

	svc, err := gkehub.NewService(context.Background(),
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("gkehub.NewService: %v", err)
	}

	return svc, project
}

func TestSDKMembershipLifecycle(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()
	parent := "projects/" + project + "/locations/global" // memberships are global
	name := parent + "/memberships/prod"

	want := &gkehub.Membership{
		Description: "prod cluster",
		ExternalId:  "ext-1",
		Endpoint: &gkehub.MembershipEndpoint{
			GkeCluster: &gkehub.GkeCluster{ResourceLink: "//container.googleapis.com/projects/mock-project/locations/us-central1/clusters/prod"},
		},
		Authority: &gkehub.Authority{Issuer: "https://container.googleapis.com/v1/projects/mock-project/locations/us-central1/clusters/prod"},
		Labels:    map[string]string{"env": "prod"},
	}

	op, err := svc.Projects.Locations.Memberships.Create(parent, want).MembershipId("prod").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Memberships.Create: %v", err)
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

	got, err := svc.Projects.Locations.Memberships.Get(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Memberships.Get: %v", err)
	}

	if got.UniqueId == "" || got.CreateTime == "" {
		t.Fatalf("computed fields missing: uniqueId=%q createTime=%q", got.UniqueId, got.CreateTime)
	}

	if got.State == nil || got.State.Code != "READY" {
		t.Fatalf("membership did not settle READY: %+v", got.State)
	}

	if got.Endpoint == nil || got.Endpoint.GkeCluster == nil ||
		!strings.HasSuffix(got.Endpoint.GkeCluster.ResourceLink, "/clusters/prod") {
		t.Fatalf("endpoint not round-tripped: %+v", got.Endpoint)
	}

	if got.Authority == nil || !strings.Contains(got.Authority.Issuer, "container.googleapis.com") {
		t.Fatalf("authority not round-tripped: %+v", got.Authority)
	}

	if got.Labels["env"] != "prod" || got.Description != "prod cluster" || got.ExternalId != "ext-1" {
		t.Fatalf("input fields not round-tripped: %+v", got)
	}

	// Byte-stable computed fields across reads (no Terraform drift).
	second, err := svc.Projects.Locations.Memberships.Get(name).Do()
	if err != nil {
		t.Fatalf("second Get: %v", err)
	}

	if second.UniqueId != got.UniqueId || second.CreateTime != got.CreateTime ||
		second.State.Code != "READY" {
		t.Fatalf("computed fields unstable across reads (drift): %+v vs %+v", got, second)
	}

	list, err := svc.Projects.Locations.Memberships.List(parent).Do()
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(list.Resources) != 1 || !strings.HasSuffix(list.Resources[0].Name, "/memberships/prod") {
		t.Fatalf("list = %+v", list.Resources)
	}

	patchOp, err := svc.Projects.Locations.Memberships.Patch(name, &gkehub.Membership{
		Labels: map[string]string{"env": "prod", "team": "sre"},
	}).UpdateMask("labels").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Memberships.Patch: %v", err)
	}

	if !patchOp.Done {
		t.Fatalf("patch operation not done")
	}

	updated, err := svc.Projects.Locations.Memberships.Get(name).Do()
	if err != nil {
		t.Fatalf("Get after patch: %v", err)
	}

	if updated.Labels["team"] != "sre" {
		t.Fatalf("patch not applied: %+v", updated.Labels)
	}

	if updated.Endpoint == nil || updated.Endpoint.GkeCluster == nil {
		t.Fatalf("unmasked endpoint lost by patch: %+v", updated.Endpoint)
	}

	delOp, err := svc.Projects.Locations.Memberships.Delete(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Memberships.Delete: %v", err)
	}

	if !delOp.Done {
		t.Fatalf("delete operation not done")
	}

	if _, err := svc.Projects.Locations.Memberships.Get(name).Do(); err == nil {
		t.Fatalf("expected 404 after delete")
	}
}

func TestSDKFeatureLifecycle(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()
	parent := "projects/" + project + "/locations/global"
	name := parent + "/features/multiclusteringress"

	want := &gkehub.Feature{
		Labels: map[string]string{"foo": "bar"},
		Spec: &gkehub.CommonFeatureSpec{
			Multiclusteringress: &gkehub.MultiClusterIngressFeatureSpec{
				ConfigMembership: parent + "/memberships/config",
			},
		},
	}

	op, err := svc.Projects.Locations.Features.Create(parent, want).FeatureId("multiclusteringress").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Features.Create: %v", err)
	}

	if !op.Done {
		t.Fatalf("create operation not done")
	}

	got, err := svc.Projects.Locations.Features.Get(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Features.Get: %v", err)
	}

	if got.ResourceState == nil || got.ResourceState.State != "ACTIVE" {
		t.Fatalf("feature resourceState not ACTIVE: %+v", got.ResourceState)
	}

	if got.Spec == nil || got.Spec.Multiclusteringress == nil ||
		!strings.HasSuffix(got.Spec.Multiclusteringress.ConfigMembership, "/memberships/config") {
		t.Fatalf("feature spec not round-tripped: %+v", got.Spec)
	}

	if got.Labels["foo"] != "bar" {
		t.Fatalf("labels not round-tripped")
	}

	// Patch the spec via updateMask -> LRO applied, labels untouched.
	patchOp, err := svc.Projects.Locations.Features.Patch(name, &gkehub.Feature{
		Spec: &gkehub.CommonFeatureSpec{
			Multiclusteringress: &gkehub.MultiClusterIngressFeatureSpec{ConfigMembership: parent + "/memberships/config2"},
		},
	}).UpdateMask("spec").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Features.Patch: %v", err)
	}

	if !patchOp.Done {
		t.Fatalf("patch operation not done")
	}

	updated, err := svc.Projects.Locations.Features.Get(name).Do()
	if err != nil {
		t.Fatalf("Get after patch: %v", err)
	}

	if !strings.HasSuffix(updated.Spec.Multiclusteringress.ConfigMembership, "/memberships/config2") {
		t.Fatalf("spec not updated: %+v", updated.Spec)
	}

	if updated.Labels["foo"] != "bar" {
		t.Fatalf("labels mutated by masked patch: %+v", updated.Labels)
	}

	if _, err := svc.Projects.Locations.Features.Delete(name).Do(); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err := svc.Projects.Locations.Features.Get(name).Do(); err == nil {
		t.Fatalf("expected 404 after delete")
	}
}

func TestSDKFleetLifecycle(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()
	parent := "projects/" + project + "/locations/global"
	name := parent + "/fleets/default"

	// Fleet is the singleton "default" — create takes no fleetId.
	op, err := svc.Projects.Locations.Fleets.Create(parent, &gkehub.Fleet{DisplayName: "prod fleet"}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Fleets.Create: %v", err)
	}

	if !op.Done {
		t.Fatalf("create operation not done")
	}

	got, err := svc.Projects.Locations.Fleets.Get(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Fleets.Get: %v", err)
	}

	if got.Uid == "" || got.CreateTime == "" {
		t.Fatalf("computed fields missing: uid=%q createTime=%q", got.Uid, got.CreateTime)
	}

	if got.State == nil || got.State.Code != "READY" {
		t.Fatalf("fleet did not settle READY: %+v", got.State)
	}

	if got.DisplayName != "prod fleet" || !strings.HasSuffix(got.Name, "/fleets/default") {
		t.Fatalf("fleet fields wrong: name=%q displayName=%q", got.Name, got.DisplayName)
	}

	// Stable across reads.
	second, err := svc.Projects.Locations.Fleets.Get(name).Do()
	if err != nil {
		t.Fatalf("second Get: %v", err)
	}

	if second.Uid != got.Uid || second.CreateTime != got.CreateTime {
		t.Fatalf("computed fleet fields unstable (drift): %+v vs %+v", got, second)
	}

	if _, err := svc.Projects.Locations.Fleets.Patch(name, &gkehub.Fleet{DisplayName: "prod fleet v2"}).
		UpdateMask("display_name").Do(); err != nil {
		t.Fatalf("Fleets.Patch: %v", err)
	}

	updated, err := svc.Projects.Locations.Fleets.Get(name).Do()
	if err != nil {
		t.Fatalf("Get after patch: %v", err)
	}

	if updated.DisplayName != "prod fleet v2" {
		t.Fatalf("displayName after patch = %q", updated.DisplayName)
	}

	if _, err := svc.Projects.Locations.Fleets.Delete(name).Do(); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err := svc.Projects.Locations.Fleets.Get(name).Do(); err == nil {
		t.Fatalf("expected 404 after delete")
	}
}

func TestSDKMembershipNotFoundAndDuplicate(t *testing.T) {
	svc, project := newSDKClient(t)
	parent := "projects/" + project + "/locations/global"

	if _, err := svc.Projects.Locations.Memberships.Get(parent + "/memberships/ghost").Do(); err == nil {
		t.Fatalf("expected NOT_FOUND")
	}

	if _, err := svc.Projects.Locations.Memberships.Create(parent, &gkehub.Membership{}).MembershipId("dup").Do(); err != nil {
		t.Fatalf("first create: %v", err)
	}

	if _, err := svc.Projects.Locations.Memberships.Create(parent, &gkehub.Membership{}).MembershipId("dup").Do(); err == nil {
		t.Fatalf("expected ALREADY_EXISTS on duplicate create")
	}
}

// TestSiblingLRONonRegression proves that registering the gkehub handler (which
// mints location-scoped operations) does not steal a sibling GCP service's
// operation poll: a certificatemanager operation created on the SAME assembled
// server still resolves through the shared LRO poller.
func TestSiblingLRONonRegression(t *testing.T) {
	ts, project := newServer(t)
	ctx := context.Background()

	cm, err := certificatemanager.NewService(ctx, option.WithEndpoint(ts.URL), option.WithoutAuthentication())
	if err != nil {
		t.Fatalf("certificatemanager.NewService: %v", err)
	}

	parent := "projects/" + project + "/locations/global"

	op, err := cm.Projects.Locations.CertificateMaps.Create(parent, &certificatemanager.CertificateMap{}).
		CertificateMapId("m").Context(ctx).Do()
	if err != nil {
		t.Fatalf("CertificateMaps.Create: %v", err)
	}

	polled, err := cm.Projects.Locations.Operations.Get(op.Name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("sibling Operations.Get stolen/404 by gkehub: %v", err)
	}

	if !polled.Done {
		t.Fatalf("sibling operation not done")
	}

	if _, err := cm.Projects.Locations.CertificateMaps.Get(parent + "/certificateMaps/m").Do(); err != nil {
		t.Fatalf("sibling resource Get failed: %v", err)
	}
}
