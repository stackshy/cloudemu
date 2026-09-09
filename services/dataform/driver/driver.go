// Package driver defines the portable interface for the Google Cloud Dataform
// control plane (dataform.googleapis.com/v1beta1). Dataform ships a v1beta1 API
// only (there is no /v1/), so both the hashicorp/google-beta provider's
// google_dataform_repository resource (the resource lives only in google-beta)
// and a real google.golang.org/api/dataform/v1beta1 client target the /v1beta1/
// base path.
//
// It is control-plane only. The single region-scoped, client-named resource a
// Terraform google-beta provider or SDK client CRUDs is modeled:
//
//	projects/{p}/locations/{loc}/repositories/{repo}
//
// The workspace/compilationResult/workflowInvocation data plane and the nested
// releaseConfigs/workflowConfigs collections are out of scope (see
// BUILDOUT_BACKLOG.md).
//
// Every Repository mutation is synchronous REST: Create/Get/List/Patch/Delete
// return the resource (or empty) directly, with no google.longrunning.Operation
// wrapper. No operation registry is involved.
//
// The resource name (full deterministic path) and createTime (minted once at
// create) are the computed, output-only fields and stay stable across reads so a
// Terraform refresh never drifts. Every other field is caller-supplied. The rich
// nested blocks gitRemoteSettings and workspaceCompilationOverrides are carried
// as raw JSON and round-trip verbatim.
package driver

import (
	"context"
	"encoding/json"
)

// Repository is one Dataform repository. Name components are stored separately so
// the full resource name and parent scoping rebuild without re-parsing. CreateTime
// is minted once at create and stays stable across reads. GitRemoteSettings and
// WorkspaceCompilationOverrides are raw JSON blocks echoed verbatim.
type Repository struct {
	Project    string
	Location   string
	ID         string
	CreateTime string

	DisplayName                            string
	Labels                                 map[string]string
	ServiceAccount                         string
	KmsKeyName                             string
	NpmrcEnvironmentVariablesSecretVersion string
	GitRemoteSettings                      json.RawMessage
	WorkspaceCompilationOverrides          json.RawMessage
}

// RepositoryConfig is the input to a repository create or patch. ID is the
// client-assigned repository id (from ?repositoryId= or the body name).
type RepositoryConfig struct {
	Project  string
	Location string
	ID       string

	DisplayName                            string
	Labels                                 map[string]string
	ServiceAccount                         string
	KmsKeyName                             string
	NpmrcEnvironmentVariablesSecretVersion string
	GitRemoteSettings                      json.RawMessage
	WorkspaceCompilationOverrides          json.RawMessage
}

// Dataform is the control-plane interface a provider implements. Every method is
// synchronous.
type Dataform interface {
	CreateRepository(ctx context.Context, cfg *RepositoryConfig) (*Repository, error)
	GetRepository(ctx context.Context, project, location, id string) (*Repository, error)
	ListRepositories(ctx context.Context, project, location string) ([]Repository, error)
	PatchRepository(ctx context.Context, cfg *RepositoryConfig, mask []string) (*Repository, error)
	DeleteRepository(ctx context.Context, project, location, id string) error
}
