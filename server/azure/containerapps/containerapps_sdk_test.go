// The real armappcontainers SDK drives this handler end-to-end: a managed
// environment create (the poller must complete, not hang), a container app
// referencing that environment, get/list, ARG discoverability, and delete.
package containerapps_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v2"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resourcegraph/armresourcegraph"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

const (
	subID   = "00000000-0000-0000-0000-000000000000"
	rgName  = "rg-ca"
	envName = "prod-env"
	appName = "api"
)

// fakeCred is a static-token credential for tests.
type fakeCred struct{}

func (fakeCred) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "fake", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

func clientOpts(ts *httptest.Server) *arm.ClientOptions {
	myCloud := cloud.Configuration{
		ActiveDirectoryAuthorityHost: "https://login.microsoftonline.com/",
		Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
			cloud.ResourceManager: {Endpoint: ts.URL, Audience: "https://management.azure.com"},
		},
	}

	return &arm.ClientOptions{ClientOptions: azcore.ClientOptions{
		Cloud: myCloud, Transport: ts.Client(),
		Retry: policy.RetryOptions{MaxRetries: -1},
	}}
}

func newServer(t *testing.T) *httptest.Server {
	t.Helper()

	ts := httptest.NewTLSServer(azureserver.NewFromProvider(cloudemu.NewAzure()))
	t.Cleanup(ts.Close)

	return ts
}

// TestSDKContainerAppsLifecycle drives the full Container Apps control plane with
// the real armappcontainers SDK: managed-environment create (poller must
// complete, not hang), a container app create referencing the environment,
// get/list, ARG discoverability, and deletes.
func TestSDKContainerAppsLifecycle(t *testing.T) {
	ts := newServer(t)
	ctx := context.Background()

	envClient, err := armappcontainers.NewManagedEnvironmentsClient(subID, fakeCred{}, clientOpts(ts))
	if err != nil {
		t.Fatalf("NewManagedEnvironmentsClient: %v", err)
	}

	envID := createEnvironment(t, ctx, envClient)
	assertEnvironmentListed(t, ctx, envClient)

	appClient, err := armappcontainers.NewContainerAppsClient(subID, fakeCred{}, clientOpts(ts))
	if err != nil {
		t.Fatalf("NewContainerAppsClient: %v", err)
	}

	createContainerApp(t, ctx, appClient, envID)
	assertContainerAppListed(t, ctx, appClient)
	assertDiscoverable(t, ctx, ts)

	deleteContainerApp(t, ctx, appClient)
	deleteEnvironment(t, ctx, envClient)
}

func createEnvironment(t *testing.T, ctx context.Context, c *armappcontainers.ManagedEnvironmentsClient) string {
	t.Helper()

	poller, err := c.BeginCreateOrUpdate(ctx, rgName, envName, armappcontainers.ManagedEnvironment{
		Location: to.Ptr("eastus"),
		Tags:     map[string]*string{"env": to.Ptr("prod")},
		Properties: &armappcontainers.ManagedEnvironmentProperties{
			AppLogsConfiguration: &armappcontainers.AppLogsConfiguration{Destination: to.Ptr("log-analytics")},
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate environment: %v", err)
	}

	// PollUntilDone must terminate: the sync 201 body carries
	// provisioningState=Succeeded, so the poller does not hang.
	res, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("poll environment create: %v", err)
	}

	if res.Properties == nil || res.Properties.ProvisioningState == nil ||
		*res.Properties.ProvisioningState != armappcontainers.EnvironmentProvisioningStateSucceeded {
		t.Fatalf("environment provisioningState = %v, want Succeeded", res.Properties)
	}

	if res.Properties.DefaultDomain == nil || !strings.HasSuffix(*res.Properties.DefaultDomain, ".azurecontainerapps.io") {
		t.Fatalf("defaultDomain = %v, want a *.azurecontainerapps.io domain", res.Properties.DefaultDomain)
	}

	if res.Properties.StaticIP == nil || *res.Properties.StaticIP == "" {
		t.Fatalf("staticIp empty, want a synthesized IP")
	}

	got, err := c.Get(ctx, rgName, envName, nil)
	if err != nil {
		t.Fatalf("Get environment: %v", err)
	}

	if got.Tags["env"] == nil || *got.Tags["env"] != "prod" {
		t.Fatalf("environment tags = %v, want env=prod", got.Tags)
	}

	return *got.ID
}

func assertEnvironmentListed(t *testing.T, ctx context.Context, c *armappcontainers.ManagedEnvironmentsClient) {
	t.Helper()

	pager := c.NewListByResourceGroupPager(rgName, nil)

	var names []string

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("list environments: %v", err)
		}

		for _, e := range page.Value {
			names = append(names, *e.Name)
		}
	}

	if len(names) != 1 || names[0] != envName {
		t.Fatalf("ListByResourceGroup = %v, want [%s]", names, envName)
	}
}

func createContainerApp(t *testing.T, ctx context.Context, c *armappcontainers.ContainerAppsClient, envID string) {
	t.Helper()

	poller, err := c.BeginCreateOrUpdate(ctx, rgName, appName, armappcontainers.ContainerApp{
		Location: to.Ptr("eastus"),
		Properties: &armappcontainers.ContainerAppProperties{
			EnvironmentID: to.Ptr(envID),
			Configuration: &armappcontainers.Configuration{
				ActiveRevisionsMode: to.Ptr(armappcontainers.ActiveRevisionsModeSingle),
				Ingress: &armappcontainers.Ingress{
					External:   to.Ptr(true),
					TargetPort: to.Ptr[int32](80),
				},
			},
			Template: &armappcontainers.Template{
				Containers: []*armappcontainers.Container{{
					Name:  to.Ptr("main"),
					Image: to.Ptr("nginx:latest"),
					Resources: &armappcontainers.ContainerResources{
						CPU:    to.Ptr(0.5),
						Memory: to.Ptr("1Gi"),
					},
				}},
				Scale: &armappcontainers.Scale{
					MinReplicas: to.Ptr[int32](2),
					MaxReplicas: to.Ptr[int32](10),
				},
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate container app: %v", err)
	}

	res, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("poll container app create: %v", err)
	}

	if res.Properties == nil || res.Properties.ProvisioningState == nil ||
		*res.Properties.ProvisioningState != armappcontainers.ContainerAppProvisioningStateSucceeded {
		t.Fatalf("app provisioningState = %v, want Succeeded", res.Properties)
	}

	got, err := c.Get(ctx, rgName, appName, nil)
	if err != nil {
		t.Fatalf("Get container app: %v", err)
	}

	assertAppShape(t, &got.ContainerApp)
}

func assertAppShape(t *testing.T, app *armappcontainers.ContainerApp) {
	t.Helper()

	p := app.Properties
	if p == nil {
		t.Fatal("app properties nil")
	}

	if p.EnvironmentID == nil || !strings.HasSuffix(*p.EnvironmentID, "managedEnvironments/"+envName) {
		t.Fatalf("environmentId = %v, want it to reference %s", p.EnvironmentID, envName)
	}

	// Ingress fqdn is synthesized from the referenced environment's default domain.
	if p.Configuration == nil || p.Configuration.Ingress == nil ||
		p.Configuration.Ingress.Fqdn == nil || !strings.Contains(*p.Configuration.Ingress.Fqdn, appName) {
		t.Fatalf("ingress fqdn = %v, want a synthesized fqdn containing %q", p.Configuration, appName)
	}

	if p.Template == nil || len(p.Template.Containers) != 1 {
		t.Fatalf("template containers = %v, want 1", p.Template)
	}

	res := p.Template.Containers[0].Resources
	if res == nil || res.CPU == nil || *res.CPU != 0.5 || res.Memory == nil || *res.Memory != "1Gi" {
		t.Fatalf("container resources = %v, want cpu=0.5 memory=1Gi", res)
	}

	if p.Template.Scale == nil || p.Template.Scale.MinReplicas == nil || *p.Template.Scale.MinReplicas != 2 {
		t.Fatalf("scale.minReplicas = %v, want 2", p.Template.Scale)
	}
}

func assertContainerAppListed(t *testing.T, ctx context.Context, c *armappcontainers.ContainerAppsClient) {
	t.Helper()

	pager := c.NewListByResourceGroupPager(rgName, nil)

	var names []string

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("list container apps: %v", err)
		}

		for _, a := range page.Value {
			names = append(names, *a.Name)
		}
	}

	if len(names) != 1 || names[0] != appName {
		t.Fatalf("ListByResourceGroup = %v, want [%s]", names, appName)
	}
}

// assertDiscoverable proves the container app surfaces through Resource Graph —
// the point of #334 — with its cost-relevant properties projected onto the row.
func assertDiscoverable(t *testing.T, ctx context.Context, ts *httptest.Server) {
	t.Helper()

	cf, err := armresourcegraph.NewClientFactory(fakeCred{}, clientOpts(ts))
	if err != nil {
		t.Fatalf("NewClientFactory: %v", err)
	}

	out, err := cf.NewClient().Resources(ctx, armresourcegraph.QueryRequest{
		Query:         to.Ptr("Resources | where type =~ 'microsoft.app/containerapps'"),
		Subscriptions: []*string{to.Ptr(subID)},
	}, nil)
	if err != nil {
		t.Fatalf("ARG query: %v", err)
	}

	data, ok := out.Data.([]any)
	if !ok || len(data) != 1 {
		t.Fatalf("ARG rows = %v, want exactly 1 container app", out.Data)
	}

	row := data[0].(map[string]any)
	if row["name"] != appName || row["type"] != "microsoft.app/containerapps" {
		t.Fatalf("ARG row = %v, want name=%s type=microsoft.app/containerapps", row, appName)
	}

	assertCostProps(t, row)
}

// assertCostProps walks the projected properties bag to the cpu/memory/replica
// signals a cost discoverer prices on.
func assertCostProps(t *testing.T, row map[string]any) {
	t.Helper()

	props, ok := row["properties"].(map[string]any)
	if !ok {
		t.Fatalf("row properties = %v, want a projected bag", row["properties"])
	}

	tmpl := props["template"].(map[string]any)

	scale := tmpl["scale"].(map[string]any)
	if got := toFloat(scale["minReplicas"]); got != 2 {
		t.Fatalf("projected scale.minReplicas = %v, want 2", scale["minReplicas"])
	}

	containers := tmpl["containers"].([]any)
	if len(containers) != 1 {
		t.Fatalf("projected containers = %v, want 1", containers)
	}

	res := containers[0].(map[string]any)["resources"].(map[string]any)
	if got := toFloat(res["cpu"]); got != 0.5 {
		t.Fatalf("projected resources.cpu = %v, want 0.5", res["cpu"])
	}

	if res["memory"] != "1Gi" {
		t.Fatalf("projected resources.memory = %v, want 1Gi", res["memory"])
	}
}

func toFloat(v any) float64 {
	f, _ := v.(float64)

	return f
}

// TestSDKContainerAppRevisions drives the revision/traffic surface with the real
// armappcontainers ContainerAppsRevisionsClient: creating an app materializes a
// revision, updating its template mints a second (both listed in multiple-revisions
// mode), get/activate/deactivate/restart a revision, a traffic split across the
// two revisions, and the errors on an unbalanced split and a missing revision/app.
func TestSDKContainerAppRevisions(t *testing.T) {
	ts := newServer(t)
	ctx := context.Background()

	envID := seedRevisionEnv(t, ctx, ts)

	appClient, err := armappcontainers.NewContainerAppsClient(subID, fakeCred{}, clientOpts(ts))
	if err != nil {
		t.Fatalf("NewContainerAppsClient: %v", err)
	}

	revClient, err := armappcontainers.NewContainerAppsRevisionsClient(subID, fakeCred{}, clientOpts(ts))
	if err != nil {
		t.Fatalf("NewContainerAppsRevisionsClient: %v", err)
	}

	// Create the app (multiple-revisions mode, suffix v1) -> revision "api--v1".
	putRevisionApp(t, ctx, appClient, envID, "v1", "nginx:1", nil)

	rev1 := appName + "--v1"
	rev2 := appName + "--v2"

	assertRevisions(t, ctx, revClient, map[string]bool{rev1: true})

	// A template change mints a second revision; both are active in multiple mode.
	putRevisionApp(t, ctx, appClient, envID, "v2", "nginx:2", nil)
	assertRevisions(t, ctx, revClient, map[string]bool{rev1: true, rev2: true})

	// The latest revision carries 100% of traffic with no explicit split.
	got := getRevision(t, ctx, revClient, rev2)
	if got.Properties.TrafficWeight == nil || *got.Properties.TrafficWeight != 100 {
		t.Fatalf("rev2 trafficWeight = %v, want 100", got.Properties.TrafficWeight)
	}

	assertActivateDeactivate(t, ctx, revClient, rev1)

	if _, err := revClient.RestartRevision(ctx, rgName, appName, rev2, nil); err != nil {
		t.Fatalf("RestartRevision: %v", err)
	}

	assertTrafficSplit(t, ctx, appClient, revClient, envID, rev1, rev2)
	assertRevisionErrors(t, ctx, revClient)
}

func seedRevisionEnv(t *testing.T, ctx context.Context, ts *httptest.Server) string {
	t.Helper()

	envClient, err := armappcontainers.NewManagedEnvironmentsClient(subID, fakeCred{}, clientOpts(ts))
	if err != nil {
		t.Fatalf("NewManagedEnvironmentsClient: %v", err)
	}

	return createEnvironment(t, ctx, envClient)
}

func putRevisionApp(
	t *testing.T, ctx context.Context, c *armappcontainers.ContainerAppsClient,
	envID, suffix, image string, traffic []*armappcontainers.TrafficWeight,
) {
	t.Helper()

	ingress := &armappcontainers.Ingress{External: to.Ptr(true), TargetPort: to.Ptr[int32](80)}
	if traffic != nil {
		ingress.Traffic = traffic
	}

	poller, err := c.BeginCreateOrUpdate(ctx, rgName, appName, armappcontainers.ContainerApp{
		Location: to.Ptr("eastus"),
		Properties: &armappcontainers.ContainerAppProperties{
			EnvironmentID: to.Ptr(envID),
			Configuration: &armappcontainers.Configuration{
				ActiveRevisionsMode: to.Ptr(armappcontainers.ActiveRevisionsModeMultiple),
				Ingress:             ingress,
			},
			Template: &armappcontainers.Template{
				RevisionSuffix: to.Ptr(suffix),
				Containers: []*armappcontainers.Container{{
					Name: to.Ptr("main"), Image: to.Ptr(image),
				}},
				Scale: &armappcontainers.Scale{MinReplicas: to.Ptr[int32](2)},
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate app (suffix %s): %v", suffix, err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("poll app create (suffix %s): %v", suffix, err)
	}
}

func assertRevisions(
	t *testing.T, ctx context.Context, c *armappcontainers.ContainerAppsRevisionsClient, wantActive map[string]bool,
) {
	t.Helper()

	got := map[string]bool{}
	pager := c.NewListRevisionsPager(rgName, appName, nil)

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("list revisions: %v", err)
		}

		for _, rev := range page.Value {
			got[*rev.Name] = rev.Properties != nil && rev.Properties.Active != nil && *rev.Properties.Active
		}
	}

	if len(got) != len(wantActive) {
		t.Fatalf("revisions = %v, want %v", got, wantActive)
	}

	for name, active := range wantActive {
		if gotActive, ok := got[name]; !ok || gotActive != active {
			t.Fatalf("revision %q active = %v (present %v), want %v", name, gotActive, ok, active)
		}
	}
}

func getRevision(
	t *testing.T, ctx context.Context, c *armappcontainers.ContainerAppsRevisionsClient, name string,
) armappcontainers.ContainerAppsRevisionsClientGetRevisionResponse {
	t.Helper()

	got, err := c.GetRevision(ctx, rgName, appName, name, nil)
	if err != nil {
		t.Fatalf("GetRevision %q: %v", name, err)
	}

	return got
}

func assertActivateDeactivate(
	t *testing.T, ctx context.Context, c *armappcontainers.ContainerAppsRevisionsClient, name string,
) {
	t.Helper()

	if _, err := c.DeactivateRevision(ctx, rgName, appName, name, nil); err != nil {
		t.Fatalf("DeactivateRevision %q: %v", name, err)
	}

	if got := getRevision(t, ctx, c, name); got.Properties.Active == nil || *got.Properties.Active {
		t.Fatalf("after deactivate, %q active = %v, want false", name, got.Properties.Active)
	}

	if _, err := c.ActivateRevision(ctx, rgName, appName, name, nil); err != nil {
		t.Fatalf("ActivateRevision %q: %v", name, err)
	}

	if got := getRevision(t, ctx, c, name); got.Properties.Active == nil || !*got.Properties.Active {
		t.Fatalf("after activate, %q active = %v, want true", name, got.Properties.Active)
	}
}

func assertTrafficSplit(
	t *testing.T, ctx context.Context,
	appClient *armappcontainers.ContainerAppsClient, revClient *armappcontainers.ContainerAppsRevisionsClient,
	envID, rev1, rev2 string,
) {
	t.Helper()

	// A valid 50/50 split across both revisions is accepted.
	putRevisionApp(t, ctx, appClient, envID, "v2", "nginx:2", []*armappcontainers.TrafficWeight{
		{RevisionName: to.Ptr(rev1), Weight: to.Ptr[int32](50)},
		{RevisionName: to.Ptr(rev2), Weight: to.Ptr[int32](50)},
	})

	if got := getRevision(t, ctx, revClient, rev1); got.Properties.TrafficWeight == nil || *got.Properties.TrafficWeight != 50 {
		t.Fatalf("rev1 trafficWeight after split = %v, want 50", got.Properties.TrafficWeight)
	}

	// A split that does not sum to 100 is rejected. The template matches the
	// stored v2 revision, so the request reaches traffic validation rather than
	// tripping the duplicate-suffix guard.
	_, err := appClient.BeginCreateOrUpdate(ctx, rgName, appName, armappcontainers.ContainerApp{
		Location: to.Ptr("eastus"),
		Properties: &armappcontainers.ContainerAppProperties{
			EnvironmentID: to.Ptr(envID),
			Configuration: &armappcontainers.Configuration{
				ActiveRevisionsMode: to.Ptr(armappcontainers.ActiveRevisionsModeMultiple),
				Ingress: &armappcontainers.Ingress{
					External: to.Ptr(true), TargetPort: to.Ptr[int32](80),
					Traffic: []*armappcontainers.TrafficWeight{
						{RevisionName: to.Ptr(rev1), Weight: to.Ptr[int32](50)},
						{RevisionName: to.Ptr(rev2), Weight: to.Ptr[int32](40)},
					},
				},
			},
			Template: &armappcontainers.Template{
				RevisionSuffix: to.Ptr("v2"),
				Containers:     []*armappcontainers.Container{{Name: to.Ptr("main"), Image: to.Ptr("nginx:2")}},
				Scale:          &armappcontainers.Scale{MinReplicas: to.Ptr[int32](2)},
			},
		},
	}, nil)
	if err == nil {
		t.Fatal("traffic split summing to 90 was accepted, want an error")
	}
}

func assertRevisionErrors(t *testing.T, ctx context.Context, c *armappcontainers.ContainerAppsRevisionsClient) {
	t.Helper()

	if _, err := c.GetRevision(ctx, rgName, appName, appName+"--missing", nil); err == nil {
		t.Fatal("GetRevision on a missing revision returned nil error, want NotFound")
	}

	pager := c.NewListRevisionsPager(rgName, "no-such-app", nil)
	if _, err := pager.NextPage(ctx); err == nil {
		t.Fatal("ListRevisions on a missing app returned nil error, want NotFound")
	}
}

func deleteContainerApp(t *testing.T, ctx context.Context, c *armappcontainers.ContainerAppsClient) {
	t.Helper()

	poller, err := c.BeginDelete(ctx, rgName, appName, nil)
	if err != nil {
		t.Fatalf("BeginDelete container app: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("poll container app delete: %v", err)
	}

	if _, err := c.Get(ctx, rgName, appName, nil); err == nil {
		t.Fatal("post-delete Get returned nil error, want NotFound")
	}
}

func deleteEnvironment(t *testing.T, ctx context.Context, c *armappcontainers.ManagedEnvironmentsClient) {
	t.Helper()

	poller, err := c.BeginDelete(ctx, rgName, envName, nil)
	if err != nil {
		t.Fatalf("BeginDelete environment: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("poll environment delete: %v", err)
	}

	if _, err := c.Get(ctx, rgName, envName, nil); err == nil {
		t.Fatal("post-delete Get returned nil error, want NotFound")
	}
}
