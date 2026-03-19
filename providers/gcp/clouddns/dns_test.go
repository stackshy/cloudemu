package clouddns

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
	opts := config.NewOptions(config.WithClock(clk), config.WithProjectID("test-project"))

	return New(opts)
}

func TestCreateHostedZone(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	tests := []struct {
		name      string
		cfg       driver.ZoneConfig
		wantErr   bool
		errSubstr string
	}{
		{name: "public zone", cfg: driver.ZoneConfig{Name: "example.com", Tags: map[string]string{"env": "test"}}},
		{name: "private zone", cfg: driver.ZoneConfig{Name: "internal.local", Private: true}},
		{name: "empty name", cfg: driver.ZoneConfig{}, wantErr: true, errSubstr: "required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := m.CreateZone(ctx, tt.cfg)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
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

func TestDeleteHostedZone(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	zone, err := m.CreateZone(ctx, driver.ZoneConfig{Name: "example.com"})
	require.NoError(t, err)

	tests := []struct {
		name      string
		id        string
		wantErr   bool
		errSubstr string
	}{
		{name: "success", id: zone.ID},
		{name: "not found", id: "missing", wantErr: true, errSubstr: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.DeleteZone(ctx, tt.id)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
			}
		})
	}
}

func TestListHostedZones(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	zones, err := m.ListZones(ctx)
	require.NoError(t, err)
	assert.Empty(t, zones)

	_, err = m.CreateZone(ctx, driver.ZoneConfig{Name: "a.com"})
	require.NoError(t, err)
	_, err = m.CreateZone(ctx, driver.ZoneConfig{Name: "b.com"})
	require.NoError(t, err)

	zones, err = m.ListZones(ctx)
	require.NoError(t, err)
	assert.Len(t, zones, 2)
}

func TestCreateRecord(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	zone, err := m.CreateZone(ctx, driver.ZoneConfig{Name: "example.com"})
	require.NoError(t, err)

	tests := []struct {
		name      string
		cfg       driver.RecordConfig
		wantErr   bool
		errSubstr string
	}{
		{name: "A record", cfg: driver.RecordConfig{
			ZoneID: zone.ID, Name: "www.example.com", Type: "A", TTL: 300,
			Values: []string{"1.2.3.4"},
		}},
		{name: "CNAME record", cfg: driver.RecordConfig{
			ZoneID: zone.ID, Name: "api.example.com", Type: "CNAME", TTL: 300,
			Values: []string{"www.example.com"},
		}},
		{name: "duplicate", cfg: driver.RecordConfig{
			ZoneID: zone.ID, Name: "www.example.com", Type: "A",
			Values: []string{"5.6.7.8"},
		}, wantErr: true, errSubstr: "already exists"},
		{name: "zone not found", cfg: driver.RecordConfig{
			ZoneID: "missing", Name: "x.com", Type: "A",
		}, wantErr: true, errSubstr: "not found"},
		{name: "empty name", cfg: driver.RecordConfig{
			ZoneID: zone.ID, Type: "A",
		}, wantErr: true, errSubstr: "name"},
		{name: "empty type", cfg: driver.RecordConfig{
			ZoneID: zone.ID, Name: "x.example.com",
		}, wantErr: true, errSubstr: "type"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := m.CreateRecord(ctx, tt.cfg)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
				assert.Equal(t, tt.cfg.Name, info.Name)
				assert.Equal(t, tt.cfg.Type, info.Type)
			}
		})
	}

	t.Run("record count updated", func(t *testing.T) {
		zoneInfo, getErr := m.GetZone(ctx, zone.ID)
		require.NoError(t, getErr)
		assert.Equal(t, 2, zoneInfo.RecordCount) // A + CNAME
	})
}

func TestDeleteRecord(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	zone, err := m.CreateZone(ctx, driver.ZoneConfig{Name: "example.com"})
	require.NoError(t, err)

	_, err = m.CreateRecord(ctx, driver.RecordConfig{
		ZoneID: zone.ID, Name: "www.example.com", Type: "A", TTL: 300,
		Values: []string{"1.2.3.4"},
	})
	require.NoError(t, err)

	tests := []struct {
		name       string
		zoneID     string
		recName    string
		recType    string
		wantErr    bool
		errSubstr  string
	}{
		{name: "success", zoneID: zone.ID, recName: "www.example.com", recType: "A"},
		{name: "zone not found", zoneID: "missing", recName: "www.example.com", recType: "A", wantErr: true, errSubstr: "not found"},
		{name: "record not found", zoneID: zone.ID, recName: "missing.example.com", recType: "A", wantErr: true, errSubstr: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.DeleteRecord(ctx, tt.zoneID, tt.recName, tt.recType)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
			}
		})
	}
}

func TestListResourceRecordSets(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	zone, err := m.CreateZone(ctx, driver.ZoneConfig{Name: "example.com"})
	require.NoError(t, err)

	_, err = m.CreateRecord(ctx, driver.RecordConfig{
		ZoneID: zone.ID, Name: "www.example.com", Type: "A", TTL: 300,
		Values: []string{"1.2.3.4"},
	})
	require.NoError(t, err)
	_, err = m.CreateRecord(ctx, driver.RecordConfig{
		ZoneID: zone.ID, Name: "api.example.com", Type: "CNAME", TTL: 300,
		Values: []string{"www.example.com"},
	})
	require.NoError(t, err)

	t.Run("list all records in zone", func(t *testing.T) {
		records, listErr := m.ListRecords(ctx, zone.ID)
		require.NoError(t, listErr)
		assert.Len(t, records, 2)
	})

	t.Run("zone not found", func(t *testing.T) {
		_, listErr := m.ListRecords(ctx, "missing")
		require.Error(t, listErr)
		assert.Contains(t, listErr.Error(), "not found")
	})
}

func TestGetRecord(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	zone, err := m.CreateZone(ctx, driver.ZoneConfig{Name: "example.com"})
	require.NoError(t, err)

	_, err = m.CreateRecord(ctx, driver.RecordConfig{
		ZoneID: zone.ID, Name: "www.example.com", Type: "A", TTL: 300,
		Values: []string{"1.2.3.4"},
	})
	require.NoError(t, err)

	tests := []struct {
		name      string
		zoneID    string
		recName   string
		recType   string
		wantErr   bool
		errSubstr string
	}{
		{name: "success", zoneID: zone.ID, recName: "www.example.com", recType: "A"},
		{name: "zone not found", zoneID: "missing", recName: "www.example.com", recType: "A", wantErr: true, errSubstr: "not found"},
		{name: "record not found", zoneID: zone.ID, recName: "missing.com", recType: "A", wantErr: true, errSubstr: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec, err := m.GetRecord(ctx, tt.zoneID, tt.recName, tt.recType)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
				assert.Equal(t, "www.example.com", rec.Name)
				assert.Equal(t, []string{"1.2.3.4"}, rec.Values)
			}
		})
	}
}

func TestUpdateRecord(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	zone, err := m.CreateZone(ctx, driver.ZoneConfig{Name: "example.com"})
	require.NoError(t, err)

	_, err = m.CreateRecord(ctx, driver.RecordConfig{
		ZoneID: zone.ID, Name: "www.example.com", Type: "A", TTL: 300,
		Values: []string{"1.2.3.4"},
	})
	require.NoError(t, err)

	tests := []struct {
		name      string
		cfg       driver.RecordConfig
		wantErr   bool
		errSubstr string
	}{
		{name: "update values", cfg: driver.RecordConfig{
			ZoneID: zone.ID, Name: "www.example.com", Type: "A", TTL: 600,
			Values: []string{"5.6.7.8", "9.10.11.12"},
		}},
		{name: "zone not found", cfg: driver.RecordConfig{
			ZoneID: "missing", Name: "www.example.com", Type: "A",
		}, wantErr: true, errSubstr: "not found"},
		{name: "record not found", cfg: driver.RecordConfig{
			ZoneID: zone.ID, Name: "missing.com", Type: "A",
		}, wantErr: true, errSubstr: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec, err := m.UpdateRecord(ctx, tt.cfg)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
				assert.Equal(t, 600, rec.TTL)
				assert.Equal(t, []string{"5.6.7.8", "9.10.11.12"}, rec.Values)
			}
		})
	}
}

func TestWeightedRecords(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	zone, err := m.CreateZone(ctx, driver.ZoneConfig{Name: "example.com"})
	require.NoError(t, err)

	w70 := 70
	w30 := 30

	_, err = m.CreateRecord(ctx, driver.RecordConfig{
		ZoneID: zone.ID, Name: "www.example.com", Type: "A", TTL: 300,
		Values: []string{"1.2.3.4"}, Weight: &w70, SetID: "set-a",
	})
	require.NoError(t, err)

	_, err = m.CreateRecord(ctx, driver.RecordConfig{
		ZoneID: zone.ID, Name: "www.example.com", Type: "A", TTL: 300,
		Values: []string{"5.6.7.8"}, Weight: &w30, SetID: "set-b",
	})
	require.NoError(t, err)

	records, err := m.ListRecords(ctx, zone.ID)
	require.NoError(t, err)
	assert.Len(t, records, 2)

	rec, err := m.GetRecord(ctx, zone.ID, "www.example.com", "A")
	require.NoError(t, err)
	assert.NotNil(t, rec.Weight)
}

func TestDeleteZoneCleansUpRecords(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	zone, err := m.CreateZone(ctx, driver.ZoneConfig{Name: "example.com"})
	require.NoError(t, err)

	_, err = m.CreateRecord(ctx, driver.RecordConfig{
		ZoneID: zone.ID, Name: "www.example.com", Type: "A", Values: []string{"1.2.3.4"},
	})
	require.NoError(t, err)

	require.NoError(t, m.DeleteZone(ctx, zone.ID))

	// Records should also be deleted - zone is gone so GetRecord should fail
	_, err = m.GetRecord(ctx, zone.ID, "www.example.com", "A")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}
