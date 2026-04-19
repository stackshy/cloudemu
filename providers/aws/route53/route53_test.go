package route53

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/dns/driver"
)

func newTestMock() *Mock {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"))
	return New(opts)
}

func createTestZone(m *Mock) *driver.ZoneInfo {
	info, _ := m.CreateZone(context.Background(), driver.ZoneConfig{
		Name: "example.com",
		Tags: map[string]string{"env": "test"},
	})
	return info
}

func TestCreateZone(t *testing.T) {
	tests := []struct {
		name      string
		cfg       driver.ZoneConfig
		expectErr bool
	}{
		{name: "success", cfg: driver.ZoneConfig{Name: "example.com"}},
		{name: "private zone", cfg: driver.ZoneConfig{Name: "internal.com", Private: true}},
		{name: "empty name", cfg: driver.ZoneConfig{}, expectErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMock()
			info, err := m.CreateZone(context.Background(), tc.cfg)
			assertError(t, err, tc.expectErr)

			if tc.expectErr {
				return
			}

			assertNotEmpty(t, info.ID)
			assertEqual(t, tc.cfg.Name, info.Name)
			assertEqual(t, tc.cfg.Private, info.Private)
			assertEqual(t, 0, info.RecordCount)
		})
	}
}

func TestDeleteZone(t *testing.T) {
	m := newTestMock()
	z := createTestZone(m)

	requireNoError(t, m.DeleteZone(context.Background(), z.ID))
	assertError(t, m.DeleteZone(context.Background(), "zone-nope"), true)
}

func TestGetZone(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	z := createTestZone(m)

	t.Run("found", func(t *testing.T) {
		info, err := m.GetZone(ctx, z.ID)
		requireNoError(t, err)
		assertEqual(t, "example.com", info.Name)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := m.GetZone(ctx, "zone-nope")
		assertError(t, err, true)
	})
}

func TestListZones(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	zones, err := m.ListZones(ctx)
	requireNoError(t, err)
	assertEqual(t, 0, len(zones))

	createTestZone(m)

	zones, err = m.ListZones(ctx)
	requireNoError(t, err)
	assertEqual(t, 1, len(zones))
}

func TestCreateRecord(t *testing.T) {
	m := newTestMock()
	z := createTestZone(m)

	tests := []struct {
		name      string
		cfg       driver.RecordConfig
		expectErr bool
	}{
		{
			name: "A record",
			cfg: driver.RecordConfig{
				ZoneID: z.ID, Name: "www.example.com", Type: "A",
				TTL: 300, Values: []string{"1.2.3.4"},
			},
		},
		{
			name: "CNAME record",
			cfg: driver.RecordConfig{
				ZoneID: z.ID, Name: "api.example.com", Type: "CNAME",
				TTL: 300, Values: []string{"www.example.com"},
			},
		},
		{
			name: "zone not found",
			cfg: driver.RecordConfig{
				ZoneID: "zone-nope", Name: "x.com", Type: "A",
			},
			expectErr: true,
		},
		{
			name: "empty name",
			cfg: driver.RecordConfig{
				ZoneID: z.ID, Type: "A",
			},
			expectErr: true,
		},
		{
			name: "empty type",
			cfg: driver.RecordConfig{
				ZoneID: z.ID, Name: "x.com",
			},
			expectErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec, err := m.CreateRecord(context.Background(), tc.cfg)
			assertError(t, err, tc.expectErr)

			if tc.expectErr {
				return
			}

			assertEqual(t, tc.cfg.Name, rec.Name)
			assertEqual(t, tc.cfg.Type, rec.Type)
			assertEqual(t, tc.cfg.TTL, rec.TTL)
		})
	}
}

func TestCreateRecordDuplicate(t *testing.T) {
	m := newTestMock()
	z := createTestZone(m)
	ctx := context.Background()

	cfg := driver.RecordConfig{
		ZoneID: z.ID, Name: "www.example.com", Type: "A",
		TTL: 300, Values: []string{"1.2.3.4"},
	}

	_, err := m.CreateRecord(ctx, cfg)
	requireNoError(t, err)

	_, err = m.CreateRecord(ctx, cfg)
	assertError(t, err, true)
}

func TestDeleteRecord(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	z := createTestZone(m)

	_, _ = m.CreateRecord(ctx, driver.RecordConfig{
		ZoneID: z.ID, Name: "www.example.com", Type: "A",
		TTL: 300, Values: []string{"1.2.3.4"},
	})

	t.Run("success", func(t *testing.T) {
		err := m.DeleteRecord(ctx, z.ID, "www.example.com", "A")
		requireNoError(t, err)
	})

	t.Run("not found", func(t *testing.T) {
		err := m.DeleteRecord(ctx, z.ID, "missing.example.com", "A")
		assertError(t, err, true)
	})

	t.Run("zone not found", func(t *testing.T) {
		err := m.DeleteRecord(ctx, "zone-nope", "x.com", "A")
		assertError(t, err, true)
	})
}

func TestGetRecord(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	z := createTestZone(m)

	_, _ = m.CreateRecord(ctx, driver.RecordConfig{
		ZoneID: z.ID, Name: "www.example.com", Type: "A",
		TTL: 300, Values: []string{"1.2.3.4"},
	})

	t.Run("found", func(t *testing.T) {
		rec, err := m.GetRecord(ctx, z.ID, "www.example.com", "A")
		requireNoError(t, err)
		assertEqual(t, "www.example.com", rec.Name)
		assertEqual(t, 1, len(rec.Values))
	})

	t.Run("not found", func(t *testing.T) {
		_, err := m.GetRecord(ctx, z.ID, "missing.example.com", "A")
		assertError(t, err, true)
	})

	t.Run("zone not found", func(t *testing.T) {
		_, err := m.GetRecord(ctx, "zone-nope", "x.com", "A")
		assertError(t, err, true)
	})
}

func TestListRecords(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	z := createTestZone(m)

	_, _ = m.CreateRecord(ctx, driver.RecordConfig{
		ZoneID: z.ID, Name: "www.example.com", Type: "A", TTL: 300, Values: []string{"1.2.3.4"},
	})
	_, _ = m.CreateRecord(ctx, driver.RecordConfig{
		ZoneID: z.ID, Name: "api.example.com", Type: "CNAME", TTL: 300, Values: []string{"www.example.com"},
	})

	t.Run("success", func(t *testing.T) {
		records, err := m.ListRecords(ctx, z.ID)
		requireNoError(t, err)
		assertEqual(t, 2, len(records))
	})

	t.Run("zone not found", func(t *testing.T) {
		_, err := m.ListRecords(ctx, "zone-nope")
		assertError(t, err, true)
	})
}

func TestUpdateRecord(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	z := createTestZone(m)

	_, _ = m.CreateRecord(ctx, driver.RecordConfig{
		ZoneID: z.ID, Name: "www.example.com", Type: "A",
		TTL: 300, Values: []string{"1.2.3.4"},
	})

	t.Run("success", func(t *testing.T) {
		rec, err := m.UpdateRecord(ctx, driver.RecordConfig{
			ZoneID: z.ID, Name: "www.example.com", Type: "A",
			TTL: 600, Values: []string{"5.6.7.8"},
		})
		requireNoError(t, err)
		assertEqual(t, 600, rec.TTL)
		assertEqual(t, "5.6.7.8", rec.Values[0])
	})

	t.Run("record not found", func(t *testing.T) {
		_, err := m.UpdateRecord(ctx, driver.RecordConfig{
			ZoneID: z.ID, Name: "missing.example.com", Type: "A",
		})
		assertError(t, err, true)
	})

	t.Run("zone not found", func(t *testing.T) {
		_, err := m.UpdateRecord(ctx, driver.RecordConfig{
			ZoneID: "zone-nope", Name: "x.com", Type: "A",
		})
		assertError(t, err, true)
	})
}

func TestRecordCountUpdates(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	z := createTestZone(m)

	_, _ = m.CreateRecord(ctx, driver.RecordConfig{
		ZoneID: z.ID, Name: "a.example.com", Type: "A", TTL: 300, Values: []string{"1.1.1.1"},
	})
	_, _ = m.CreateRecord(ctx, driver.RecordConfig{
		ZoneID: z.ID, Name: "b.example.com", Type: "A", TTL: 300, Values: []string{"2.2.2.2"},
	})

	zone, _ := m.GetZone(ctx, z.ID)
	assertEqual(t, 2, zone.RecordCount)

	_ = m.DeleteRecord(ctx, z.ID, "a.example.com", "A")
	zone, _ = m.GetZone(ctx, z.ID)
	assertEqual(t, 1, zone.RecordCount)
}

func TestWeightedRecords(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	z := createTestZone(m)

	w70 := 70
	w30 := 30

	_, err := m.CreateRecord(ctx, driver.RecordConfig{
		ZoneID: z.ID, Name: "www.example.com", Type: "A",
		TTL: 300, Values: []string{"1.1.1.1"}, Weight: &w70, SetID: "primary",
	})
	requireNoError(t, err)

	_, err = m.CreateRecord(ctx, driver.RecordConfig{
		ZoneID: z.ID, Name: "www.example.com", Type: "A",
		TTL: 300, Values: []string{"2.2.2.2"}, Weight: &w30, SetID: "secondary",
	})
	requireNoError(t, err)

	rec, err := m.GetRecord(ctx, z.ID, "www.example.com", "A")
	requireNoError(t, err)
	assertNotEmpty(t, rec.Name)
}

func TestDeleteZoneCascadesRecords(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	z := createTestZone(m)

	_, _ = m.CreateRecord(ctx, driver.RecordConfig{
		ZoneID: z.ID, Name: "www.example.com", Type: "A", TTL: 300, Values: []string{"1.1.1.1"},
	})

	requireNoError(t, m.DeleteZone(ctx, z.ID))

	// Zone and records should be gone
	_, err := m.GetZone(ctx, z.ID)
	assertError(t, err, true)
}

func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertError(t *testing.T, err error, expectErr bool) {
	t.Helper()
	switch {
	case expectErr && err == nil:
		t.Fatal("expected error but got nil")
	case !expectErr && err != nil:
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertEqual(t *testing.T, expected, actual any) {
	t.Helper()
	if expected != actual {
		t.Errorf("expected %v, got %v", expected, actual)
	}
}

func assertNotEmpty(t *testing.T, s string) {
	t.Helper()
	if s == "" {
		t.Error("expected non-empty string")
	}
}
