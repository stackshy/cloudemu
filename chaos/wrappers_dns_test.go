package chaos_test

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu"
	"github.com/stackshy/cloudemu/chaos"
	"github.com/stackshy/cloudemu/config"
	dnsdriver "github.com/stackshy/cloudemu/dns/driver"
)

func newChaosDNS(t *testing.T) (dnsdriver.DNS, *chaos.Engine) {
	t.Helper()

	e := chaos.New(config.RealClock{})
	t.Cleanup(e.Stop)

	return chaos.WrapDNS(cloudemu.NewAWS().Route53, e), e
}

func TestWrapDNSCreateZoneChaos(t *testing.T) {
	d, e := newChaosDNS(t)
	ctx := context.Background()

	if _, err := d.CreateZone(ctx, dnsdriver.ZoneConfig{Name: "ok.test."}); err != nil {
		t.Fatalf("baseline: %v", err)
	}

	e.Apply(chaos.ServiceOutage("dns", time.Hour))

	if _, err := d.CreateZone(ctx, dnsdriver.ZoneConfig{Name: "fail.test."}); err == nil {
		t.Error("expected chaos error on CreateZone")
	}
}

func TestWrapDNSDeleteZoneChaos(t *testing.T) {
	d, e := newChaosDNS(t)
	ctx := context.Background()
	z, _ := d.CreateZone(ctx, dnsdriver.ZoneConfig{Name: "del.test."})

	e.Apply(chaos.ServiceOutage("dns", time.Hour))

	if err := d.DeleteZone(ctx, z.ID); err == nil {
		t.Error("expected chaos error on DeleteZone")
	}
}

func TestWrapDNSGetZoneChaos(t *testing.T) {
	d, e := newChaosDNS(t)
	ctx := context.Background()
	z, _ := d.CreateZone(ctx, dnsdriver.ZoneConfig{Name: "g.test."})

	e.Apply(chaos.ServiceOutage("dns", time.Hour))

	if _, err := d.GetZone(ctx, z.ID); err == nil {
		t.Error("expected chaos error on GetZone")
	}
}

func TestWrapDNSListZonesChaos(t *testing.T) {
	d, e := newChaosDNS(t)
	ctx := context.Background()

	e.Apply(chaos.ServiceOutage("dns", time.Hour))

	if _, err := d.ListZones(ctx); err == nil {
		t.Error("expected chaos error on ListZones")
	}
}

func TestWrapDNSCreateRecordChaos(t *testing.T) {
	d, e := newChaosDNS(t)
	ctx := context.Background()
	z, _ := d.CreateZone(ctx, dnsdriver.ZoneConfig{Name: "r.test."})

	e.Apply(chaos.ServiceOutage("dns", time.Hour))

	cfg := dnsdriver.RecordConfig{ZoneID: z.ID, Name: "x.r.test.", Type: "A", TTL: 300, Values: []string{"1.2.3.4"}}
	if _, err := d.CreateRecord(ctx, cfg); err == nil {
		t.Error("expected chaos error on CreateRecord")
	}
}

func TestWrapDNSDeleteRecordChaos(t *testing.T) {
	d, e := newChaosDNS(t)
	ctx := context.Background()
	z, _ := d.CreateZone(ctx, dnsdriver.ZoneConfig{Name: "dr.test."})
	_, _ = d.CreateRecord(ctx, dnsdriver.RecordConfig{ZoneID: z.ID, Name: "x.dr.test.", Type: "A", TTL: 300, Values: []string{"1.2.3.4"}})

	e.Apply(chaos.ServiceOutage("dns", time.Hour))

	if err := d.DeleteRecord(ctx, z.ID, "x.dr.test.", "A"); err == nil {
		t.Error("expected chaos error on DeleteRecord")
	}
}

func TestWrapDNSGetRecordChaos(t *testing.T) {
	d, e := newChaosDNS(t)
	ctx := context.Background()
	z, _ := d.CreateZone(ctx, dnsdriver.ZoneConfig{Name: "gr.test."})
	_, _ = d.CreateRecord(ctx, dnsdriver.RecordConfig{ZoneID: z.ID, Name: "x.gr.test.", Type: "A", TTL: 300, Values: []string{"1.2.3.4"}})

	e.Apply(chaos.ServiceOutage("dns", time.Hour))

	if _, err := d.GetRecord(ctx, z.ID, "x.gr.test.", "A"); err == nil {
		t.Error("expected chaos error on GetRecord")
	}
}

func TestWrapDNSListRecordsChaos(t *testing.T) {
	d, e := newChaosDNS(t)
	ctx := context.Background()
	z, _ := d.CreateZone(ctx, dnsdriver.ZoneConfig{Name: "lr.test."})

	e.Apply(chaos.ServiceOutage("dns", time.Hour))

	if _, err := d.ListRecords(ctx, z.ID); err == nil {
		t.Error("expected chaos error on ListRecords")
	}
}

func TestWrapDNSUpdateRecordChaos(t *testing.T) {
	d, e := newChaosDNS(t)
	ctx := context.Background()
	z, _ := d.CreateZone(ctx, dnsdriver.ZoneConfig{Name: "ur.test."})
	_, _ = d.CreateRecord(ctx, dnsdriver.RecordConfig{ZoneID: z.ID, Name: "x.ur.test.", Type: "A", TTL: 300, Values: []string{"1.2.3.4"}})

	e.Apply(chaos.ServiceOutage("dns", time.Hour))

	cfg := dnsdriver.RecordConfig{ZoneID: z.ID, Name: "x.ur.test.", Type: "A", TTL: 600, Values: []string{"5.6.7.8"}}
	if _, err := d.UpdateRecord(ctx, cfg); err == nil {
		t.Error("expected chaos error on UpdateRecord")
	}
}
