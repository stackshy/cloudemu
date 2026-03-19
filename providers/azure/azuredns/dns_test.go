package azuredns

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/dns/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestMock() *Mock {
	clk := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(clk), config.WithAccountID("test-sub"))

	return New(opts)
}

func createTestZone(t *testing.T, m *Mock) string {
	t.Helper()

	ctx := context.Background()
	zone, err := m.CreateZone(ctx, driver.ZoneConfig{Name: "example.com", Tags: map[string]string{"env": "test"}})
	require.NoError(t, err)

	return zone.ID
}

func TestCreateZone(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	tests := []struct {
		name    string
		cfg     driver.ZoneConfig
		wantErr bool
		errMsg  string
	}{
		{name: "public zone", cfg: driver.ZoneConfig{Name: "example.com", Private: false, Tags: map[string]string{"env": "prod"}}},
		{name: "private zone", cfg: driver.ZoneConfig{Name: "internal.local", Private: true}},
		{name: "empty name", cfg: driver.ZoneConfig{Name: ""}, wantErr: true, errMsg: "zone name is required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := m.CreateZone(ctx, tt.cfg)

			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			default:
				require.NoError(t, err)
				assert.NotEmpty(t, info.ID)
				assert.Equal(t, tt.cfg.Name, info.Name)
				assert.Equal(t, tt.cfg.Private, info.Private)
				assert.Equal(t, 0, info.RecordCount)
			}
		})
	}
}

func TestDeleteZone(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	zoneID := createTestZone(t, m)

	tests := []struct {
		name    string
		id      string
		wantErr bool
		errMsg  string
	}{
		{name: "success", id: zoneID},
		{name: "not found", id: "missing-id", wantErr: true, errMsg: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.DeleteZone(ctx, tt.id)

			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			default:
				require.NoError(t, err)
			}
		})
	}
}

func TestGetZone(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	zoneID := createTestZone(t, m)

	t.Run("success", func(t *testing.T) {
		zone, err := m.GetZone(ctx, zoneID)
		require.NoError(t, err)
		assert.Equal(t, "example.com", zone.Name)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := m.GetZone(ctx, "missing")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestListZones(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	t.Run("empty", func(t *testing.T) {
		zones, err := m.ListZones(ctx)
		require.NoError(t, err)
		assert.Empty(t, zones)
	})

	t.Run("with zones", func(t *testing.T) {
		_, _ = m.CreateZone(ctx, driver.ZoneConfig{Name: "a.com"})
		_, _ = m.CreateZone(ctx, driver.ZoneConfig{Name: "b.com"})

		zones, err := m.ListZones(ctx)
		require.NoError(t, err)
		assert.Len(t, zones, 2)
	})
}

func TestCreateRecord(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	zoneID := createTestZone(t, m)

	tests := []struct {
		name    string
		cfg     driver.RecordConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "A record",
			cfg:  driver.RecordConfig{ZoneID: zoneID, Name: "www", Type: "A", TTL: 300, Values: []string{"1.2.3.4"}},
		},
		{
			name: "CNAME record",
			cfg:  driver.RecordConfig{ZoneID: zoneID, Name: "api", Type: "CNAME", TTL: 600, Values: []string{"api.example.com"}},
		},
		{name: "zone not found", cfg: driver.RecordConfig{ZoneID: "missing", Name: "x", Type: "A"}, wantErr: true, errMsg: "zone"},
		{name: "empty name", cfg: driver.RecordConfig{ZoneID: zoneID, Name: "", Type: "A"}, wantErr: true, errMsg: "record name is required"},
		{name: "empty type", cfg: driver.RecordConfig{ZoneID: zoneID, Name: "x", Type: ""}, wantErr: true, errMsg: "record type is required"},
		{
			name:    "duplicate record",
			cfg:     driver.RecordConfig{ZoneID: zoneID, Name: "www", Type: "A", TTL: 300, Values: []string{"5.6.7.8"}},
			wantErr: true, errMsg: "already exists",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := m.CreateRecord(ctx, tt.cfg)

			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			default:
				require.NoError(t, err)
				assert.Equal(t, tt.cfg.Name, info.Name)
				assert.Equal(t, tt.cfg.Type, info.Type)
				assert.Equal(t, tt.cfg.TTL, info.TTL)
			}
		})
	}
}

func TestCreateRecordUpdatesZoneCount(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	zoneID := createTestZone(t, m)

	_, err := m.CreateRecord(ctx, driver.RecordConfig{ZoneID: zoneID, Name: "www", Type: "A", TTL: 300, Values: []string{"1.2.3.4"}})
	require.NoError(t, err)

	zone, err := m.GetZone(ctx, zoneID)
	require.NoError(t, err)
	assert.Equal(t, 1, zone.RecordCount)
}

func TestDeleteRecord(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	zoneID := createTestZone(t, m)

	_, _ = m.CreateRecord(ctx, driver.RecordConfig{ZoneID: zoneID, Name: "www", Type: "A", TTL: 300, Values: []string{"1.2.3.4"}})

	tests := []struct {
		name       string
		zoneID     string
		recordName string
		recordType string
		wantErr    bool
		errMsg     string
	}{
		{name: "success", zoneID: zoneID, recordName: "www", recordType: "A"},
		{name: "not found", zoneID: zoneID, recordName: "missing", recordType: "A", wantErr: true, errMsg: "not found"},
		{name: "zone not found", zoneID: "missing", recordName: "www", recordType: "A", wantErr: true, errMsg: "zone"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.DeleteRecord(ctx, tt.zoneID, tt.recordName, tt.recordType)

			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			default:
				require.NoError(t, err)
			}
		})
	}
}

func TestGetRecord(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	zoneID := createTestZone(t, m)

	_, _ = m.CreateRecord(ctx, driver.RecordConfig{ZoneID: zoneID, Name: "www", Type: "A", TTL: 300, Values: []string{"1.2.3.4"}})

	t.Run("success", func(t *testing.T) {
		rec, err := m.GetRecord(ctx, zoneID, "www", "A")
		require.NoError(t, err)
		assert.Equal(t, "www", rec.Name)
		assert.Equal(t, "A", rec.Type)
		assert.Equal(t, []string{"1.2.3.4"}, rec.Values)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := m.GetRecord(ctx, zoneID, "missing", "A")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("zone not found", func(t *testing.T) {
		_, err := m.GetRecord(ctx, "missing", "www", "A")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "zone")
	})
}

func TestListRecords(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	zoneID := createTestZone(t, m)

	_, _ = m.CreateRecord(ctx, driver.RecordConfig{ZoneID: zoneID, Name: "www", Type: "A", TTL: 300, Values: []string{"1.2.3.4"}})
	_, _ = m.CreateRecord(ctx, driver.RecordConfig{ZoneID: zoneID, Name: "api", Type: "CNAME", TTL: 600, Values: []string{"api.example.com"}})

	t.Run("success", func(t *testing.T) {
		records, err := m.ListRecords(ctx, zoneID)
		require.NoError(t, err)
		assert.Len(t, records, 2)
	})

	t.Run("zone not found", func(t *testing.T) {
		_, err := m.ListRecords(ctx, "missing")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestUpdateRecord(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	zoneID := createTestZone(t, m)

	_, _ = m.CreateRecord(ctx, driver.RecordConfig{ZoneID: zoneID, Name: "www", Type: "A", TTL: 300, Values: []string{"1.2.3.4"}})

	t.Run("success", func(t *testing.T) {
		rec, err := m.UpdateRecord(ctx, driver.RecordConfig{
			ZoneID: zoneID, Name: "www", Type: "A", TTL: 600, Values: []string{"5.6.7.8"},
		})
		require.NoError(t, err)
		assert.Equal(t, 600, rec.TTL)
		assert.Equal(t, []string{"5.6.7.8"}, rec.Values)
	})

	t.Run("record not found", func(t *testing.T) {
		_, err := m.UpdateRecord(ctx, driver.RecordConfig{
			ZoneID: zoneID, Name: "missing", Type: "A", TTL: 300,
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("zone not found", func(t *testing.T) {
		_, err := m.UpdateRecord(ctx, driver.RecordConfig{
			ZoneID: "missing", Name: "www", Type: "A", TTL: 300,
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "zone")
	})
}

func TestWeightedRecords(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	zoneID := createTestZone(t, m)

	w1 := 70
	w2 := 30

	_, err := m.CreateRecord(ctx, driver.RecordConfig{
		ZoneID: zoneID, Name: "www", Type: "A", TTL: 300,
		Values: []string{"1.1.1.1"}, Weight: &w1, SetID: "primary",
	})
	require.NoError(t, err)

	_, err = m.CreateRecord(ctx, driver.RecordConfig{
		ZoneID: zoneID, Name: "www", Type: "A", TTL: 300,
		Values: []string{"2.2.2.2"}, Weight: &w2, SetID: "secondary",
	})
	require.NoError(t, err)

	t.Run("get weighted record", func(t *testing.T) {
		rec, err := m.GetRecord(ctx, zoneID, "www", "A")
		require.NoError(t, err)
		assert.NotNil(t, rec.Weight)
		assert.NotEmpty(t, rec.SetID)
	})

	t.Run("list shows both", func(t *testing.T) {
		records, err := m.ListRecords(ctx, zoneID)
		require.NoError(t, err)
		assert.Len(t, records, 2)
	})

	t.Run("delete removes all weighted", func(t *testing.T) {
		err := m.DeleteRecord(ctx, zoneID, "www", "A")
		require.NoError(t, err)

		records, err := m.ListRecords(ctx, zoneID)
		require.NoError(t, err)
		assert.Empty(t, records)
	})
}

func TestDeleteZoneCascadesRecords(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	zoneID := createTestZone(t, m)

	_, _ = m.CreateRecord(ctx, driver.RecordConfig{ZoneID: zoneID, Name: "www", Type: "A", TTL: 300, Values: []string{"1.2.3.4"}})

	require.NoError(t, m.DeleteZone(ctx, zoneID))

	// Verify zone is gone
	_, err := m.GetZone(ctx, zoneID)
	require.Error(t, err)
}
