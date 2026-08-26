// Package driver defines the interface for DNS service implementations.
package driver

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/scope"
)

// VPCAssociation identifies an Amazon VPC associated with an AWS Route 53
// private hosted zone. Other providers leave it unused.
type VPCAssociation struct {
	VPCID     string
	VPCRegion string
}

// ZoneConfig describes a DNS zone to create.
type ZoneConfig struct {
	Name    string
	Private bool
	Tags    map[string]string
	// CallerReference is the AWS Route 53 caller-supplied idempotency token,
	// persisted and returned verbatim on Get/List. Other providers leave it
	// empty.
	CallerReference string
	// Comment is the AWS Route 53 hosted-zone comment, persisted and returned on
	// Create/Get. Other providers leave it empty.
	Comment string
	// VPCs are the Amazon VPCs a Route 53 private hosted zone is associated with
	// at create time. A non-empty list marks the zone private. Other providers
	// leave it nil.
	VPCs []VPCAssociation
	// Scope records the cloud-side container the zone was created in (Azure
	// subscription/resource group or GCP project). The zero value is unscoped.
	Scope scope.Scope
}

// ZoneInfo describes a DNS zone.
type ZoneInfo struct {
	ID          string
	Name        string
	Private     bool
	RecordCount int
	Tags        map[string]string
	// CallerReference is the AWS Route 53 caller-supplied idempotency token as
	// stored on create. Other providers leave it empty.
	CallerReference string
	// Comment is the AWS Route 53 hosted-zone comment as stored on create. Other
	// providers leave it empty.
	Comment string
	// VPCs are the Amazon VPCs a Route 53 private hosted zone is currently
	// associated with. Other providers leave it nil.
	VPCs []VPCAssociation
	// Scope is the container the zone lives in; scoped list endpoints filter
	// on it. The zero value is unscoped and visible everywhere.
	Scope scope.Scope
}

// AliasTarget describes an AWS Route 53 alias target (an A/AAAA record that
// points at another AWS resource instead of an IP). Other providers leave it
// nil.
type AliasTarget struct {
	DNSName              string
	HostedZoneID         string
	EvaluateTargetHealth bool
}

// GeoLocation describes an AWS Route 53 geolocation routing constraint. Other
// providers leave it nil.
type GeoLocation struct {
	ContinentCode   string
	CountryCode     string
	SubdivisionCode string
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
	// Routing/alias attributes below are AWS Route 53 specific; other providers
	// leave them zero. They round-trip on a record set the same way Weight/SetID
	// do so weighted/latency/failover/geo/alias records are faithful.
	Failover         string // "PRIMARY" | "SECONDARY", empty when not a failover record
	Region           string // latency-based routing region, empty otherwise
	HealthCheckID    string
	MultiValueAnswer *bool
	GeoLocation      *GeoLocation
	AliasTarget      *AliasTarget
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
	// AWS Route 53 routing/alias attributes; other providers leave them zero.
	Failover         string
	Region           string
	HealthCheckID    string
	MultiValueAnswer *bool
	GeoLocation      *GeoLocation
	AliasTarget      *AliasTarget
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
	ListZones(ctx context.Context, filter scope.Scope) ([]ZoneInfo, error)
	// UpdateZone applies the mutable fields (tags) of an existing zone,
	// mirroring ARM CreateOrUpdate-on-existing. It matches the zone by name.
	UpdateZone(ctx context.Context, config ZoneConfig) (*ZoneInfo, error)

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
