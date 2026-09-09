package iothub_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/azure/iothub"
)

func TestARMIDShapes(t *testing.T) {
	m := newMock()
	createHub(t, m)

	hubs, _ := m.DiscoverHubs(context.Background())
	if len(hubs) != 1 {
		t.Fatalf("discover = %d, want 1", len(hubs))
	}

	id := hubs[0].ARMID()
	if !strings.Contains(id, "/providers/Microsoft.Devices/IotHubs/hub1") {
		t.Errorf("hub ARMID = %q", id)
	}

	if _, _, err := m.CreateOrUpdateConsumerGroup(context.Background(), "sub", "rg", "hub1", "cg1"); err != nil {
		t.Fatalf("create cg: %v", err)
	}

	c, _ := m.GetConsumerGroup(context.Background(), "sub", "rg", "hub1", "cg1")
	if !strings.HasSuffix(c.ARMID(), "/eventHubEndpoints/events/ConsumerGroups/cg1") {
		t.Errorf("cg ARMID = %q", c.ARMID())
	}

	if c.ARMType() != "Microsoft.Devices/IotHubs/EventHubEndpoints/ConsumerGroups" {
		t.Errorf("cg ARMType = %q", c.ARMType())
	}
}

func TestSkuTierDerivation(t *testing.T) {
	m := iothub.New(config.NewOptions())

	cases := map[string]string{"F1": "Free", "B1": "Basic", "S2": "Standard", "S3": "Standard"}
	i := 0

	for sku, tier := range cases {
		name := "hub-sku-" + sku
		in := &iothub.HubInput{SkuName: &sku, SkuCapacity: i64ptr(int64(2 + i))}
		i++

		h, _, err := m.CreateOrUpdateHub(context.Background(), "sub", "rg", name, "West US", in)
		if err != nil {
			t.Fatalf("create %s: %v", sku, err)
		}

		if h.Sku.Tier != tier {
			t.Errorf("sku %s tier = %q, want %q", sku, h.Sku.Tier, tier)
		}

		if h.Sku.Capacity != int64(2+i-1) {
			t.Errorf("sku %s capacity = %d", sku, h.Sku.Capacity)
		}
	}
}

func TestScalarPassthroughAndPolicyOverride(t *testing.T) {
	m := newMock()
	createHub(t, m)

	in := &iothub.HubInput{
		MinTLSVersion:       sptr("1.2"),
		PublicNetworkAccess: sptr("Enabled"),
		Features:            sptr("DeviceManagement"),
		// Override an existing default policy's rights.
		Policies: []iothub.SharedAccessPolicy{
			{KeyName: "service", Rights: "ServiceConnect", PrimaryKey: "explicit-primary", SecondaryKey: "explicit-secondary"},
		},
	}

	h, _, err := m.CreateOrUpdateHub(context.Background(), "sub", "rg", "hub1", "West US", in)
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	if h.MinTLSVersion != "1.2" || h.PublicNetworkAccess != "Enabled" || h.Features != "DeviceManagement" {
		t.Errorf("scalars not applied: %+v", h)
	}

	p, _ := m.GetKeysForKeyName(context.Background(), "sub", "rg", "hub1", "service")
	if p.PrimaryKey != "explicit-primary" {
		t.Errorf("explicit policy key not honored: %+v", p)
	}

	// Still five policies — override did not append a duplicate.
	keys, _ := m.ListKeys(context.Background(), "sub", "rg", "hub1")
	if len(keys) != 5 {
		t.Errorf("policy count after override = %d, want 5", len(keys))
	}
}

func TestListKeysAndConsumerGroupsMissingHub(t *testing.T) {
	m := newMock()

	if _, err := m.ListKeys(context.Background(), "sub", "rg", "ghost"); err == nil {
		t.Error("listkeys on missing hub: want error")
	}

	if _, err := m.GetKeysForKeyName(context.Background(), "sub", "rg", "ghost", "iothubowner"); err == nil {
		t.Error("getKeysForKeyName on missing hub: want error")
	}

	if _, err := m.ListConsumerGroups(context.Background(), "sub", "rg", "ghost"); err == nil {
		t.Error("list cg on missing hub: want error")
	}

	if _, err := m.GetConsumerGroup(context.Background(), "sub", "rg", "hub1", "ghost"); err == nil {
		t.Error("get missing cg: want error")
	}
}

func TestConsumerGroupValidation(t *testing.T) {
	m := newMock()
	cases := []struct{ sub, rg, hub, name string }{
		{"", "rg", "h", "cg"},
		{"sub", "", "h", "cg"},
		{"sub", "rg", "", "cg"},
		{"sub", "rg", "h", ""},
	}

	for _, c := range cases {
		if _, _, err := m.CreateOrUpdateConsumerGroup(context.Background(), c.sub, c.rg, c.hub, c.name); err == nil {
			t.Errorf("case %+v: want validation error", c)
		}
	}
}

func TestConsumerGroupUpdatePreservesEtag(t *testing.T) {
	m := newMock()
	createHub(t, m)

	first, _, _ := m.CreateOrUpdateConsumerGroup(context.Background(), "sub", "rg", "hub1", "cg1")
	second, isNew, _ := m.CreateOrUpdateConsumerGroup(context.Background(), "sub", "rg", "hub1", "cg1")

	if isNew {
		t.Error("second create reported new")
	}

	if first.Etag != second.Etag {
		t.Errorf("etag changed on update: %q -> %q", first.Etag, second.Etag)
	}
}
