package route53

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/dns/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

func TestUpdateZoneAppliesTags(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	z, err := m.CreateZone(ctx, driver.ZoneConfig{
		Name: "example.com",
		Tags: map[string]string{"env": "test"},
	})
	requireNoError(t, err)

	updated, err := m.UpdateZone(ctx, driver.ZoneConfig{
		Name: "example.com",
		Tags: map[string]string{"env": "prod", "team": "platform"},
	})
	requireNoError(t, err)
	assertEqual(t, "prod", updated.Tags["env"])
	assertEqual(t, "platform", updated.Tags["team"])

	got, err := m.GetZone(ctx, z.ID)
	requireNoError(t, err)
	assertEqual(t, "prod", got.Tags["env"])
	assertEqual(t, "platform", got.Tags["team"])

	zones, err := m.ListZones(ctx, scope.Scope{})
	requireNoError(t, err)
	assertEqual(t, 1, len(zones))
	assertEqual(t, "prod", zones[0].Tags["env"])
}

func TestUpdateZoneNotFound(t *testing.T) {
	m := newTestMock()

	_, err := m.UpdateZone(context.Background(), driver.ZoneConfig{Name: "missing.com"})
	assertError(t, err, true)
}
