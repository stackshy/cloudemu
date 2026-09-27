package functions_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appservice/armappservice/v3"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

func newPlanPatchServer(t *testing.T) (*httptest.Server, *armappservice.PlansClient) {
	t.Helper()

	cloudP := cloudemu.NewAzure()
	ts := httptest.NewTLSServer(azureserver.New(azureserver.Drivers{Functions: cloudP.Functions}))
	t.Cleanup(ts.Close)

	ensureRG(t, ts.Client(), ts.URL, subID, rgName)

	client := newPlansClient(t, ts)

	poller, err := client.BeginCreateOrUpdate(context.Background(), rgName, "patch-plan", armappservice.Plan{
		Kind:     to.Ptr("elastic"),
		Location: to.Ptr("eastus"),
		Tags:     map[string]*string{"env": to.Ptr("dev")},
		SKU:      &armappservice.SKUDescription{Name: to.Ptr("EP1"), Capacity: to.Ptr[int32](1)},
		Properties: &armappservice.PlanProperties{
			MaximumElasticWorkerCount: to.Ptr[int32](5),
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}

	if _, err := poller.PollUntilDone(context.Background(), &runtimePollerOptions); err != nil {
		t.Fatalf("PollUntilDone: %v", err)
	}

	return ts, client
}

// TestSDKAzureAppServicePlanUpdate drives armappservice PlansClient.Update
// (PATCH): the changed properties land, and everything the body omitted
// (SKU, tags, kind, reserved) is kept.
func TestSDKAzureAppServicePlanUpdate(t *testing.T) {
	_, client := newPlanPatchServer(t)
	ctx := context.Background()

	resp, err := client.Update(ctx, rgName, "patch-plan", armappservice.PlanPatchResource{
		Properties: &armappservice.PlanPatchResourceProperties{
			MaximumElasticWorkerCount: to.Ptr[int32](20),
			PerSiteScaling:            to.Ptr(true),
			ZoneRedundant:             to.Ptr(true),
		},
	}, nil)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	assertPatchedPlan(t, "update response", resp.Plan)

	got, err := client.Get(ctx, rgName, "patch-plan", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	assertPatchedPlan(t, "get", got.Plan)
}

func assertPatchedPlan(t *testing.T, stage string, p armappservice.Plan) {
	t.Helper()

	if p.Properties == nil {
		t.Fatalf("%s: properties nil", stage)
	}

	pp := p.Properties

	if pp.MaximumElasticWorkerCount == nil || *pp.MaximumElasticWorkerCount != 20 {
		t.Errorf("%s: maximumElasticWorkerCount=%v want 20", stage, pp.MaximumElasticWorkerCount)
	}

	if pp.PerSiteScaling == nil || !*pp.PerSiteScaling {
		t.Errorf("%s: perSiteScaling=%v want true", stage, pp.PerSiteScaling)
	}

	if pp.Reserved == nil || *pp.Reserved {
		t.Errorf("%s: reserved=%v want false (unchanged)", stage, pp.Reserved)
	}

	if pp.ZoneRedundant == nil || !*pp.ZoneRedundant {
		t.Errorf("%s: zoneRedundant=%v want true", stage, pp.ZoneRedundant)
	}

	if p.SKU == nil || *p.SKU.Name != "EP1" || *p.SKU.Tier != "ElasticPremium" {
		t.Errorf("%s: sku=%+v want EP1/ElasticPremium (unchanged)", stage, p.SKU)
	}

	if p.Kind == nil || *p.Kind != "elastic" {
		t.Errorf("%s: kind=%v want elastic (unchanged)", stage, p.Kind)
	}

	if len(p.Tags) != 1 || *p.Tags["env"] != "dev" {
		t.Errorf("%s: tags=%v want {env:dev} (unchanged)", stage, p.Tags)
	}
}

// TestAzureAppServicePlanPatchSKUAndTags sends the sku and tags a raw ARM
// PATCH may carry (armappservice.PlanPatchResource has no fields for them), and
// reads the result back through the SDK. A new SKU name re-derives the tier;
// tags are replaced wholesale.
func TestAzureAppServicePlanPatchSKUAndTags(t *testing.T) {
	ts, client := newPlanPatchServer(t)

	body := `{"sku":{"name":"EP2","capacity":3},"tags":{"team":"payments"}}`

	status, out := rawPlanPatch(t, ts, "patch-plan", body)
	if status != http.StatusOK {
		t.Fatalf("PATCH status=%d body=%s", status, out)
	}

	got, err := client.Get(context.Background(), rgName, "patch-plan", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if *got.SKU.Name != "EP2" || *got.SKU.Tier != "ElasticPremium" || *got.SKU.Capacity != 3 {
		t.Errorf("sku=%s/%s/%d want EP2/ElasticPremium/3", *got.SKU.Name, *got.SKU.Tier, *got.SKU.Capacity)
	}

	if len(got.Tags) != 1 || got.Tags["team"] == nil || *got.Tags["team"] != "payments" {
		t.Errorf("tags=%v want exactly {team:payments}", got.Tags)
	}

	if got.Properties.MaximumElasticWorkerCount == nil || *got.Properties.MaximumElasticWorkerCount != 5 {
		t.Errorf("maximumElasticWorkerCount=%v want 5 (unchanged)", got.Properties.MaximumElasticWorkerCount)
	}

	var resp map[string]any
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("decode PATCH body: %v", err)
	}

	if resp["name"] != "patch-plan" {
		t.Errorf("PATCH response name=%v want patch-plan", resp["name"])
	}
}

// TestSDKAzureAppServicePlanUpdateMissing checks a PATCH against a plan that
// does not exist (or lives in another resource group) is a 404.
func TestSDKAzureAppServicePlanUpdateMissing(t *testing.T) {
	_, client := newPlanPatchServer(t)

	_, err := client.Update(context.Background(), rgName, "no-such-plan", armappservice.PlanPatchResource{
		Properties: &armappservice.PlanPatchResourceProperties{Reserved: to.Ptr(true)},
	}, nil)

	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) || respErr.StatusCode != http.StatusNotFound {
		t.Fatalf("err=%v, want 404 ResponseError", err)
	}
}

func rawPlanPatch(t *testing.T, ts *httptest.Server, name, body string) (int, []byte) {
	t.Helper()

	url := ts.URL + "/subscriptions/" + subID + "/resourceGroups/" + rgName +
		"/providers/Microsoft.Web/serverfarms/" + name + "?api-version=2023-01-01"

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPatch, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var buf strings.Builder

	if _, err := io.Copy(&buf, resp.Body); err != nil {
		t.Fatal(err)
	}

	return resp.StatusCode, []byte(buf.String())
}

// TestSDKAzureAppServicePlanOSChangeRefused checks both write paths refuse to
// change an existing plan's OS with the 400 BadRequest real Azure returns
// ("You cannot change the OS hosting your app at this time", Azure/bicep#5724):
// a PATCH of reserved or kind, and a re-PUT that omits reserved.
func TestSDKAzureAppServicePlanOSChangeRefused(t *testing.T) {
	ts, client := newPlanPatchServer(t)
	ctx := context.Background()

	poller, err := client.BeginCreateOrUpdate(ctx, rgName, "linux-plan", armappservice.Plan{
		Kind:       to.Ptr("linux"),
		Location:   to.Ptr("eastus"),
		SKU:        &armappservice.SKUDescription{Name: to.Ptr("P1v3")},
		Properties: &armappservice.PlanProperties{Reserved: to.Ptr(true)},
	}, nil)
	if err != nil {
		t.Fatalf("create linux plan: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, &runtimePollerOptions); err != nil {
		t.Fatalf("PollUntilDone: %v", err)
	}

	_, err = client.Update(ctx, rgName, "linux-plan", armappservice.PlanPatchResource{
		Properties: &armappservice.PlanPatchResourceProperties{Reserved: to.Ptr(false)},
	}, nil)
	wantPlanErr(t, "PATCH reserved:false", err, "BadRequest")

	status, out := rawPlanPatch(t, ts, "linux-plan", `{"kind":"app"}`)
	if status != http.StatusBadRequest || !strings.Contains(string(out), `"BadRequest"`) {
		t.Errorf("PATCH kind:app: status=%d body=%s, want 400 BadRequest", status, out)
	}

	_, err = client.BeginCreateOrUpdate(ctx, rgName, "linux-plan", armappservice.Plan{
		Kind:     to.Ptr("linux"),
		Location: to.Ptr("eastus"),
		SKU:      &armappservice.SKUDescription{Name: to.Ptr("P1v3")},
	}, nil)
	wantPlanErr(t, "re-PUT without reserved", err, "BadRequest")

	got, err := client.Get(ctx, rgName, "linux-plan", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Properties.Reserved == nil || !*got.Properties.Reserved || *got.Kind != "linux" {
		t.Errorf("refused OS change mutated the plan: kind=%s reserved=%v", *got.Kind, got.Properties.Reserved)
	}
}

// TestAzureAppServicePlanPatchSKUValidation checks a PATCH with a capacity
// below 1, above the tier maximum, an unknown SKU or a move out of the Elastic
// Premium family is a 400, and leaves the plan unchanged.
func TestAzureAppServicePlanPatchSKUValidation(t *testing.T) {
	ts, client := newPlanPatchServer(t)

	for _, body := range []string{
		`{"sku":{"capacity":-5}}`,
		`{"sku":{"capacity":0}}`,
		`{"sku":{"name":"ZZ9"}}`,
		`{"sku":{"name":"S1"}}`,
		`{"sku":{"name":"EP2","tier":"Standard"}}`,
	} {
		status, out := rawPlanPatch(t, ts, "patch-plan", body)
		if status != http.StatusBadRequest || !strings.Contains(string(out), `"InvalidParameter"`) {
			t.Errorf("PATCH %s: status=%d body=%s, want 400 InvalidParameter", body, status, out)
		}
	}

	got, err := client.Get(context.Background(), rgName, "patch-plan", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if *got.SKU.Name != "EP1" || *got.SKU.Tier != "ElasticPremium" || *got.SKU.Capacity != 1 {
		t.Errorf("sku=%s/%s/%d want EP1/ElasticPremium/1 (unchanged)", *got.SKU.Name, *got.SKU.Tier, *got.SKU.Capacity)
	}
}

func wantPlanErr(t *testing.T, what string, err error, code string) {
	t.Helper()

	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) || respErr.StatusCode != http.StatusBadRequest || respErr.ErrorCode != code {
		t.Errorf("%s: err=%v, want 400 %s", what, err, code)
	}
}
