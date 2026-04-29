package chaos

import (
	"context"

	dnsdriver "github.com/stackshy/cloudemu/dns/driver"
)

// chaosDNS wraps a DNS driver. Hot-path: zone and record CRUD.
// Health checks delegate through.
type chaosDNS struct {
	dnsdriver.DNS
	engine *Engine
}

// WrapDNS returns a DNS driver that consults engine on zone and record calls.
func WrapDNS(inner dnsdriver.DNS, engine *Engine) dnsdriver.DNS {
	return &chaosDNS{DNS: inner, engine: engine}
}

func (c *chaosDNS) CreateZone(ctx context.Context, cfg dnsdriver.ZoneConfig) (*dnsdriver.ZoneInfo, error) {
	if err := applyChaos(ctx, c.engine, "dns", "CreateZone"); err != nil {
		return nil, err
	}

	return c.DNS.CreateZone(ctx, cfg)
}

func (c *chaosDNS) DeleteZone(ctx context.Context, id string) error {
	if err := applyChaos(ctx, c.engine, "dns", "DeleteZone"); err != nil {
		return err
	}

	return c.DNS.DeleteZone(ctx, id)
}

func (c *chaosDNS) GetZone(ctx context.Context, id string) (*dnsdriver.ZoneInfo, error) {
	if err := applyChaos(ctx, c.engine, "dns", "GetZone"); err != nil {
		return nil, err
	}

	return c.DNS.GetZone(ctx, id)
}

func (c *chaosDNS) ListZones(ctx context.Context) ([]dnsdriver.ZoneInfo, error) {
	if err := applyChaos(ctx, c.engine, "dns", "ListZones"); err != nil {
		return nil, err
	}

	return c.DNS.ListZones(ctx)
}

//nolint:gocritic // cfg is a value type by interface contract
func (c *chaosDNS) CreateRecord(ctx context.Context, cfg dnsdriver.RecordConfig) (*dnsdriver.RecordInfo, error) {
	if err := applyChaos(ctx, c.engine, "dns", "CreateRecord"); err != nil {
		return nil, err
	}

	return c.DNS.CreateRecord(ctx, cfg)
}

func (c *chaosDNS) DeleteRecord(ctx context.Context, zoneID, name, recordType string) error {
	if err := applyChaos(ctx, c.engine, "dns", "DeleteRecord"); err != nil {
		return err
	}

	return c.DNS.DeleteRecord(ctx, zoneID, name, recordType)
}

func (c *chaosDNS) GetRecord(ctx context.Context, zoneID, name, recordType string) (*dnsdriver.RecordInfo, error) {
	if err := applyChaos(ctx, c.engine, "dns", "GetRecord"); err != nil {
		return nil, err
	}

	return c.DNS.GetRecord(ctx, zoneID, name, recordType)
}

func (c *chaosDNS) ListRecords(ctx context.Context, zoneID string) ([]dnsdriver.RecordInfo, error) {
	if err := applyChaos(ctx, c.engine, "dns", "ListRecords"); err != nil {
		return nil, err
	}

	return c.DNS.ListRecords(ctx, zoneID)
}

//nolint:gocritic // cfg is a value type by interface contract
func (c *chaosDNS) UpdateRecord(ctx context.Context, cfg dnsdriver.RecordConfig) (*dnsdriver.RecordInfo, error) {
	if err := applyChaos(ctx, c.engine, "dns", "UpdateRecord"); err != nil {
		return nil, err
	}

	return c.DNS.UpdateRecord(ctx, cfg)
}
