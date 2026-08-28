package serverkit

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	cloudemu "github.com/stackshy/cloudemu/v2"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
	"github.com/stackshy/cloudemu/v2/services/resourcediscovery"
)

func TestServeCostEstimatesInventory(t *testing.T) {
	ctx := context.Background()
	aws := cloudemu.NewAWS()

	// Two running instances → an always-on estimate the cost endpoint should sum.
	if _, err := aws.EC2.RunInstances(ctx, computedriver.InstanceConfig{ImageID: "ami-1", InstanceType: "t3.micro"}, 2); err != nil {
		t.Fatalf("run instances: %v", err)
	}

	engines := map[string]*resourcediscovery.Engine{"aws": aws.ResourceDiscovery}

	rec := httptest.NewRecorder()
	serveCost(rec, httptest.NewRequest(http.MethodGet, "/_cloudemu/cost", nil), engines)
	if rec.Code != http.StatusOK {
		t.Fatalf("cost status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}

	var resp struct {
		EstimatedMonthlyUSD float64    `json:"estimatedMonthlyUsd"`
		Resources           []costLine `json:"resources"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.EstimatedMonthlyUSD <= 0 || len(resp.Resources) != 2 {
		t.Fatalf("cost = $%.2f over %d resources, want >0 over 2 instances", resp.EstimatedMonthlyUSD, len(resp.Resources))
	}
}

func TestServeCostEmptyInventory(t *testing.T) {
	engines := map[string]*resourcediscovery.Engine{"aws": cloudemu.NewAWS().ResourceDiscovery}

	rec := httptest.NewRecorder()
	serveCost(rec, httptest.NewRequest(http.MethodGet, "/_cloudemu/cost", nil), engines)
	if rec.Code != http.StatusOK {
		t.Fatalf("cost status = %d, want 200", rec.Code)
	}

	var resp struct {
		EstimatedMonthlyUSD float64 `json:"estimatedMonthlyUsd"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.EstimatedMonthlyUSD != 0 {
		t.Fatalf("empty inventory cost = $%.2f, want 0", resp.EstimatedMonthlyUSD)
	}
}
