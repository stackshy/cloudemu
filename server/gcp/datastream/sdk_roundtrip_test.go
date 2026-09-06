package datastream_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	datastream "google.golang.org/api/datastream/v1"
	"google.golang.org/api/option"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

func newSDKClient(t *testing.T) (*datastream.Service, string) {
	t.Helper()

	cloud := cloudemu.NewGCP()
	srv := gcpserver.NewFromProvider(cloud)

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	svc, err := datastream.NewService(context.Background(),
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("datastream.NewService: %v", err)
	}

	return svc, "mock-project"
}

func TestSDKConnectionProfileLifecycle(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()
	parent := "projects/" + project + "/locations/us-central1"
	name := parent + "/connectionProfiles/src"

	want := &datastream.ConnectionProfile{
		DisplayName: "postgres source",
		PostgresqlProfile: &datastream.PostgresqlProfile{
			Hostname: "10.0.0.5", Port: 5432, Username: "replicator", Database: "app",
		},
		Labels: map[string]string{"team": "data"},
	}

	op, err := svc.Projects.Locations.ConnectionProfiles.Create(parent, want).
		ConnectionProfileId("src").Context(ctx).Do()
	if err != nil {
		t.Fatalf("ConnectionProfiles.Create: %v", err)
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

	got, err := svc.Projects.Locations.ConnectionProfiles.Get(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("ConnectionProfiles.Get: %v", err)
	}

	if got.Name != name || got.CreateTime == "" || got.UpdateTime == "" {
		t.Fatalf("computed fields missing: name=%q createTime=%q updateTime=%q", got.Name, got.CreateTime, got.UpdateTime)
	}

	if got.PostgresqlProfile == nil || got.PostgresqlProfile.Hostname != "10.0.0.5" ||
		got.PostgresqlProfile.Port != 5432 || got.PostgresqlProfile.Database != "app" {
		t.Fatalf("postgresqlProfile not round-tripped: %+v", got.PostgresqlProfile)
	}

	if got.Labels["team"] != "data" {
		t.Fatalf("labels not round-tripped")
	}

	// Computed fields stable across reads (no Terraform drift).
	second, err := svc.Projects.Locations.ConnectionProfiles.Get(name).Do()
	if err != nil {
		t.Fatalf("second Get: %v", err)
	}

	if second.CreateTime != got.CreateTime || second.UpdateTime != got.UpdateTime {
		t.Fatalf("computed fields unstable across reads (drift): %+v vs %+v", got, second)
	}

	// Patch displayName via updateMask -> LRO, applied; profile untouched.
	patchOp, err := svc.Projects.Locations.ConnectionProfiles.Patch(name,
		&datastream.ConnectionProfile{DisplayName: "postgres source v2"}).
		UpdateMask("displayName").Context(ctx).Do()
	if err != nil {
		t.Fatalf("ConnectionProfiles.Patch: %v", err)
	}

	if !patchOp.Done {
		t.Fatalf("patch operation not done")
	}

	updated, err := svc.Projects.Locations.ConnectionProfiles.Get(name).Do()
	if err != nil {
		t.Fatalf("Get after patch: %v", err)
	}

	if updated.DisplayName != "postgres source v2" {
		t.Fatalf("displayName after patch = %q", updated.DisplayName)
	}

	if updated.PostgresqlProfile == nil || updated.PostgresqlProfile.Hostname != "10.0.0.5" {
		t.Fatalf("postgresqlProfile mutated by masked patch: %+v", updated.PostgresqlProfile)
	}
}

func TestSDKStreamLifecycle(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()
	parent := "projects/" + project + "/locations/us-central1"
	streamName := parent + "/streams/s"

	// Two connection profiles the stream references (source + gcs destination).
	seedProfile(t, svc, parent, "src", &datastream.ConnectionProfile{
		PostgresqlProfile: &datastream.PostgresqlProfile{Hostname: "10.0.0.5", Username: "u", Database: "app"},
	})
	seedProfile(t, svc, parent, "sink", &datastream.ConnectionProfile{
		GcsProfile: &datastream.GcsProfile{Bucket: "my-lake", RootPath: "/cdc"},
	})

	want := &datastream.Stream{
		DisplayName: "pg to gcs",
		SourceConfig: &datastream.SourceConfig{
			SourceConnectionProfile: parent + "/connectionProfiles/src",
			PostgresqlSourceConfig:  &datastream.PostgresqlSourceConfig{},
		},
		DestinationConfig: &datastream.DestinationConfig{
			DestinationConnectionProfile: parent + "/connectionProfiles/sink",
			GcsDestinationConfig: &datastream.GcsDestinationConfig{
				Path: "/prefix", JsonFileFormat: &datastream.JsonFileFormat{},
			},
		},
		BackfillAll: &datastream.BackfillAllStrategy{},
		Labels:      map[string]string{"env": "prod"},
	}

	op, err := svc.Projects.Locations.Streams.Create(parent, want).StreamId("s").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Streams.Create: %v", err)
	}

	if !op.Done {
		t.Fatalf("create operation not done")
	}

	got, err := svc.Projects.Locations.Streams.Get(streamName).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Streams.Get: %v", err)
	}

	if got.Name != streamName || got.CreateTime == "" {
		t.Fatalf("computed fields missing: %+v", got)
	}

	// A stream with no explicit state is minted NOT_STARTED (stable, so a default
	// desired_state reconciles clean).
	if got.State != "NOT_STARTED" {
		t.Fatalf("default state = %q, want NOT_STARTED", got.State)
	}

	if got.SourceConfig == nil || !strings.HasSuffix(got.SourceConfig.SourceConnectionProfile, "/src") ||
		got.SourceConfig.PostgresqlSourceConfig == nil {
		t.Fatalf("sourceConfig not round-tripped: %+v", got.SourceConfig)
	}

	if got.DestinationConfig == nil || got.DestinationConfig.GcsDestinationConfig == nil ||
		got.DestinationConfig.GcsDestinationConfig.Path != "/prefix" {
		t.Fatalf("destinationConfig not round-tripped: %+v", got.DestinationConfig)
	}

	if got.BackfillAll == nil {
		t.Fatalf("backfillAll oneof (empty object) not round-tripped: %+v", got)
	}

	// Stable across reads.
	second, err := svc.Projects.Locations.Streams.Get(streamName).Do()
	if err != nil {
		t.Fatalf("second Get: %v", err)
	}

	if second.CreateTime != got.CreateTime || second.State != got.State {
		t.Fatalf("computed fields unstable across reads (drift)")
	}

	// List.
	list, err := svc.Projects.Locations.Streams.List(parent).Do()
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(list.Streams) != 1 || !strings.HasSuffix(list.Streams[0].Name, "/s") {
		t.Fatalf("list = %+v", list.Streams)
	}

	// Patch displayName + state via updateMask -> LRO, applied; config untouched.
	patchOp, err := svc.Projects.Locations.Streams.Patch(streamName, &datastream.Stream{
		DisplayName: "pg to gcs v2", State: "RUNNING",
	}).UpdateMask("displayName,state").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Streams.Patch: %v", err)
	}

	if !patchOp.Done {
		t.Fatalf("patch operation not done")
	}

	updated, err := svc.Projects.Locations.Streams.Get(streamName).Do()
	if err != nil {
		t.Fatalf("Get after patch: %v", err)
	}

	if updated.DisplayName != "pg to gcs v2" || updated.State != "RUNNING" {
		t.Fatalf("patch not applied: displayName=%q state=%q", updated.DisplayName, updated.State)
	}

	if updated.SourceConfig == nil || !strings.HasSuffix(updated.SourceConfig.SourceConnectionProfile, "/src") {
		t.Fatalf("sourceConfig mutated by masked patch: %+v", updated.SourceConfig)
	}

	delOp, err := svc.Projects.Locations.Streams.Delete(streamName).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Streams.Delete: %v", err)
	}

	if !delOp.Done {
		t.Fatalf("delete operation not done")
	}

	if _, err := svc.Projects.Locations.Streams.Get(streamName).Do(); err == nil {
		t.Fatalf("expected 404 after delete")
	}
}

func TestSDKConnectionProfileNotFoundAndDuplicate(t *testing.T) {
	svc, project := newSDKClient(t)
	parent := "projects/" + project + "/locations/us-central1"

	if _, err := svc.Projects.Locations.ConnectionProfiles.Get(parent + "/connectionProfiles/ghost").Do(); err == nil {
		t.Fatalf("expected NOT_FOUND")
	}

	p := &datastream.ConnectionProfile{GcsProfile: &datastream.GcsProfile{Bucket: "b"}}

	if _, err := svc.Projects.Locations.ConnectionProfiles.Create(parent, p).ConnectionProfileId("dup").Do(); err != nil {
		t.Fatalf("first create: %v", err)
	}

	if _, err := svc.Projects.Locations.ConnectionProfiles.Create(parent, p).ConnectionProfileId("dup").Do(); err == nil {
		t.Fatalf("expected ALREADY_EXISTS on duplicate create")
	}
}

func seedProfile(t *testing.T, svc *datastream.Service, parent, id string, cp *datastream.ConnectionProfile) {
	t.Helper()

	if _, err := svc.Projects.Locations.ConnectionProfiles.Create(parent, cp).ConnectionProfileId(id).Do(); err != nil {
		t.Fatalf("seed connection profile %s: %v", id, err)
	}
}
