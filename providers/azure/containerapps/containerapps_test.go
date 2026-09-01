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

func ptr[T any](v T) *T { return &v }
