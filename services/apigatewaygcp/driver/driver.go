// Package driver defines the portable interface for the Google API Gateway
// control plane (apigateway.googleapis.com). It lives under services/apigatewaygcp
// (not services/apigateway) because that name is already the AWS Amazon API
// Gateway REST-API-v1 service, a wholly different API that shares only a
// marketing name; the two cannot share a portable interface.
//
// It is control-plane only — the three resource collections a Terraform
// google-beta provider or a real google.golang.org/api/apigateway client CRUDs
// are modeled:
//
//	projects/{p}/locations/global/apis/{api}                       (global)
//	projects/{p}/locations/global/apis/{api}/configs/{config}      (nested under an api)
//	projects/{p}/locations/{region}/gateways/{gateway}             (regional)
//
// and the long-running operations their mutating RPCs return, which are
// location-scoped:
//
//	projects/{p}/locations/{loc}/operations/{op}
//
// API Gateway resources exist ONLY in the terraform-provider-google-beta
// provider, whose default base path is apigateway.googleapis.com/v1beta/; a
// real google.golang.org/api/apigateway/v1 client and gcloud use /v1/. The wire
// layer serves both prefixes.
//
// The identity + computed fields (name, createTime, updateTime, state) are
// derived at create and stay stable across reads so a Terraform refresh does not
// drift. An apiConfig also carries a computed serviceConfigId and a gateway a
// computed defaultHostname, each minted once at create; both are stored and
// stable across reads (the classic drift points). Every other caller-supplied
// body key (displayName, labels, managedService, openapiDocuments, grpcServices,
// managedServiceConfigs, gatewayConfig, apiConfig, …) is carried as Fields
// verbatim, matching the certificatemanager/clouddeploy raw-passthrough model, so
// deep loosely-typed sub-blocks (an openapiDocument's base64 contents) round-trip
// byte-for-byte and cannot drift.
//
// IAM policy verbs and any real request routing (the data plane) are out of
// scope (see BUILDOUT_BACKLOG.md).
package driver

import (
	"context"
	"encoding/json"
	"time"
)

// Resource is one API Gateway control-plane resource (an api, an api config, or
// a gateway). Name components are stored separately so the full resource name
// and location scoping can be rebuilt without re-parsing. API names the parent
// api of a config and is empty for an api or a gateway. CreateTime/UpdateTime
// are derived deterministically and stay stable across reads. Fields holds every
// caller-supplied, non-computed body key verbatim — plus any computed body value
// seeded once at create (state, an apiConfig's serviceConfigId, a gateway's
// defaultHostname), which then round-trips as a stable passthrough value.
type Resource struct {
	Project    string
	Location   string
	API        string // parent api id for a config; empty for an api or a gateway
	ID         string
	CreateTime time.Time
	UpdateTime time.Time
	Fields     map[string]json.RawMessage
}

// Config is the input to a create or patch. API carries the parent api id for a
// config create/patch and is empty for an api or a gateway. Fields carries the
// caller-supplied body keys (the computed output keys already stripped by the
// wire layer; state/serviceConfigId/defaultHostname already seeded).
type Config struct {
	Project  string
	Location string
	API      string
	ID       string
	Fields   map[string]json.RawMessage
}

// Operation is a completed long-running operation. Every CloudEmu mutation
// finishes synchronously, so Done is always true; the shared poller (for /v1/
// SDK polls) and the wire handler (for /v1beta/ Terraform polls) both replay it
// so an SDK or Terraform LRO wait terminates on the first poll.
type Operation struct {
	Name       string // projects/{p}/locations/{loc}/operations/{op}
	Done       bool
	TargetName string // the resource the operation acted on
	Type       string // create | update | delete
}

// APIGateway is the control-plane interface a provider implements. Apis and
// gateways are flat, project+location-scoped collections; configs are nested
// under an api, so their verbs carry the parent api id. Deleting an api cascades
// to its configs.
type APIGateway interface {
	CreateAPI(ctx context.Context, cfg *Config) (*Resource, *Operation, error)
	GetAPI(ctx context.Context, project, location, id string) (*Resource, error)
	ListAPIs(ctx context.Context, project, location string) ([]Resource, error)
	PatchAPI(ctx context.Context, cfg *Config, mask []string) (*Resource, *Operation, error)
	DeleteAPI(ctx context.Context, project, location, id string) (*Operation, error)

	CreateAPIConfig(ctx context.Context, cfg *Config) (*Resource, *Operation, error)
	GetAPIConfig(ctx context.Context, project, location, api, id string) (*Resource, error)
	ListAPIConfigs(ctx context.Context, project, location, api string) ([]Resource, error)
	PatchAPIConfig(ctx context.Context, cfg *Config, mask []string) (*Resource, *Operation, error)
	DeleteAPIConfig(ctx context.Context, project, location, api, id string) (*Operation, error)

	CreateGateway(ctx context.Context, cfg *Config) (*Resource, *Operation, error)
	GetGateway(ctx context.Context, project, location, id string) (*Resource, error)
	ListGateways(ctx context.Context, project, location string) ([]Resource, error)
	PatchGateway(ctx context.Context, cfg *Config, mask []string) (*Resource, *Operation, error)
	DeleteGateway(ctx context.Context, project, location, id string) (*Operation, error)

	// GetOperation resolves a (done) long-running operation by name, for the
	// wire handler's /v1beta/ operations poll (and a standalone server's /v1/).
	GetOperation(ctx context.Context, name string) (*Operation, error)
}
