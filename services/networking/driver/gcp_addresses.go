package driver

import (
	"context"
	"encoding/json"
)

// compute.addresses (regional) and compute.globalAddresses reserve an IP: a
// static external IP, an internal IP in a subnetwork, or a Private Services
// Access / Private Service Connect range. The cross-cloud Networking model has
// no reserved-address resource with GCP's purpose/prefixLength shape, so the
// GCP provider stores it through this OPTIONAL, type-asserted capability,
// alongside its other state so it snapshots and restores with it. AWS and Azure
// do not implement it.

// GCPAddress is one reserved address, addressed by (Project, Scope, Name).
// Scope is "global" for a global address, otherwise the region name. Body is
// the address resource as served (compute#address JSON), including the labels
// and labelFingerprint the provider maintains.
type GCPAddress struct {
	Project string
	Scope   string
	Name    string
	Body    json.RawMessage
}

// GCPAddressStore is the GCP-only reserved-address surface.
type GCPAddressStore interface {
	// InsertGCPAddress stores a new address, returning AlreadyExists when the
	// (project, scope, name) is taken. The provider stamps labelFingerprint
	// from the body's labels, so a freshly inserted address already carries the
	// fingerprint setLabels requires.
	InsertGCPAddress(ctx context.Context, addr GCPAddress) error
	// GetGCPAddress returns the address, or NotFound.
	GetGCPAddress(ctx context.Context, project, scope, name string) (*GCPAddress, error)
	// ListGCPAddresses returns every address of a project in scope, or in every
	// scope when scope is empty.
	ListGCPAddresses(ctx context.Context, project, scope string) ([]GCPAddress, error)
	// DeleteGCPAddress removes the address, or returns NotFound.
	DeleteGCPAddress(ctx context.Context, project, scope, name string) error
	// AllocateGCPAddressIP hands out the next IP of the provider's synthetic
	// reserved range, for an address the caller did not pin to an IP.
	AllocateGCPAddressIP(ctx context.Context) (string, error)
	// SetGCPAddressLabels replaces the address's whole label set (an empty set
	// removes every label) and recomputes labelFingerprint. fingerprint must be
	// the current labelFingerprint: a missing or stale one returns
	// FailedPrecondition with nothing changed. Returns NotFound when absent.
	SetGCPAddressLabels(ctx context.Context, project, scope, name string,
		labels map[string]string, fingerprint string) error
}
