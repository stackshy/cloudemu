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
// (SKU, tags, kind) is kept.
func TestSDKAzureAppServicePlanUpdate(t *testing.T) {
	_, client := newPlanPatchServer(t)
	ctx := context.Background()

	resp, err := client.Update(ctx, rgName, "patch-plan", armappservice.PlanPatchResource{
		Properties: &armappservice.PlanPatchResourceProperties{
			MaximumElasticWorkerCount: to.Ptr[int32](20),
			Reserved:                  to.Ptr(true),
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

	if pp.Reserved == nil || !*pp.Reserved {
		t.Errorf("%s: reserved=%v want true", stage, pp.Reserved)
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
