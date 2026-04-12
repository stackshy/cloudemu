package dns

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/dns/driver"
	"github.com/stackshy/cloudemu/inject"
	"github.com/stackshy/cloudemu/metrics"
	"github.com/stackshy/cloudemu/recorder"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockDriver implements driver.DNS for testing the portable wrapper.
type mockDriver struct {
	zones        map[string]*driver.ZoneInfo
	records      map[string]map[string]*driver.RecordInfo // zoneID -> "name|type" -> record
	healthChecks map[string]*driver.HealthCheckInfo
	seq          int
}

func newMockDriver() *mockDriver {
	return &mockDriver{
		zones:        make(map[string]*driver.ZoneInfo),
		records:      make(map[string]map[string]*driver.RecordInfo),
		healthChecks: make(map[string]*driver.HealthCheckInfo),
	}
}

func (m *mockDriver) nextID(prefix string) string {
	m.seq++

	return fmt.Sprintf("%s-%d", prefix, m.seq)
}

func recordKey(name, recordType string) string {
	return name + "|" + recordType
}

func (m *mockDriver) CreateZone(_ context.Context, config driver.ZoneConfig) (*driver.ZoneInfo, error) {
	if config.Name == "" {
		return nil, fmt.Errorf("name required")
	}

	id := m.nextID("zone")
	info := &driver.ZoneInfo{ID: id, Name: config.Name, Private: config.Private, Tags: config.Tags}
	m.zones[id] = info
	m.records[id] = make(map[string]*driver.RecordInfo)

	return info, nil
}

func (m *mockDriver) DeleteZone(_ context.Context, id string) error {
	if _, ok := m.zones[id]; !ok {
		return fmt.Errorf("not found")
	}

	delete(m.zones, id)
	delete(m.records, id)

	return nil
}

func (m *mockDriver) GetZone(_ context.Context, id string) (*driver.ZoneInfo, error) {
	info, ok := m.zones[id]
	if !ok {
		return nil, fmt.Errorf("not found")
	}

	return info, nil
}

func (m *mockDriver) ListZones(_ context.Context) ([]driver.ZoneInfo, error) {
	result := make([]driver.ZoneInfo, 0, len(m.zones))
	for _, info := range m.zones {
		result = append(result, *info)
	}

	return result, nil
}

func (m *mockDriver) CreateRecord(_ context.Context, config driver.RecordConfig) (*driver.RecordInfo, error) {
	zoneRecords, ok := m.records[config.ZoneID]
	if !ok {
		return nil, fmt.Errorf("zone not found")
	}

	key := recordKey(config.Name, config.Type)

	if _, ok := zoneRecords[key]; ok {
		return nil, fmt.Errorf("record already exists")
	}

	info := &driver.RecordInfo{ZoneID: config.ZoneID, Name: config.Name, Type: config.Type, TTL: config.TTL, Values: config.Values}
	zoneRecords[key] = info

	return info, nil
}

func (m *mockDriver) DeleteRecord(_ context.Context, zoneID, name, recordType string) error {
	zoneRecords, ok := m.records[zoneID]
	if !ok {
		return fmt.Errorf("zone not found")
	}

	key := recordKey(name, recordType)

	if _, ok := zoneRecords[key]; !ok {
		return fmt.Errorf("record not found")
	}

	delete(zoneRecords, key)

	return nil
}

func (m *mockDriver) GetRecord(_ context.Context, zoneID, name, recordType string) (*driver.RecordInfo, error) {
	zoneRecords, ok := m.records[zoneID]
	if !ok {
		return nil, fmt.Errorf("zone not found")
	}

	key := recordKey(name, recordType)

	info, ok := zoneRecords[key]
	if !ok {
		return nil, fmt.Errorf("record not found")
	}

	return info, nil
}

func (m *mockDriver) ListRecords(_ context.Context, zoneID string) ([]driver.RecordInfo, error) {
	zoneRecords, ok := m.records[zoneID]
	if !ok {
		return nil, fmt.Errorf("zone not found")
	}

	result := make([]driver.RecordInfo, 0, len(zoneRecords))
	for _, info := range zoneRecords {
		result = append(result, *info)
	}

	return result, nil
}

func (m *mockDriver) UpdateRecord(_ context.Context, config driver.RecordConfig) (*driver.RecordInfo, error) {
	zoneRecords, ok := m.records[config.ZoneID]
	if !ok {
		return nil, fmt.Errorf("zone not found")
	}

	key := recordKey(config.Name, config.Type)

	info, ok := zoneRecords[key]
	if !ok {
		return nil, fmt.Errorf("record not found")
	}

	info.TTL = config.TTL
	info.Values = config.Values

	return info, nil
}

func (m *mockDriver) CreateHealthCheck(_ context.Context, config driver.HealthCheckConfig) (*driver.HealthCheckInfo, error) {
	if config.Endpoint == "" {
		return nil, fmt.Errorf("endpoint required")
	}

	id := m.nextID("hc")
	info := &driver.HealthCheckInfo{
		ID: id, Endpoint: config.Endpoint, Port: config.Port, Protocol: config.Protocol,
		Path: config.Path, IntervalSeconds: config.IntervalSeconds, FailureThreshold: config.FailureThreshold,
		Status: "HEALTHY", Tags: config.Tags,
	}
	m.healthChecks[id] = info

	return info, nil
}

func (m *mockDriver) DeleteHealthCheck(_ context.Context, id string) error {
	if _, ok := m.healthChecks[id]; !ok {
		return fmt.Errorf("not found")
	}

	delete(m.healthChecks, id)

	return nil
}

func (m *mockDriver) GetHealthCheck(_ context.Context, id string) (*driver.HealthCheckInfo, error) {
	info, ok := m.healthChecks[id]
	if !ok {
		return nil, fmt.Errorf("not found")
	}

	return info, nil
}

func (m *mockDriver) ListHealthChecks(_ context.Context) ([]driver.HealthCheckInfo, error) {
	result := make([]driver.HealthCheckInfo, 0, len(m.healthChecks))
	for _, info := range m.healthChecks {
		result = append(result, *info)
	}

	return result, nil
}

func (m *mockDriver) UpdateHealthCheck(_ context.Context, id string, config driver.HealthCheckConfig) (*driver.HealthCheckInfo, error) {
	info, ok := m.healthChecks[id]
	if !ok {
		return nil, fmt.Errorf("not found")
	}

	if config.Endpoint != "" {
		info.Endpoint = config.Endpoint
	}

	if config.Port != 0 {
		info.Port = config.Port
	}

	return info, nil
}

func (m *mockDriver) SetHealthCheckStatus(_ context.Context, id, status string) error {
	info, ok := m.healthChecks[id]
	if !ok {
		return fmt.Errorf("not found")
	}

	info.Status = status

	return nil
}

func newTestDNS(opts ...Option) *DNS {
	return NewDNS(newMockDriver(), opts...)
}

func setupZone(t *testing.T, d *DNS, name string) string {
	t.Helper()

	ctx := context.Background()

	info, err := d.CreateZone(ctx, driver.ZoneConfig{Name: name})
	require.NoError(t, err)

	return info.ID
}

func TestNewDNS(t *testing.T) {
	d := newTestDNS()
	require.NotNil(t, d)
	require.NotNil(t, d.driver)
}

func TestCreateZone(t *testing.T) {
	d := newTestDNS()
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		info, err := d.CreateZone(ctx, driver.ZoneConfig{Name: "example.com"})
		require.NoError(t, err)
		assert.Equal(t, "example.com", info.Name)
		assert.NotEmpty(t, info.ID)
	})

	t.Run("empty name error", func(t *testing.T) {
		_, err := d.CreateZone(ctx, driver.ZoneConfig{})
		require.Error(t, err)
	})
}

func TestDeleteZone(t *testing.T) {
	d := newTestDNS()
	ctx := context.Background()
	zoneID := setupZone(t, d, "del.example.com")

	t.Run("success", func(t *testing.T) {
		err := d.DeleteZone(ctx, zoneID)
		require.NoError(t, err)
	})

	t.Run("not found", func(t *testing.T) {
		err := d.DeleteZone(ctx, "nonexistent")
		require.Error(t, err)
	})
}

func TestGetZone(t *testing.T) {
	d := newTestDNS()
	ctx := context.Background()
	zoneID := setupZone(t, d, "get.example.com")

	t.Run("success", func(t *testing.T) {
		info, err := d.GetZone(ctx, zoneID)
		require.NoError(t, err)
		assert.Equal(t, "get.example.com", info.Name)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := d.GetZone(ctx, "nonexistent")
		require.Error(t, err)
	})
}

func TestListZones(t *testing.T) {
	d := newTestDNS()
	ctx := context.Background()

	zones, err := d.ListZones(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, len(zones))

	setupZone(t, d, "a.example.com")
	setupZone(t, d, "b.example.com")

	zones, err = d.ListZones(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, len(zones))
}

func TestCreateRecord(t *testing.T) {
	d := newTestDNS()
	ctx := context.Background()
	zoneID := setupZone(t, d, "rec.example.com")

	t.Run("success", func(t *testing.T) {
		info, err := d.CreateRecord(ctx, driver.RecordConfig{
			ZoneID: zoneID, Name: "www", Type: "A", TTL: 300, Values: []string{"1.2.3.4"},
		})
		require.NoError(t, err)
		assert.Equal(t, "www", info.Name)
		assert.Equal(t, "A", info.Type)
	})

	t.Run("zone not found", func(t *testing.T) {
		_, err := d.CreateRecord(ctx, driver.RecordConfig{ZoneID: "nonexistent", Name: "www", Type: "A"})
		require.Error(t, err)
	})
}

func TestDeleteRecord(t *testing.T) {
	d := newTestDNS()
	ctx := context.Background()
	zoneID := setupZone(t, d, "delrec.example.com")

	_, err := d.CreateRecord(ctx, driver.RecordConfig{ZoneID: zoneID, Name: "www", Type: "A", TTL: 300, Values: []string{"1.2.3.4"}})
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		err := d.DeleteRecord(ctx, zoneID, "www", "A")
		require.NoError(t, err)
	})

	t.Run("not found", func(t *testing.T) {
		err := d.DeleteRecord(ctx, zoneID, "nonexistent", "A")
		require.Error(t, err)
	})
}

func TestGetRecord(t *testing.T) {
	d := newTestDNS()
	ctx := context.Background()
	zoneID := setupZone(t, d, "getrec.example.com")

	_, err := d.CreateRecord(ctx, driver.RecordConfig{ZoneID: zoneID, Name: "www", Type: "A", TTL: 300, Values: []string{"1.2.3.4"}})
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		info, err := d.GetRecord(ctx, zoneID, "www", "A")
		require.NoError(t, err)
		assert.Equal(t, "www", info.Name)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := d.GetRecord(ctx, zoneID, "nonexistent", "A")
		require.Error(t, err)
	})
}

func TestListRecords(t *testing.T) {
	d := newTestDNS()
	ctx := context.Background()
	zoneID := setupZone(t, d, "listrec.example.com")

	_, err := d.CreateRecord(ctx, driver.RecordConfig{ZoneID: zoneID, Name: "www", Type: "A", TTL: 300, Values: []string{"1.2.3.4"}})
	require.NoError(t, err)

	_, err = d.CreateRecord(ctx, driver.RecordConfig{ZoneID: zoneID, Name: "mail", Type: "MX", TTL: 300, Values: []string{"10 mail.example.com"}})
	require.NoError(t, err)

	records, err := d.ListRecords(ctx, zoneID)
	require.NoError(t, err)
	assert.Equal(t, 2, len(records))
}

func TestUpdateRecord(t *testing.T) {
	d := newTestDNS()
	ctx := context.Background()
	zoneID := setupZone(t, d, "updrec.example.com")

	_, err := d.CreateRecord(ctx, driver.RecordConfig{ZoneID: zoneID, Name: "www", Type: "A", TTL: 300, Values: []string{"1.2.3.4"}})
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		info, err := d.UpdateRecord(ctx, driver.RecordConfig{ZoneID: zoneID, Name: "www", Type: "A", TTL: 600, Values: []string{"5.6.7.8"}})
		require.NoError(t, err)
		assert.Equal(t, 600, info.TTL)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := d.UpdateRecord(ctx, driver.RecordConfig{ZoneID: zoneID, Name: "nonexistent", Type: "A"})
		require.Error(t, err)
	})
}

func TestDNSWithRecorder(t *testing.T) {
	rec := recorder.New()
	d := newTestDNS(WithRecorder(rec))
	ctx := context.Background()

	_, err := d.CreateZone(ctx, driver.ZoneConfig{Name: "rec.example.com"})
	require.NoError(t, err)

	totalCalls := rec.CallCount()
	assert.GreaterOrEqual(t, totalCalls, 1)

	createCalls := rec.CallCountFor("dns", "CreateZone")
	assert.Equal(t, 1, createCalls)
}

func TestDNSWithRecorderOnError(t *testing.T) {
	rec := recorder.New()
	d := newTestDNS(WithRecorder(rec))
	ctx := context.Background()

	_, _ = d.GetZone(ctx, "nonexistent")

	totalCalls := rec.CallCount()
	assert.Equal(t, 1, totalCalls)

	last := rec.LastCall()
	require.NotNil(t, last)
	assert.NotNil(t, last.Error)
}

func TestDNSWithMetrics(t *testing.T) {
	mc := metrics.NewCollector()
	d := newTestDNS(WithMetrics(mc))
	ctx := context.Background()

	_, err := d.CreateZone(ctx, driver.ZoneConfig{Name: "met.example.com"})
	require.NoError(t, err)

	_, err = d.GetZone(ctx, "zone-1")
	// May or may not error; we care about metrics being collected.

	q := metrics.NewQuery(mc)

	callsCount := q.ByName("calls_total").Count()
	assert.GreaterOrEqual(t, callsCount, 2)

	durCount := q.ByName("call_duration").Count()
	assert.GreaterOrEqual(t, durCount, 2)
}

func TestDNSWithMetricsOnError(t *testing.T) {
	mc := metrics.NewCollector()
	d := newTestDNS(WithMetrics(mc))
	ctx := context.Background()

	_, _ = d.GetZone(ctx, "nonexistent")

	q := metrics.NewQuery(mc)

	errCount := q.ByName("errors_total").Count()
	assert.Equal(t, 1, errCount)
}

func TestDNSWithErrorInjection(t *testing.T) {
	inj := inject.NewInjector()
	d := newTestDNS(WithErrorInjection(inj))
	ctx := context.Background()

	injectedErr := fmt.Errorf("injected failure")
	inj.Set("dns", "CreateZone", injectedErr, inject.Always{})

	_, err := d.CreateZone(ctx, driver.ZoneConfig{Name: "fail.example.com"})
	require.Error(t, err)
	assert.Equal(t, injectedErr, err)
}

func TestDNSWithErrorInjectionRecorded(t *testing.T) {
	rec := recorder.New()
	inj := inject.NewInjector()
	d := newTestDNS(WithErrorInjection(inj), WithRecorder(rec))
	ctx := context.Background()

	injectedErr := fmt.Errorf("boom")
	inj.Set("dns", "GetZone", injectedErr, inject.Always{})

	_, err := d.CreateZone(ctx, driver.ZoneConfig{Name: "inj.example.com"})
	require.NoError(t, err)

	_, err = d.GetZone(ctx, "zone-1")
	require.Error(t, err)

	getCalls := rec.CallsFor("dns", "GetZone")
	assert.Equal(t, 1, len(getCalls))
	assert.NotNil(t, getCalls[0].Error)
}

func TestDNSWithErrorInjectionRemoved(t *testing.T) {
	inj := inject.NewInjector()
	d := newTestDNS(WithErrorInjection(inj))
	ctx := context.Background()

	injectedErr := fmt.Errorf("fail")
	inj.Set("dns", "CreateZone", injectedErr, inject.Always{})

	_, err := d.CreateZone(ctx, driver.ZoneConfig{Name: "test.example.com"})
	require.Error(t, err)

	inj.Remove("dns", "CreateZone")

	_, err = d.CreateZone(ctx, driver.ZoneConfig{Name: "test.example.com"})
	require.NoError(t, err)
}

func TestDNSWithLatency(t *testing.T) {
	latency := 1 * time.Millisecond
	d := newTestDNS(WithLatency(latency))
	ctx := context.Background()

	info, err := d.CreateZone(ctx, driver.ZoneConfig{Name: "lat.example.com"})
	require.NoError(t, err)
	assert.Equal(t, "lat.example.com", info.Name)
}

func TestDNSAllOptionsComposed(t *testing.T) {
	rec := recorder.New()
	mc := metrics.NewCollector()
	inj := inject.NewInjector()
	latency := 1 * time.Millisecond

	d := NewDNS(newMockDriver(),
		WithRecorder(rec),
		WithMetrics(mc),
		WithErrorInjection(inj),
		WithLatency(latency),
	)
	ctx := context.Background()

	_, err := d.CreateZone(ctx, driver.ZoneConfig{Name: "all.example.com"})
	require.NoError(t, err)

	_, err = d.ListZones(ctx)
	require.NoError(t, err)

	assert.Equal(t, 2, rec.CallCount())

	q := metrics.NewQuery(mc)
	assert.Equal(t, 2, q.ByName("calls_total").Count())
}

func TestCreateHealthCheck(t *testing.T) {
	d := newTestDNS()
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		hc, err := d.CreateHealthCheck(ctx, driver.HealthCheckConfig{
			Endpoint: "10.0.0.1", Port: 80, Protocol: "HTTP",
		})
		require.NoError(t, err)
		assert.NotEmpty(t, hc.ID)
		assert.Equal(t, "10.0.0.1", hc.Endpoint)
		assert.Equal(t, "HEALTHY", hc.Status)
	})

	t.Run("empty endpoint error", func(t *testing.T) {
		_, err := d.CreateHealthCheck(ctx, driver.HealthCheckConfig{})
		require.Error(t, err)
	})
}

func TestDeleteHealthCheck(t *testing.T) {
	d := newTestDNS()
	ctx := context.Background()

	hc, err := d.CreateHealthCheck(ctx, driver.HealthCheckConfig{Endpoint: "10.0.0.1"})
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		err := d.DeleteHealthCheck(ctx, hc.ID)
		require.NoError(t, err)
	})

	t.Run("not found", func(t *testing.T) {
		err := d.DeleteHealthCheck(ctx, "nonexistent")
		require.Error(t, err)
	})
}

func TestGetHealthCheck(t *testing.T) {
	d := newTestDNS()
	ctx := context.Background()

	hc, err := d.CreateHealthCheck(ctx, driver.HealthCheckConfig{Endpoint: "10.0.0.1"})
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		got, err := d.GetHealthCheck(ctx, hc.ID)
		require.NoError(t, err)
		assert.Equal(t, "10.0.0.1", got.Endpoint)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := d.GetHealthCheck(ctx, "nonexistent")
		require.Error(t, err)
	})
}

func TestListHealthChecks(t *testing.T) {
	d := newTestDNS()
	ctx := context.Background()

	checks, err := d.ListHealthChecks(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, len(checks))

	_, err = d.CreateHealthCheck(ctx, driver.HealthCheckConfig{Endpoint: "10.0.0.1"})
	require.NoError(t, err)

	_, err = d.CreateHealthCheck(ctx, driver.HealthCheckConfig{Endpoint: "10.0.0.2"})
	require.NoError(t, err)

	checks, err = d.ListHealthChecks(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, len(checks))
}

func TestUpdateHealthCheck(t *testing.T) {
	d := newTestDNS()
	ctx := context.Background()

	hc, err := d.CreateHealthCheck(ctx, driver.HealthCheckConfig{Endpoint: "10.0.0.1", Port: 80})
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		updated, err := d.UpdateHealthCheck(ctx, hc.ID, driver.HealthCheckConfig{Endpoint: "10.0.0.2", Port: 443})
		require.NoError(t, err)
		assert.Equal(t, "10.0.0.2", updated.Endpoint)
		assert.Equal(t, 443, updated.Port)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := d.UpdateHealthCheck(ctx, "nonexistent", driver.HealthCheckConfig{Endpoint: "10.0.0.3"})
		require.Error(t, err)
	})
}

func TestSetHealthCheckStatus(t *testing.T) {
	d := newTestDNS()
	ctx := context.Background()

	hc, err := d.CreateHealthCheck(ctx, driver.HealthCheckConfig{Endpoint: "10.0.0.1"})
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		err := d.SetHealthCheckStatus(ctx, hc.ID, "UNHEALTHY")
		require.NoError(t, err)
	})

	t.Run("not found", func(t *testing.T) {
		err := d.SetHealthCheckStatus(ctx, "nonexistent", "HEALTHY")
		require.Error(t, err)
	})
}
