// Package driver defines the interface for DNS service implementations.
package driver

import "context"

// ZoneConfig describes a DNS zone to create.
type ZoneConfig struct {
	Name    string
	Private bool
	Tags    map[string]string
}

// ZoneInfo describes a DNS zone.
type ZoneInfo struct {
	ID          string
	Name        string
	Private     bool
	RecordCount int
	Tags        map[string]string
}

// RecordConfig describes a DNS record.
type RecordConfig struct {
	ZoneID string
	Name   string
	Type   string // "A", "AAAA", "CNAME", "MX", "TXT", "NS", "SOA", "SRV"
	TTL    int
	Values []string
	Weight *int // for weighted routing, nil means not weighted
	SetID  string
}

// RecordInfo describes a DNS record.
type RecordInfo struct {
	ZoneID string
	Name   string
	Type   string
	TTL    int
	Values []string
	Weight *int
	SetID  string
}

// HealthCheckConfig describes a health check to create.
type HealthCheckConfig struct {
	Endpoint         string // IP or domain
	Port             int
	Protocol         string // "HTTP", "HTTPS", "TCP"
	Path             string // for HTTP/HTTPS
	IntervalSeconds  int
	FailureThreshold int
	Tags             map[string]string
}

// HealthCheckInfo describes a health check.
type HealthCheckInfo struct {
	ID               string
	Endpoint         string
	Port             int
	Protocol         string
	Path             string
	IntervalSeconds  int
	FailureThreshold int
	Status           string // "HEALTHY", "UNHEALTHY"
	Tags             map[string]string
}

// DNS is the interface that DNS provider implementations must satisfy.
type DNS interface {
	CreateZone(ctx context.Context, config ZoneConfig) (*ZoneInfo, error)
	DeleteZone(ctx context.Context, id string) error
	GetZone(ctx context.Context, id string) (*ZoneInfo, error)
	ListZones(ctx context.Context) ([]ZoneInfo, error)

	CreateRecord(ctx context.Context, config RecordConfig) (*RecordInfo, error)
	DeleteRecord(ctx context.Context, zoneID, name, recordType string) error
	GetRecord(ctx context.Context, zoneID, name, recordType string) (*RecordInfo, error)
	ListRecords(ctx context.Context, zoneID string) ([]RecordInfo, error)
	UpdateRecord(ctx context.Context, config RecordConfig) (*RecordInfo, error)

	// Health Checks
	CreateHealthCheck(ctx context.Context, config HealthCheckConfig) (*HealthCheckInfo, error)
	DeleteHealthCheck(ctx context.Context, id string) error
	GetHealthCheck(ctx context.Context, id string) (*HealthCheckInfo, error)
	ListHealthChecks(ctx context.Context) ([]HealthCheckInfo, error)
	UpdateHealthCheck(ctx context.Context, id string, config HealthCheckConfig) (*HealthCheckInfo, error)
	SetHealthCheckStatus(ctx context.Context, id, status string) error
}
