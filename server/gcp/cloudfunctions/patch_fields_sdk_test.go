package cloudfunctions_test

import (
	"context"
	"testing"

	cloudfunctions "google.golang.org/api/cloudfunctions/v1"
	cloudfunctions2 "google.golang.org/api/cloudfunctions/v2"
)

// TestSDKGen2PatchInstanceCount reproduces the dropped-scaling finding: a gen2
// PATCH of serviceConfig.maxInstanceCount must reflect on the next Get instead of
// being silently discarded by the merge.
func TestSDKGen2PatchInstanceCount(t *testing.T) {
	svc := newGCPV2Service(t)
	ctx := context.Background()

	parent := "projects/demo/locations/us-central1"
	name := parent + "/functions/scale"

	if _, err := svc.Projects.Locations.Functions.Create(parent, &cloudfunctions2.Function{
		BuildConfig: &cloudfunctions2.BuildConfig{Runtime: "go121", EntryPoint: "Hello"},
		ServiceConfig: &cloudfunctions2.ServiceConfig{
			MaxInstanceCount: 7,
			MinInstanceCount: 1,
		},
	}).FunctionId("scale").Context(ctx).Do(); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := svc.Projects.Locations.Functions.Get(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.ServiceConfig.MaxInstanceCount != 7 {
		t.Fatalf("maxInstanceCount = %d at create, want 7", got.ServiceConfig.MaxInstanceCount)
	}

	if _, err := svc.Projects.Locations.Functions.Patch(name, &cloudfunctions2.Function{
		ServiceConfig: &cloudfunctions2.ServiceConfig{MaxInstanceCount: 20},
	}).UpdateMask("serviceConfig.maxInstanceCount").Context(ctx).Do(); err != nil {
		t.Fatalf("Patch: %v", err)
	}

	got2, err := svc.Projects.Locations.Functions.Get(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get after patch: %v", err)
	}

	if got2.ServiceConfig.MaxInstanceCount != 20 {
		t.Fatalf("maxInstanceCount = %d after patch, want 20", got2.ServiceConfig.MaxInstanceCount)
	}

	// The unmasked minInstanceCount must survive the scaling patch.
	if got2.ServiceConfig.MinInstanceCount != 1 {
		t.Fatalf("minInstanceCount = %d after patch, want 1 preserved", got2.ServiceConfig.MinInstanceCount)
	}
}

// TestSDKGen2PatchPartialBuildConfig reproduces the wholesale-buildConfig-replace
// finding: a partial buildConfig PATCH must merge sub-fields, leaving the
// unmasked runtime and entryPoint intact.
func TestSDKGen2PatchPartialBuildConfig(t *testing.T) {
	svc := newGCPV2Service(t)
	ctx := context.Background()

	parent := "projects/demo/locations/us-central1"
	name := parent + "/functions/build"

	if _, err := svc.Projects.Locations.Functions.Create(parent, &cloudfunctions2.Function{
		BuildConfig: &cloudfunctions2.BuildConfig{Runtime: "go121", EntryPoint: "Hello"},
	}).FunctionId("build").Context(ctx).Do(); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := svc.Projects.Locations.Functions.Patch(name, &cloudfunctions2.Function{
		BuildConfig: &cloudfunctions2.BuildConfig{
			EnvironmentVariables: map[string]string{"K": "V"},
		},
	}).UpdateMask("buildConfig.environmentVariables").Context(ctx).Do(); err != nil {
		t.Fatalf("Patch: %v", err)
	}

	got, err := svc.Projects.Locations.Functions.Get(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get after patch: %v", err)
	}

	if got.BuildConfig.Runtime != "go121" {
		t.Fatalf("buildConfig.runtime = %q after partial patch, want go121 preserved", got.BuildConfig.Runtime)
	}

	if got.BuildConfig.EntryPoint != "Hello" {
		t.Fatalf("buildConfig.entryPoint = %q after partial patch, want Hello preserved", got.BuildConfig.EntryPoint)
	}

	if got.BuildConfig.EnvironmentVariables["K"] != "V" {
		t.Fatalf("buildConfig.environmentVariables = %v, want K=V applied", got.BuildConfig.EnvironmentVariables)
	}
}

// TestSDKGen1EventTriggerRoundTrip reproduces the missing-eventTrigger finding: an
// event-driven gen1 function must round-trip its eventTrigger and advertise NO
// httpsTrigger (the two are mutually exclusive).
func TestSDKGen1EventTriggerRoundTrip(t *testing.T) {
	svc := newGCPSDKService(t)
	ctx := context.Background()

	parent := "projects/demo/locations/us-central1"
	name := parent + "/functions/onpublish"

	if _, err := svc.Projects.Locations.Functions.Create(parent, &cloudfunctions.CloudFunction{
		Name:    name,
		Runtime: "go121",
		EventTrigger: &cloudfunctions.EventTrigger{
			EventType: "google.pubsub.topic.publish",
			Resource:  "projects/demo/topics/events",
			Service:   "pubsub.googleapis.com",
		},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := svc.Projects.Locations.Functions.Get(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.EventTrigger == nil {
		t.Fatal("eventTrigger missing, want it round-tripped")
	}

	if got.EventTrigger.EventType != "google.pubsub.topic.publish" {
		t.Fatalf("eventType = %q, want google.pubsub.topic.publish", got.EventTrigger.EventType)
	}

	if got.EventTrigger.Resource != "projects/demo/topics/events" {
		t.Fatalf("resource = %q, want the created topic", got.EventTrigger.Resource)
	}

	if got.HttpsTrigger != nil {
		t.Fatalf("httpsTrigger = %+v, want nil for an event-driven function", got.HttpsTrigger)
	}
}

// TestSDKGen1FieldRoundTrip reproduces the dropped-field finding: description and
// vpcConnector supplied at create must round-trip through Get.
func TestSDKGen1FieldRoundTrip(t *testing.T) {
	svc := newGCPSDKService(t)
	ctx := context.Background()

	parent := "projects/demo/locations/us-central1"
	name := parent + "/functions/rich"

	if _, err := svc.Projects.Locations.Functions.Create(parent, &cloudfunctions.CloudFunction{
		Name:         name,
		Runtime:      "go121",
		Description:  "handles orders",
		VpcConnector: "projects/demo/locations/us-central1/connectors/vpc",
		MaxInstances: 5,
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := svc.Projects.Locations.Functions.Get(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Description != "handles orders" {
		t.Fatalf("description = %q, want 'handles orders'", got.Description)
	}

	if got.VpcConnector != "projects/demo/locations/us-central1/connectors/vpc" {
		t.Fatalf("vpcConnector = %q, want the created connector", got.VpcConnector)
	}

	if got.MaxInstances != 5 {
		t.Fatalf("maxInstances = %d, want 5", got.MaxInstances)
	}
}

// TestSDKGen1UpdateMaskClears reproduces the ignored-updateMask finding: a masked
// field must be CLEARED when the PATCH body omits it, and an unmasked field must
// survive the same patch.
func TestSDKGen1UpdateMaskClears(t *testing.T) {
	svc := newGCPSDKService(t)
	ctx := context.Background()

	parent := "projects/demo/locations/us-central1"
	name := parent + "/functions/clearable"

	if _, err := svc.Projects.Locations.Functions.Create(parent, &cloudfunctions.CloudFunction{
		Name:         name,
		Runtime:      "go121",
		Description:  "keep me",
		VpcConnector: "projects/demo/locations/us-central1/connectors/vpc",
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Mask names vpcConnector but the body omits it -> clear vpcConnector, while
	// the unmasked description survives.
	if _, err := svc.Projects.Locations.Functions.Patch(name, &cloudfunctions.CloudFunction{
		Name: name,
	}).UpdateMask("vpcConnector").Context(ctx).Do(); err != nil {
		t.Fatalf("Patch: %v", err)
	}

	got, err := svc.Projects.Locations.Functions.Get(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get after patch: %v", err)
	}

	if got.VpcConnector != "" {
		t.Fatalf("vpcConnector = %q after masked clear, want empty", got.VpcConnector)
	}

	if got.Description != "keep me" {
		t.Fatalf("description = %q after unrelated patch, want 'keep me' preserved", got.Description)
	}
}
