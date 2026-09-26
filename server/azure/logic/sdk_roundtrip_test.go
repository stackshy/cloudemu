package logic_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/logic/armlogic"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

const (
	sdkSub = "00000000-0000-0000-0000-00000000a0a0"
	sdkRG  = "rg-logic"
)

type fakeCred struct{}

func (fakeCred) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "fake", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

type sdkEnv struct {
	ts        *httptest.Server
	workflows *armlogic.WorkflowsClient
	groups    *armresources.ResourceGroupsClient
}

func newSDKEnv(t *testing.T) *sdkEnv {
	t.Helper()

	cloudP := cloudemu.NewAzure(config.WithAccountID(sdkSub))
	ts := httptest.NewTLSServer(azureserver.NewFromProvider(cloudP))
	t.Cleanup(ts.Close)

	opts := &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Cloud: cloud.Configuration{
				ActiveDirectoryAuthorityHost: "https://login.microsoftonline.com/",
				Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
					cloud.ResourceManager: {Endpoint: ts.URL, Audience: "https://management.azure.com"},
				},
			},
			Transport: ts.Client(),
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
	}

	wfc, err := armlogic.NewWorkflowsClient(sdkSub, fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	rgc, err := armresources.NewResourceGroupsClient(sdkSub, fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := rgc.CreateOrUpdate(context.Background(), sdkRG,
		armresources.ResourceGroup{Location: to.Ptr("eastus")}, nil); err != nil {
		t.Fatalf("create resource group: %v", err)
	}

	return &sdkEnv{ts: ts, workflows: wfc, groups: rgc}
}

func sdkDefinition() map[string]any {
	return map[string]any{
		"$schema":        "https://schema.management.azure.com/providers/Microsoft.Logic/schemas/2016-06-01/workflowdefinition.json#",
		"contentVersion": "1.0.0.0",
		"triggers": map[string]any{
			"manual": map[string]any{"type": "Request", "kind": "Http"},
		},
		"actions": map[string]any{},
	}
}

func createWorkflow(t *testing.T, env *sdkEnv, name string) armlogic.Workflow {
	t.Helper()

	resp, err := env.workflows.CreateOrUpdate(context.Background(), sdkRG, name, armlogic.Workflow{
		Location: to.Ptr("eastus"),
		Tags:     map[string]*string{"env": to.Ptr("dev")},
		Properties: &armlogic.WorkflowProperties{
			Definition: sdkDefinition(),
			Parameters: map[string]*armlogic.WorkflowParameter{
				"greeting": {Type: to.Ptr(armlogic.ParameterTypeString), Value: "hello"},
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("CreateOrUpdate %s: %v", name, err)
	}

	return resp.Workflow
}

func TestSDKWorkflowLifecycle(t *testing.T) {
	env := newSDKEnv(t)
	ctx := context.Background()
	wf := createWorkflow(t, env, "wf-sdk")

	p := wf.Properties
	if p == nil || *p.ProvisioningState != armlogic.WorkflowProvisioningStateSucceeded ||
		*p.State != armlogic.WorkflowStateEnabled || p.AccessEndpoint == nil || p.Version == nil ||
		p.CreatedTime == nil || p.ChangedTime == nil {
		t.Fatalf("create response properties: %+v", p)
	}

	if *wf.Type != "Microsoft.Logic/workflows" || *wf.Name != "wf-sdk" {
		t.Errorf("type/name = %q/%q", *wf.Type, *wf.Name)
	}

	got, err := env.workflows.Get(ctx, sdkRG, "wf-sdk", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	def, _ := got.Properties.Definition.(map[string]any)
	if trig, _ := def["triggers"].(map[string]any); trig["manual"] == nil {
		t.Errorf("definition did not round-trip: %v", got.Properties.Definition)
	}

	if prm := got.Properties.Parameters["greeting"]; prm == nil || prm.Value != "hello" {
		t.Errorf("parameters did not round-trip: %+v", got.Properties.Parameters)
	}

	// armlogic's Update sends a body-less PATCH: it must succeed and echo the workflow.
	upd, err := env.workflows.Update(ctx, sdkRG, "wf-sdk", nil)
	if err != nil || *upd.Name != "wf-sdk" || *upd.Tags["env"] != "dev" {
		t.Fatalf("Update: err=%v wf=%+v", err, upd.Workflow)
	}

	if _, err := env.workflows.Disable(ctx, sdkRG, "wf-sdk", nil); err != nil {
		t.Fatalf("Disable: %v", err)
	}

	if got, _ := env.workflows.Get(ctx, sdkRG, "wf-sdk", nil); *got.Properties.State != armlogic.WorkflowStateDisabled {
		t.Errorf("state after Disable = %s", *got.Properties.State)
	}

	if _, err := env.workflows.Enable(ctx, sdkRG, "wf-sdk", nil); err != nil {
		t.Fatalf("Enable: %v", err)
	}

	if got, _ := env.workflows.Get(ctx, sdkRG, "wf-sdk", nil); *got.Properties.State != armlogic.WorkflowStateEnabled {
		t.Errorf("state after Enable = %s", *got.Properties.State)
	}

	if _, err := env.workflows.Delete(ctx, sdkRG, "wf-sdk", nil); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	assertNotFound(t, env, "wf-sdk")
}

func TestSDKWorkflowListAndMissing(t *testing.T) {
	env := newSDKEnv(t)
	ctx := context.Background()

	createWorkflow(t, env, "wf-b")
	createWorkflow(t, env, "wf-a")

	page, err := env.workflows.NewListByResourceGroupPager(sdkRG, nil).NextPage(ctx)
	if err != nil || len(page.Value) != 2 || *page.Value[0].Name != "wf-a" {
		t.Fatalf("ListByResourceGroup: err=%v value=%+v", err, page.Value)
	}

	subPage, err := env.workflows.NewListBySubscriptionPager(nil).NextPage(ctx)
	if err != nil || len(subPage.Value) != 2 {
		t.Fatalf("ListBySubscription: err=%v len=%d", err, len(subPage.Value))
	}

	assertNotFound(t, env, "missing")

	if _, err := env.workflows.Enable(ctx, sdkRG, "missing", nil); !isStatus(err, http.StatusNotFound) {
		t.Errorf("Enable missing: err=%v, want 404", err)
	}
}

func TestSDKResourceGroupDeleteCascades(t *testing.T) {
	env := newSDKEnv(t)
	ctx := context.Background()

	createWorkflow(t, env, "wf-cascade")

	poller, err := env.groups.BeginDelete(ctx, sdkRG, nil)
	if err != nil {
		t.Fatalf("BeginDelete rg: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond}); err != nil {
		t.Fatalf("rg delete poll: %v", err)
	}

	// Recreate the group: the workflow must not have survived the cascade.
	if _, err := env.groups.CreateOrUpdate(ctx, sdkRG, armresources.ResourceGroup{Location: to.Ptr("eastus")}, nil); err != nil {
		t.Fatalf("recreate rg: %v", err)
	}

	assertNotFound(t, env, "wf-cascade")
}

func TestSDKWorkflowInResourceGraph(t *testing.T) {
	env := newSDKEnv(t)
	createWorkflow(t, env, "wf-arg")

	body, _ := json.Marshal(map[string]any{
		"subscriptions": []string{sdkSub},
		"query":         "Resources | where type == 'microsoft.logic/workflows'",
	})

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost,
		env.ts.URL+"/providers/Microsoft.ResourceGraph/resources?api-version=2021-03-01", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := env.ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	var out struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("ARG: status=%d err=%v", resp.StatusCode, err)
	}

	if len(out.Data) != 1 || out.Data[0]["name"] != "wf-arg" || out.Data[0]["type"] != "microsoft.logic/workflows" {
		t.Fatalf("ARG rows = %+v", out.Data)
	}
}

func assertNotFound(t *testing.T, env *sdkEnv, name string) {
	t.Helper()

	_, err := env.workflows.Get(context.Background(), sdkRG, name, nil)

	var re *azcore.ResponseError
	if !errors.As(err, &re) || re.StatusCode != http.StatusNotFound || re.ErrorCode != "ResourceNotFound" {
		t.Fatalf("Get %s: err=%v, want 404 ResourceNotFound", name, err)
	}
}

func isStatus(err error, status int) bool {
	var re *azcore.ResponseError
	return errors.As(err, &re) && re.StatusCode == status
}
