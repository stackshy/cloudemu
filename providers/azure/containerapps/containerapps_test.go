package containerapps_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/providers/azure/containerapps"
)

const (
	sub    = "sub-1"
	rg     = "rg-1"
	envNm  = "env-1"
	appNm  = "app-1"
	region = "eastus"
)

func newMock() *containerapps.Mock { return containerapps.New(nil) }

func mustEnv(t *testing.T, m *containerapps.Mock) containerapps.Environment {
	t.Helper()

	env, created, err := m.CreateOrUpdateEnvironment(context.Background(), sub, rg, envNm, containerapps.EnvironmentInput{
		Location: region,
		Tags:     map[string]string{"team": "core"},
		AppLogs:  &containerapps.AppLogsConfiguration{Destination: "log-analytics"},
	})
	if err != nil {
		t.Fatalf("CreateOrUpdateEnvironment: %v", err)
	}

	if !created {
		t.Fatal("first environment create should report created=true")
	}

	return env
}

func TestEnvironmentMintsStableDomainAndIP(t *testing.T) {
	m := newMock()
	env := mustEnv(t, m)

	if !strings.HasSuffix(env.DefaultDomain, ".azurecontainerapps.io") {
		t.Fatalf("defaultDomain = %q, want a *.azurecontainerapps.io domain", env.DefaultDomain)
	}

	if env.StaticIP == "" {
		t.Fatal("staticIp empty, want a synthesized IP")
	}

	// An update preserves the minted domain and IP.
	updated, created, err := m.CreateOrUpdateEnvironment(context.Background(), sub, rg, envNm, containerapps.EnvironmentInput{
		Location: region, Tags: map[string]string{"team": "platform"},
	})
	if err != nil {
		t.Fatalf("update environment: %v", err)
	}

	if created {
		t.Fatal("second create should report created=false")
	}

	if updated.DefaultDomain != env.DefaultDomain || updated.StaticIP != env.StaticIP {
		t.Fatalf("update changed minted values: domain %q->%q ip %q->%q",
			env.DefaultDomain, updated.DefaultDomain, env.StaticIP, updated.StaticIP)
	}

	if updated.Tags["team"] != "platform" {
		t.Fatalf("tags not updated: %v", updated.Tags)
	}
}

func TestAppFqdnDerivedFromEnvironment(t *testing.T) {
	m := newMock()
	env := mustEnv(t, m)

	app, created, err := m.CreateOrUpdateApp(context.Background(), sub, rg, appNm, &containerapps.AppInput{
		Location:      region,
		EnvironmentID: env.ARMID(),
		Ingress:       &containerapps.Ingress{External: true, TargetPort: 8080},
		Template: containerapps.Template{
			Containers: []containerapps.Container{{
				Name: "main", Image: "nginx", Resources: &containerapps.ContainerResources{CPU: 0.25, Memory: "0.5Gi"},
			}},
			Scale: &containerapps.Scale{MinReplicas: ptr(int32(1)), MaxReplicas: ptr(int32(5))},
		},
	})
	if err != nil {
		t.Fatalf("CreateOrUpdateApp: %v", err)
	}

	if !created {
		t.Fatal("first app create should report created=true")
	}

	wantFqdn := strings.ToLower(appNm) + "." + env.DefaultDomain
	if app.Fqdn != wantFqdn {
		t.Fatalf("fqdn = %q, want %q", app.Fqdn, wantFqdn)
	}

	if app.LatestRevisionName == "" || !strings.HasPrefix(app.LatestRevisionName, appNm+"--") {
		t.Fatalf("latestRevisionName = %q, want %q prefix", app.LatestRevisionName, appNm+"--")
	}
}

func TestAppWithoutIngressHasNoFqdn(t *testing.T) {
	m := newMock()
	env := mustEnv(t, m)

	app, _, err := m.CreateOrUpdateApp(context.Background(), sub, rg, appNm, &containerapps.AppInput{
		Location: region, EnvironmentID: env.ARMID(),
	})
	if err != nil {
		t.Fatalf("CreateOrUpdateApp: %v", err)
	}

	if app.Fqdn != "" {
		t.Fatalf("fqdn = %q, want empty for an app with no ingress", app.Fqdn)
	}
}

func TestListAndDelete(t *testing.T) {
	m := newMock()
	env := mustEnv(t, m)
	ctx := context.Background()

	if _, _, err := m.CreateOrUpdateApp(ctx, sub, rg, appNm, &containerapps.AppInput{
		Location: region, EnvironmentID: env.ARMID(),
	}); err != nil {
		t.Fatalf("create app: %v", err)
	}

	envs, err := m.ListEnvironmentsBySubscription(ctx, sub)
	if err != nil || len(envs) != 1 {
		t.Fatalf("ListEnvironmentsBySubscription = %v (err %v), want 1", envs, err)
	}

	apps, err := m.ListAppsByResourceGroup(ctx, sub, rg)
	if err != nil || len(apps) != 1 {
		t.Fatalf("ListAppsByResourceGroup = %v (err %v), want 1", apps, err)
	}

	existed, err := m.DeleteApp(ctx, sub, rg, appNm)
	if err != nil || !existed {
		t.Fatalf("DeleteApp = %v (err %v), want existed=true", existed, err)
	}

	if _, err := m.GetApp(ctx, sub, rg, appNm); err == nil {
		t.Fatal("GetApp after delete returned nil error, want NotFound")
	}

	// Deleting again is idempotent.
	if existed, _ := m.DeleteApp(ctx, sub, rg, appNm); existed {
		t.Fatal("second DeleteApp reported existed=true, want false")
	}
}

func TestPurgeResourceGroupCascades(t *testing.T) {
	m := newMock()
	env := mustEnv(t, m)
	ctx := context.Background()

	if _, _, err := m.CreateOrUpdateApp(ctx, sub, rg, appNm, &containerapps.AppInput{
		Location: region, EnvironmentID: env.ARMID(),
	}); err != nil {
		t.Fatalf("create app: %v", err)
	}

	if err := m.PurgeResourceGroup(ctx, sub, rg); err != nil {
		t.Fatalf("PurgeResourceGroup: %v", err)
	}

	envs, _ := m.DiscoverEnvironments(ctx)
	apps, _ := m.DiscoverApps(ctx)

	if len(envs) != 0 || len(apps) != 0 {
		t.Fatalf("after purge: %d envs, %d apps, want 0/0", len(envs), len(apps))
	}
}

func TestSnapshotRoundTrip(t *testing.T) {
	m := newMock()
	env := mustEnv(t, m)
	ctx := context.Background()

	if _, _, err := m.CreateOrUpdateApp(ctx, sub, rg, appNm, &containerapps.AppInput{
		Location: region, EnvironmentID: env.ARMID(),
		Ingress:  &containerapps.Ingress{External: true, TargetPort: 80},
		Template: containerapps.Template{Scale: &containerapps.Scale{MinReplicas: ptr(int32(3))}},
	}); err != nil {
		t.Fatalf("create app: %v", err)
	}

	data, err := m.Snapshot(ctx, false)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	restored := newMock()
	if err := restored.Restore(ctx, data); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	gotEnv, err := restored.GetEnvironment(ctx, sub, rg, envNm)
	if err != nil {
		t.Fatalf("GetEnvironment after restore: %v", err)
	}

	if gotEnv.DefaultDomain != env.DefaultDomain {
		t.Fatalf("restored defaultDomain = %q, want %q", gotEnv.DefaultDomain, env.DefaultDomain)
	}

	gotApp, err := restored.GetApp(ctx, sub, rg, appNm)
	if err != nil {
		t.Fatalf("GetApp after restore: %v", err)
	}

	if gotApp.Template.Scale == nil || gotApp.Template.Scale.MinReplicas == nil || *gotApp.Template.Scale.MinReplicas != 3 {
		t.Fatalf("restored scale = %v, want minReplicas=3", gotApp.Template.Scale)
	}
}

func mustApp(t *testing.T, m *containerapps.Mock, in *containerapps.AppInput) containerapps.ContainerApp {
	t.Helper()

	app, _, err := m.CreateOrUpdateApp(context.Background(), sub, rg, appNm, in)
	if err != nil {
		t.Fatalf("CreateOrUpdateApp: %v", err)
	}

	return app
}

// TestRevisionMaterializedOnTemplateChange proves a create mints one revision and
// a template change mints a second, with both retained in multiple-revisions mode.
func TestRevisionMaterializedOnTemplateChange(t *testing.T) {
	m := newMock()
	env := mustEnv(t, m)
	ctx := context.Background()

	base := func(suffix, image string) *containerapps.AppInput {
		return &containerapps.AppInput{
			Location: region, EnvironmentID: env.ARMID(), ActiveRevMode: "Multiple",
			Ingress:  &containerapps.Ingress{External: true, TargetPort: 80},
			Template: containerapps.Template{
				RevisionSuffix: suffix,
				Containers:     []containerapps.Container{{Name: "main", Image: image}},
			},
		}
	}

	app := mustApp(t, m, base("v1", "nginx:1"))
	if app.LatestRevisionName != appNm+"--v1" {
		t.Fatalf("latestRevisionName = %q, want %s--v1", app.LatestRevisionName, appNm)
	}

	revs, err := m.ListRevisions(ctx, sub, rg, appNm)
	if err != nil || len(revs) != 1 {
		t.Fatalf("ListRevisions after create = %v (err %v), want 1", revs, err)
	}

	if !revs[0].Active || revs[0].TrafficWeight != 100 {
		t.Fatalf("first revision active=%v weight=%d, want active=true weight=100", revs[0].Active, revs[0].TrafficWeight)
	}

	mustApp(t, m, base("v2", "nginx:2"))

	revs, err = m.ListRevisions(ctx, sub, rg, appNm)
	if err != nil || len(revs) != 2 {
		t.Fatalf("ListRevisions after update = %v (err %v), want 2", revs, err)
	}

	// Multiple mode keeps the earlier revision active alongside the new latest.
	for i := range revs {
		if !revs[i].Active {
			t.Fatalf("revision %q inactive in multiple mode, want active", revs[i].Name)
		}
	}
}

// TestSingleModeSupersedesRevision proves single-revision mode deactivates the
// prior revision when a new one is materialized.
func TestSingleModeSupersedesRevision(t *testing.T) {
	m := newMock()
	env := mustEnv(t, m)
	ctx := context.Background()

	mk := func(suffix string) *containerapps.AppInput {
		return &containerapps.AppInput{
			Location: region, EnvironmentID: env.ARMID(), ActiveRevMode: "Single",
			Template: containerapps.Template{RevisionSuffix: suffix},
		}
	}

	mustApp(t, m, mk("v1"))
	mustApp(t, m, mk("v2"))

	r1, err := m.GetRevision(ctx, sub, rg, appNm, appNm+"--v1")
	if err != nil {
		t.Fatalf("GetRevision v1: %v", err)
	}

	if r1.Active {
		t.Fatal("v1 still active in single mode after v2 created, want superseded")
	}

	r2, err := m.GetRevision(ctx, sub, rg, appNm, appNm+"--v2")
	if err != nil || !r2.Active {
		t.Fatalf("v2 active = %v (err %v), want active", r2.Active, err)
	}
}

// TestActivateDeactivateRestartRevision covers the three POST verbs and NotFound.
func TestActivateDeactivateRestartRevision(t *testing.T) {
	m := newMock()
	env := mustEnv(t, m)
	ctx := context.Background()
	rev := appNm + "--v1"

	mustApp(t, m, &containerapps.AppInput{
		Location: region, EnvironmentID: env.ARMID(),
		Template: containerapps.Template{RevisionSuffix: "v1"},
	})

	if err := m.DeactivateRevision(ctx, sub, rg, appNm, rev); err != nil {
		t.Fatalf("DeactivateRevision: %v", err)
	}

	if got, _ := m.GetRevision(ctx, sub, rg, appNm, rev); got.Active {
		t.Fatal("revision active after deactivate, want false")
	}

	if err := m.ActivateRevision(ctx, sub, rg, appNm, rev); err != nil {
		t.Fatalf("ActivateRevision: %v", err)
	}

	if got, _ := m.GetRevision(ctx, sub, rg, appNm, rev); !got.Active {
		t.Fatal("revision inactive after activate, want true")
	}

	if err := m.RestartRevision(ctx, sub, rg, appNm, rev); err != nil {
		t.Fatalf("RestartRevision: %v", err)
	}

	if err := m.ActivateRevision(ctx, sub, rg, appNm, appNm+"--missing"); err == nil {
		t.Fatal("ActivateRevision on missing revision returned nil, want NotFound")
	}

	if _, err := m.ListRevisions(ctx, sub, rg, "no-app"); err == nil {
		t.Fatal("ListRevisions on missing app returned nil, want NotFound")
	}
}

// TestTrafficWeightValidation proves an ingress split must reference known
// revisions and sum to 100, and a rejected update leaves the app unchanged.
func TestTrafficWeightValidation(t *testing.T) {
	m := newMock()
	env := mustEnv(t, m)
	ctx := context.Background()

	mk := func(suffix string, traffic []containerapps.TrafficWeight) *containerapps.AppInput {
		return &containerapps.AppInput{
			Location: region, EnvironmentID: env.ARMID(), ActiveRevMode: "Multiple",
			Ingress:  &containerapps.Ingress{External: true, TargetPort: 80, Traffic: traffic},
			Template: containerapps.Template{RevisionSuffix: suffix},
		}
	}

	mustApp(t, m, mk("v1", nil))
	mustApp(t, m, mk("v2", nil))

	rev1, rev2 := appNm+"--v1", appNm+"--v2"

	// A balanced split is accepted and reflected in per-revision weights.
	mustApp(t, m, mk("v2", []containerapps.TrafficWeight{
		{RevisionName: rev1, Weight: 60}, {RevisionName: rev2, Weight: 40},
	}))

	r1, _ := m.GetRevision(ctx, sub, rg, appNm, rev1)
	if r1.TrafficWeight != 60 {
		t.Fatalf("rev1 weight = %d, want 60", r1.TrafficWeight)
	}

	// An unbalanced split is rejected...
	if _, _, err := m.CreateOrUpdateApp(ctx, sub, rg, appNm, mk("v2", []containerapps.TrafficWeight{
		{RevisionName: rev1, Weight: 60}, {RevisionName: rev2, Weight: 30},
	})); err == nil {
		t.Fatal("split summing to 90 accepted, want InvalidArgument")
	}

	// ...and the prior valid split survives the rejected update.
	if r1, _ = m.GetRevision(ctx, sub, rg, appNm, rev1); r1.TrafficWeight != 60 {
		t.Fatalf("rev1 weight after rejected update = %d, want 60 (unchanged)", r1.TrafficWeight)
	}

	// A split naming an unknown revision is rejected.
	if _, _, err := m.CreateOrUpdateApp(ctx, sub, rg, appNm, mk("v2", []containerapps.TrafficWeight{
		{RevisionName: appNm + "--ghost", Weight: 100},
	})); err == nil {
		t.Fatal("split referencing unknown revision accepted, want InvalidArgument")
	}
}

// TestRevisionsSurviveSnapshot proves the revision history round-trips through a
// snapshot/restore.
func TestRevisionsSurviveSnapshot(t *testing.T) {
	m := newMock()
	env := mustEnv(t, m)
	ctx := context.Background()

	mustApp(t, m, &containerapps.AppInput{
		Location: region, EnvironmentID: env.ARMID(), ActiveRevMode: "Multiple",
		Template: containerapps.Template{RevisionSuffix: "v1"},
	})
	mustApp(t, m, &containerapps.AppInput{
		Location: region, EnvironmentID: env.ARMID(), ActiveRevMode: "Multiple",
		Template: containerapps.Template{RevisionSuffix: "v2"},
	})

	data, err := m.Snapshot(ctx, false)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	restored := newMock()
	if err := restored.Restore(ctx, data); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	revs, err := restored.ListRevisions(ctx, sub, rg, appNm)
	if err != nil || len(revs) != 2 {
		t.Fatalf("restored ListRevisions = %v (err %v), want 2", revs, err)
	}
}

func ptr[T any](v T) *T { return &v }
