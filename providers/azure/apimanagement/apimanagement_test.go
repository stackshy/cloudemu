package apimanagement_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/azure/apimanagement"
)

const (
	sub = "sub"
	rg  = "rg"
)

//nolint:gochecknoglobals // fixed test clock origin
var epoch = time.Date(2026, 9, 1, 10, 30, 0, 0, time.UTC)

func newMock() (*apimanagement.Mock, *config.FakeClock) {
	clk := config.NewFakeClock(epoch)
	return apimanagement.New(config.NewOptions(config.WithClock(clk))), clk
}

func sptr(v string) *string { return &v }

func i32(v int32) *int32 { return &v }

func devInput() *apimanagement.ServiceInput {
	return &apimanagement.ServiceInput{
		Tags:        map[string]string{"env": "dev"},
		SkuName:     sptr("Developer"),
		SkuCapacity: i32(1),
		Identity:    &apimanagement.ManagedIdentity{Type: "SystemAssigned"},
		Properties:  json.RawMessage(`{"publisherEmail":"a@b.test","publisherName":"Contoso","customProperties":{"k":"v"}}`),
	}
}

func create(t *testing.T, m *apimanagement.Mock, name string) apimanagement.Service {
	t.Helper()

	s, created, err := m.CreateOrUpdateService(context.Background(), sub, rg, name, "East US", devInput())
	if err != nil || !created {
		t.Fatalf("create %s: err=%v created=%v", name, err, created)
	}

	return s
}

func TestCreateComputedFields(t *testing.T) {
	m, _ := newMock()
	s := create(t, m, "Apim1")

	if s.ProvisioningState != "Succeeded" || s.Etag == "" {
		t.Fatalf("computed fields not minted: %+v", s)
	}

	if !s.CreatedAt.Equal(epoch) {
		t.Errorf("createdAt = %v, want %v", s.CreatedAt, epoch)
	}

	if s.Identity == nil || s.Identity.PrincipalID == "" || s.Identity.TenantID == "" {
		t.Fatalf("system-assigned identity not synthesized: %+v", s.Identity)
	}

	if got := s.Endpoints().Gateway; got != "https://apim1.azure-api.net" {
		t.Errorf("gateway url = %q", got)
	}

	if got := s.Endpoints().Scm; got != "https://apim1.scm.azure-api.net" {
		t.Errorf("scm url = %q", got)
	}

	want := "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.ApiManagement/service/Apim1"
	if s.ARMID() != want {
		t.Errorf("ARM id = %q, want %q", s.ARMID(), want)
	}
}

// TestReplaceKeepsStableFieldsAndRotatesEtag: a replace keeps createdAt, location
// and the principal, but mints a new etag (every write changes it).
func TestReplaceKeepsStableFieldsAndRotatesEtag(t *testing.T) {
	m, clk := newMock()
	first := create(t, m, "apim1")

	clk.Advance(time.Hour)

	in := devInput()
	in.Tags = nil
	in.SkuName = sptr("premium")
	in.SkuCapacity = i32(2)

	got, created, err := m.CreateOrUpdateService(context.Background(), sub, rg, "apim1", "eastus", in)
	if err != nil || created {
		t.Fatalf("replace: err=%v created=%v", err, created)
	}

	if !got.CreatedAt.Equal(first.CreatedAt) || got.Location != "East US" {
		t.Errorf("replace changed stable fields: %+v", got)
	}

	if got.Etag == first.Etag || got.Etag == "" {
		t.Errorf("replace must rotate the etag, got %q (was %q)", got.Etag, first.Etag)
	}

	if got.SkuName != "Premium" || got.SkuCapacity != 2 {
		t.Errorf("sku = %s/%d, want Premium/2", got.SkuName, got.SkuCapacity)
	}

	if got.Tags != nil {
		t.Errorf("PUT without tags must clear them, got %v", got.Tags)
	}

	if got.Identity.PrincipalID != first.Identity.PrincipalID {
		t.Error("principalId changed across a replace")
	}
}

func TestCreateValidation(t *testing.T) {
	cases := map[string]struct {
		name     string
		location string
		mutate   func(*apimanagement.ServiceInput)
	}{
		"missing location":      {"svc", "", nil},
		"name starts digit":     {"1svc", "eastus", nil},
		"name trailing hyphen":  {"svc-", "eastus", nil},
		"name bad char":         {"svc_1", "eastus", nil},
		"name too long":         {"a" + strings.Repeat("b", 50), "eastus", nil},
		"missing sku":           {"svc", "eastus", func(in *apimanagement.ServiceInput) { in.SkuName = nil }},
		"unknown sku":           {"svc", "eastus", func(in *apimanagement.ServiceInput) { in.SkuName = sptr("Gold") }},
		"missing capacity":      {"svc", "eastus", func(in *apimanagement.ServiceInput) { in.SkuCapacity = nil }},
		"zero capacity dev":     {"svc", "eastus", func(in *apimanagement.ServiceInput) { in.SkuCapacity = i32(0) }},
		"consumption capacity1": {"svc", "eastus", func(in *apimanagement.ServiceInput) { in.SkuName = sptr("Consumption") }},
		"missing email": {"svc", "eastus", func(in *apimanagement.ServiceInput) {
			in.Properties = json.RawMessage(`{"publisherName":"Contoso"}`)
		}},
		"missing publisher name": {"svc", "eastus", func(in *apimanagement.ServiceInput) {
			in.Properties = json.RawMessage(`{"publisherEmail":"a@b.test"}`)
		}},
	}

	for label, tc := range cases {
		t.Run(label, func(t *testing.T) {
			m, _ := newMock()
			in := devInput()

			if tc.mutate != nil {
				tc.mutate(in)
			}

			_, _, err := m.CreateOrUpdateService(context.Background(), sub, rg, tc.name, tc.location, in)
			if !cerrors.IsInvalidArgument(err) {
				t.Fatalf("err = %v, want InvalidArgument", err)
			}
		})
	}
}

func TestConsumptionZeroCapacityAccepted(t *testing.T) {
	m, _ := newMock()
	in := devInput()
	in.SkuName = sptr("Consumption")
	in.SkuCapacity = i32(0)

	s, _, err := m.CreateOrUpdateService(context.Background(), sub, rg, "serverless", "eastus", in)
	if err != nil {
		t.Fatalf("create consumption: %v", err)
	}

	if s.SkuName != "Consumption" || s.SkuCapacity != 0 {
		t.Errorf("sku = %s/%d", s.SkuName, s.SkuCapacity)
	}
}

func TestPatchMergesPropertiesAndReplacesTags(t *testing.T) {
	m, _ := newMock()
	create(t, m, "apim1")

	got, err := m.UpdateService(context.Background(), sub, rg, "apim1", &apimanagement.ServiceInput{
		Tags:       map[string]string{"team": "api"},
		Properties: json.RawMessage(`{"publisherName":"Fabrikam"}`),
	})
	if err != nil {
		t.Fatalf("patch: %v", err)
	}

	if len(got.Tags) != 1 || got.Tags["team"] != "api" {
		t.Errorf("tags = %v, want exactly team=api", got.Tags)
	}

	var props map[string]any
	if err := json.Unmarshal(got.Properties, &props); err != nil {
		t.Fatalf("props: %v", err)
	}

	if props["publisherName"] != "Fabrikam" || props["publisherEmail"] != "a@b.test" || props["customProperties"] == nil {
		t.Errorf("merge lost keys: %v", props)
	}

	if got.SkuName != "Developer" || got.SkuCapacity != 1 {
		t.Errorf("patch without sku changed it: %s/%d", got.SkuName, got.SkuCapacity)
	}
}

func TestPatchRejectsInvalidResult(t *testing.T) {
	m, _ := newMock()
	create(t, m, "apim1")

	ctx := context.Background()

	_, err := m.UpdateService(ctx, sub, rg, "apim1", &apimanagement.ServiceInput{SkuName: sptr("Consumption")})
	if !cerrors.IsInvalidArgument(err) {
		t.Fatalf("Consumption with capacity 1: err = %v, want InvalidArgument", err)
	}

	_, err = m.UpdateService(ctx, sub, rg, "apim1", &apimanagement.ServiceInput{
		Properties: json.RawMessage(`{"publisherEmail":""}`),
	})
	if !cerrors.IsInvalidArgument(err) {
		t.Fatalf("blank publisherEmail: err = %v, want InvalidArgument", err)
	}

	s, err := m.GetService(ctx, sub, rg, "apim1")
	if err != nil || s.SkuName != "Developer" {
		t.Fatalf("a rejected PATCH must not persist: %+v, %v", s, err)
	}

	_, err = m.UpdateService(ctx, sub, rg, "missing", &apimanagement.ServiceInput{})
	if !cerrors.IsNotFound(err) {
		t.Fatalf("patch missing: err = %v, want NotFound", err)
	}
}

func TestListDeleteAndPurge(t *testing.T) {
	m, _ := newMock()
	ctx := context.Background()

	create(t, m, "b-svc")
	create(t, m, "a-svc")

	if _, _, err := m.CreateOrUpdateService(ctx, sub, "other", "c-svc", "eastus", devInput()); err != nil {
		t.Fatalf("create other: %v", err)
	}

	list, _ := m.ListServicesByResourceGroup(ctx, sub, "RG")
	if len(list) != 2 || list[0].Name != "a-svc" {
		t.Fatalf("list by rg = %+v", list)
	}

	all, _ := m.ListServicesBySubscription(ctx, sub)
	if len(all) != 3 {
		t.Fatalf("list by sub = %d, want 3", len(all))
	}

	if existed, _ := m.DeleteService(ctx, sub, rg, "a-svc"); !existed {
		t.Fatal("delete existing reported not existed")
	}

	if existed, _ := m.DeleteService(ctx, sub, rg, "a-svc"); existed {
		t.Fatal("second delete reported existed")
	}

	if _, err := m.GetService(ctx, sub, rg, "a-svc"); !cerrors.IsNotFound(err) {
		t.Fatalf("get deleted: err = %v", err)
	}

	if err := m.PurgeResourceGroup(ctx, sub, rg); err != nil {
		t.Fatalf("purge: %v", err)
	}

	left, _ := m.DiscoverServices(ctx)
	if len(left) != 1 || left[0].Name != "c-svc" {
		t.Fatalf("after purge = %+v, want only c-svc", left)
	}
}

func TestSnapshotRoundTrip(t *testing.T) {
	m, _ := newMock()
	orig := create(t, m, "apim1")

	data, err := m.Snapshot(context.Background(), false)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	restored, _ := newMock()
	if err := restored.Restore(context.Background(), data); err != nil {
		t.Fatalf("restore: %v", err)
	}

	got, err := restored.GetService(context.Background(), sub, rg, "apim1")
	if err != nil {
		t.Fatalf("get restored: %v", err)
	}

	if got.Etag != orig.Etag || !got.CreatedAt.Equal(orig.CreatedAt) ||
		got.Identity.PrincipalID != orig.Identity.PrincipalID || string(got.Properties) != string(orig.Properties) {
		t.Errorf("restored service differs:\n got %+v\nwant %+v", got, orig)
	}
}
