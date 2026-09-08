package iothub_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/azure/iothub"
)

func newMock() *iothub.Mock { return iothub.New(config.NewOptions()) }

func sptr(v string) *string { return &v }
func i64ptr(v int64) *int64 { return &v }
func bptr(v bool) *bool     { return &v }

func createHub(t *testing.T, m *iothub.Mock) iothub.Hub {
	t.Helper()

	in := &iothub.HubInput{
		Tags:    map[string]string{"env": "dev"},
		SkuName: sptr("S1"),
	}

	h, isNew, err := m.CreateOrUpdateHub(context.Background(), "sub", "rg", "hub1", "West US", in)
	if err != nil || !isNew {
		t.Fatalf("create hub: err=%v isNew=%v", err, isNew)
	}

	return h
}

func TestCreateHubComputedFields(t *testing.T) {
	m := newMock()
	h := createHub(t, m)

	if h.HostName != "hub1.azure-devices.net" {
		t.Errorf("hostName = %q, want hub1.azure-devices.net", h.HostName)
	}

	if h.ProvisioningState != "Succeeded" {
		t.Errorf("provisioningState = %q, want Succeeded", h.ProvisioningState)
	}

	if h.State != "Active" {
		t.Errorf("state = %q, want Active", h.State)
	}

	if h.Sku.Name != "S1" || h.Sku.Tier != "Standard" || h.Sku.Capacity != 1 {
		t.Errorf("sku = %+v, want {S1 Standard 1}", h.Sku)
	}

	if h.Etag == "" || h.Features != "None" {
		t.Errorf("etag/features = %q/%q", h.Etag, h.Features)
	}

	if h.Events.Path != "hub1" || h.Events.PartitionCount != 4 || len(h.Events.PartitionIDs) != 4 {
		t.Errorf("events endpoint = %+v", h.Events)
	}

	if h.Events.Endpoint == "" {
		t.Error("events endpoint URL empty")
	}
}

func TestDefaultPoliciesSeeded(t *testing.T) {
	m := newMock()
	createHub(t, m)

	policies, err := m.ListKeys(context.Background(), "sub", "rg", "hub1")
	if err != nil {
		t.Fatalf("listkeys: %v", err)
	}

	want := map[string]bool{
		"iothubowner": true, "service": true, "device": true,
		"registryRead": true, "registryReadWrite": true,
	}

	if len(policies) != len(want) {
		t.Fatalf("policy count = %d, want %d", len(policies), len(want))
	}

	for _, p := range policies {
		if !want[p.KeyName] {
			t.Errorf("unexpected policy %q", p.KeyName)
		}

		if p.PrimaryKey == "" || p.SecondaryKey == "" || len(p.PrimaryKey) != 44 {
			t.Errorf("policy %q keys bad: primary=%q secondary=%q", p.KeyName, p.PrimaryKey, p.SecondaryKey)
		}
	}
}

func TestKeysByteStableAcrossReads(t *testing.T) {
	m := newMock()
	createHub(t, m)

	first, _ := m.ListKeys(context.Background(), "sub", "rg", "hub1")
	second, _ := m.ListKeys(context.Background(), "sub", "rg", "hub1")

	a, _ := json.Marshal(first)
	b, _ := json.Marshal(second)

	if string(a) != string(b) {
		t.Errorf("listkeys not byte-stable:\n%s\n%s", a, b)
	}
}

func TestGetKeysForKeyName(t *testing.T) {
	m := newMock()
	createHub(t, m)

	p, err := m.GetKeysForKeyName(context.Background(), "sub", "rg", "hub1", "iothubowner")
	if err != nil {
		t.Fatalf("getKeysForKeyName: %v", err)
	}

	if p.KeyName != "iothubowner" || p.PrimaryKey == "" {
		t.Errorf("policy = %+v", p)
	}

	if _, err := m.GetKeysForKeyName(context.Background(), "sub", "rg", "hub1", "nope"); !cerrors.IsNotFound(err) {
		t.Errorf("missing key: want NotFound, got %v", err)
	}
}

func TestGetHubNotFound(t *testing.T) {
	m := newMock()
	if _, err := m.GetHub(context.Background(), "sub", "rg", "ghost"); !cerrors.IsNotFound(err) {
		t.Errorf("want NotFound, got %v", err)
	}
}

func TestUpdatePreservesComputedAndReplacesTags(t *testing.T) {
	m := newMock()
	orig := createHub(t, m)

	in := &iothub.HubInput{Tags: map[string]string{"team": "iot"}}
	updated, isNew, err := m.CreateOrUpdateHub(context.Background(), "sub", "rg", "hub1", "West US", in)
	if err != nil || isNew {
		t.Fatalf("update: err=%v isNew=%v", err, isNew)
	}

	if updated.Etag != orig.Etag || updated.HostName != orig.HostName {
		t.Errorf("computed fields changed on update")
	}

	if _, ok := updated.Tags["env"]; ok {
		t.Errorf("tags not replaced: %v", updated.Tags)
	}

	if updated.Tags["team"] != "iot" {
		t.Errorf("new tag missing: %v", updated.Tags)
	}
}

func TestPatchScalarPointersRoundTrip(t *testing.T) {
	m := newMock()
	createHub(t, m)

	in := &iothub.HubInput{
		DisableLocalAuth:              bptr(false),
		EnableFileUploadNotifications: bptr(false),
		PartitionCount:                i64ptr(8),
	}
	h, _, err := m.CreateOrUpdateHub(context.Background(), "sub", "rg", "hub1", "West US", in)
	if err != nil {
		t.Fatalf("patch: %v", err)
	}

	if h.DisableLocalAuth == nil || *h.DisableLocalAuth {
		t.Errorf("disableLocalAuth false not round-tripped: %v", h.DisableLocalAuth)
	}

	if h.Events.PartitionCount != 8 || len(h.Events.PartitionIDs) != 8 {
		t.Errorf("partition count not applied: %+v", h.Events)
	}
}

func TestRoutingRoundTripsVerbatim(t *testing.T) {
	m := newMock()
	routing := json.RawMessage(`{"routes":[{"name":"r1","source":"DeviceMessages"}],"fallbackRoute":{"name":"$fallback"}}`)

	in := &iothub.HubInput{Routing: routing}
	h, _, err := m.CreateOrUpdateHub(context.Background(), "sub", "rg", "hubr", "West US", in)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if string(h.Routing) != string(routing) {
		t.Errorf("routing not verbatim: %s", h.Routing)
	}
}

func TestGlobalNameUniqueness(t *testing.T) {
	m := newMock()
	createHub(t, m)

	in := &iothub.HubInput{}
	_, _, err := m.CreateOrUpdateHub(context.Background(), "sub", "otherRG", "hub1", "West US", in)
	if !cerrors.IsAlreadyExists(err) {
		t.Errorf("duplicate name in other RG: want AlreadyExists, got %v", err)
	}
}

func TestListByGroupAndSubscription(t *testing.T) {
	m := newMock()
	createHub(t, m)

	mk := func(rg, name string) {
		if _, _, err := m.CreateOrUpdateHub(context.Background(), "sub", rg, name, "West US", &iothub.HubInput{}); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}
	mk("rg", "hub2")
	mk("rg2", "hub3")

	byRG, _ := m.ListHubsByResourceGroup(context.Background(), "sub", "rg")
	if len(byRG) != 2 {
		t.Errorf("by-rg = %d, want 2", len(byRG))
	}

	bySub, _ := m.ListHubsBySubscription(context.Background(), "sub")
	if len(bySub) != 3 {
		t.Errorf("by-sub = %d, want 3", len(bySub))
	}
}

func TestDeleteHubCascadesConsumerGroups(t *testing.T) {
	m := newMock()
	createHub(t, m)

	if _, _, err := m.CreateOrUpdateConsumerGroup(context.Background(), "sub", "rg", "hub1", "cg1"); err != nil {
		t.Fatalf("create cg: %v", err)
	}

	existed, _ := m.DeleteHub(context.Background(), "sub", "rg", "hub1")
	if !existed {
		t.Fatal("delete hub: not existed")
	}

	if _, err := m.ListConsumerGroups(context.Background(), "sub", "rg", "hub1"); !cerrors.IsNotFound(err) {
		t.Errorf("consumer groups survived hub delete: %v", err)
	}
}

func TestConsumerGroupDefaultSeeded(t *testing.T) {
	m := newMock()
	createHub(t, m)

	groups, err := m.ListConsumerGroups(context.Background(), "sub", "rg", "hub1")
	if err != nil {
		t.Fatalf("list cg: %v", err)
	}

	if len(groups) != 1 || groups[0].Name != "$Default" {
		t.Errorf("default consumer group missing: %+v", groups)
	}
}

func TestConsumerGroupCRUD(t *testing.T) {
	m := newMock()
	createHub(t, m)

	c, isNew, err := m.CreateOrUpdateConsumerGroup(context.Background(), "sub", "rg", "hub1", "telemetry")
	if err != nil || !isNew {
		t.Fatalf("create cg: err=%v isNew=%v", err, isNew)
	}

	if c.Etag == "" {
		t.Error("consumer group etag empty")
	}

	got, err := m.GetConsumerGroup(context.Background(), "sub", "rg", "hub1", "telemetry")
	if err != nil || got.Etag != c.Etag {
		t.Fatalf("get cg: err=%v etag=%q", err, got.Etag)
	}

	groups, _ := m.ListConsumerGroups(context.Background(), "sub", "rg", "hub1")
	if len(groups) != 2 {
		t.Errorf("cg count = %d, want 2 ($Default + telemetry)", len(groups))
	}

	existed, _ := m.DeleteConsumerGroup(context.Background(), "sub", "rg", "hub1", "telemetry")
	if !existed {
		t.Error("delete cg: not existed")
	}
}

func TestConsumerGroupParentMustExist(t *testing.T) {
	m := newMock()
	if _, _, err := m.CreateOrUpdateConsumerGroup(context.Background(), "sub", "rg", "ghost", "cg1"); !cerrors.IsNotFound(err) {
		t.Errorf("create under missing hub: want NotFound, got %v", err)
	}
}

func TestCustomPolicyMergedWithMintedKeys(t *testing.T) {
	m := newMock()

	in := &iothub.HubInput{
		Policies: []iothub.SharedAccessPolicy{{KeyName: "custompolicy", Rights: "RegistryRead"}},
	}
	if _, _, err := m.CreateOrUpdateHub(context.Background(), "sub", "rg", "hubc", "West US", in); err != nil {
		t.Fatalf("create: %v", err)
	}

	p, err := m.GetKeysForKeyName(context.Background(), "sub", "rg", "hubc", "custompolicy")
	if err != nil {
		t.Fatalf("get custom policy: %v", err)
	}

	if p.PrimaryKey == "" || p.SecondaryKey == "" {
		t.Errorf("custom policy keys not minted: %+v", p)
	}
}

func TestPurgeResourceGroup(t *testing.T) {
	m := newMock()
	createHub(t, m)

	if _, _, err := m.CreateOrUpdateHub(context.Background(), "sub", "rg2", "hubkeep", "West US", &iothub.HubInput{}); err != nil {
		t.Fatalf("create sibling: %v", err)
	}

	if err := m.PurgeResourceGroup(context.Background(), "sub", "rg"); err != nil {
		t.Fatalf("purge: %v", err)
	}

	if _, err := m.GetHub(context.Background(), "sub", "rg", "hub1"); !cerrors.IsNotFound(err) {
		t.Errorf("purged hub survived: %v", err)
	}

	if _, err := m.GetHub(context.Background(), "sub", "rg2", "hubkeep"); err != nil {
		t.Errorf("sibling hub removed by purge: %v", err)
	}
}

func TestValidation(t *testing.T) {
	m := newMock()
	cases := []struct{ sub, rg, name, loc string }{
		{"", "rg", "h", "loc"},
		{"sub", "", "h", "loc"},
		{"sub", "rg", "", "loc"},
		{"sub", "rg", "h", ""},
	}

	for _, c := range cases {
		if _, _, err := m.CreateOrUpdateHub(context.Background(), c.sub, c.rg, c.name, c.loc, &iothub.HubInput{}); !cerrors.IsInvalidArgument(err) {
			t.Errorf("case %+v: want InvalidArgument, got %v", c, err)
		}
	}
}

func TestSnapshotRestore(t *testing.T) {
	m := newMock()
	createHub(t, m)

	if _, _, err := m.CreateOrUpdateConsumerGroup(context.Background(), "sub", "rg", "hub1", "cg1"); err != nil {
		t.Fatalf("create cg: %v", err)
	}

	data, err := m.Snapshot(context.Background(), false)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	restored := newMock()
	if err := restored.Restore(context.Background(), data); err != nil {
		t.Fatalf("restore: %v", err)
	}

	h, err := restored.GetHub(context.Background(), "sub", "rg", "hub1")
	if err != nil {
		t.Fatalf("get restored hub: %v", err)
	}

	if h.HostName != "hub1.azure-devices.net" {
		t.Errorf("restored hostName = %q", h.HostName)
	}

	keys, _ := restored.ListKeys(context.Background(), "sub", "rg", "hub1")
	if len(keys) != 5 {
		t.Errorf("restored keys = %d, want 5", len(keys))
	}

	groups, _ := restored.ListConsumerGroups(context.Background(), "sub", "rg", "hub1")
	if len(groups) != 2 {
		t.Errorf("restored consumer groups = %d, want 2", len(groups))
	}
}
