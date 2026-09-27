// Package driver defines the AWS-native Systems Manager families that have no
// portable counterpart: Run Command and Documents. Parameter Store stays in
// services/parameterstore/driver because Azure App Configuration and GCP
// Secret Manager share its shape.
//
// Each family is an optional capability the SSM wire handler discovers by type
// assertion on the configured Parameter Store driver.
package driver

import (
	"context"
	"time"
)

// CommandInvocation is the result of a Run Command execution on one instance.
type CommandInvocation struct {
	CommandID    string
	InstanceID   string
	DocumentName string
	Status       string
	ResponseCode int32
	Stdout       string
	Stderr       string
}

// CommandTarget identifies managed nodes by a Key/Values criterion, e.g.
// {Key: "tag:Name", Values: ["web"]}. It mirrors the SSM Target shape and is an
// alternative to listing InstanceIDs explicitly.
type CommandTarget struct {
	Key    string
	Values []string
}

// CommandConfig describes a Run Command send. Either InstanceIDs or Targets
// (or both) must be supplied; Targets select managed nodes by tag/attribute.
type CommandConfig struct {
	InstanceIDs  []string
	Targets      []CommandTarget
	DocumentName string
	Comment      string
	Parameters   map[string][]string
}

// RunCommand is an OPTIONAL capability, discovered by type assertion.
//
// Targets are validated: sending to an instance that does not exist is
// InvalidInstanceId, as it is against the real service.
//
// IMPORTANT: an emulated instance has no guest operating system, so nothing
// executes. Invocations report success and empty output. This exercises a
// caller's send/poll orchestration (that it waits for a terminal status, reads
// the response code, and handles failure) but it does NOT validate the script
// itself. A caller whose bootstrap script is wrong will still see success here.
type RunCommand interface {
	SendCommand(ctx context.Context, cfg CommandConfig) (string, error)
	GetCommandInvocation(ctx context.Context, commandID, instanceID string) (*CommandInvocation, error)
}

// Document formats, statuses, owners and hash types, as the SSM API spells them.
const (
	FormatJSON = "JSON"
	FormatYAML = "YAML"
	FormatText = "TEXT"

	StatusActive = "Active"

	OwnerAmazon = "Amazon"

	HashTypeSha256 = "Sha256"

	DocumentTypeCommand    = "Command"
	DocumentTypeAutomation = "Automation"
	DocumentTypeSession    = "Session"
	DocumentTypePolicy     = "Policy"
	DocumentTypePackage    = "Package"

	PermissionTypeShare = "Share"
)

// DocumentRef selects one version of a document. An empty Version and
// VersionName select the default version. Version may be a number, $LATEST or
// $DEFAULT. When both are set they must name the same version.
type DocumentRef struct {
	Name        string
	Version     string
	VersionName string
}

// DocumentParameter is one input parameter declared in a document's content.
type DocumentParameter struct {
	Name         string
	Type         string
	Description  string
	DefaultValue string
}

// DocumentDescription describes one version of a document together with the
// document-wide default and latest versions.
type DocumentDescription struct {
	Name            string
	DisplayName     string
	VersionName     string
	Owner           string
	CreatedDate     time.Time
	Status          string
	DocumentVersion string
	Description     string
	Parameters      []DocumentParameter
	PlatformTypes   []string
	DocumentType    string
	SchemaVersion   string
	LatestVersion   string
	DefaultVersion  string
	DocumentFormat  string
	TargetType      string
	Hash            string
	HashType        string
	Tags            map[string]string
}

// DocumentContent is GetDocument's result: one version's content in the
// requested format.
type DocumentContent struct {
	Name            string
	DisplayName     string
	VersionName     string
	DocumentVersion string
	CreatedDate     time.Time
	Status          string
	Content         string
	DocumentType    string
	DocumentFormat  string
}

// DocumentVersionInfo is one entry of ListDocumentVersions.
type DocumentVersionInfo struct {
	Name             string
	DisplayName      string
	DocumentVersion  string
	VersionName      string
	CreatedDate      time.Time
	IsDefaultVersion bool
	DocumentFormat   string
	Status           string
}

// DefaultVersionResult is UpdateDocumentDefaultVersion's result.
type DefaultVersionResult struct {
	Name               string
	DefaultVersion     string
	DefaultVersionName string
}

// CreateDocumentInput is a CreateDocument request. An empty DocumentType is
// Command and an empty DocumentFormat is JSON.
type CreateDocumentInput struct {
	Name           string
	Content        string
	DisplayName    string
	VersionName    string
	DocumentType   string
	DocumentFormat string
	TargetType     string
	Tags           map[string]string
}

// UpdateDocumentInput is an UpdateDocument request. Version must name the
// latest version (or be $LATEST). Empty DisplayName and TargetType keep the
// latest version's values.
type UpdateDocumentInput struct {
	Name           string
	Content        string
	DisplayName    string
	VersionName    string
	Version        string
	DocumentFormat string
	TargetType     string
}

// DocumentFilter is one ListDocuments filter. Values of one filter are OR'd
// and filters are AND'd. Keys are Name (prefix), Owner, PlatformTypes,
// DocumentType, TargetType, SearchKeyword and tag:<key>.
type DocumentFilter struct {
	Key    string
	Values []string
}

// AccountSharingInfo is one account a document is shared with.
type AccountSharingInfo struct {
	AccountID             string
	SharedDocumentVersion string
}

// ModifyPermissionInput is a ModifyDocumentPermission request.
type ModifyPermissionInput struct {
	Name                  string
	PermissionType        string
	AccountIDsToAdd       []string
	AccountIDsToRemove    []string
	SharedDocumentVersion string
}

// Documents is an OPTIONAL capability, discovered by type assertion. It covers
// customer documents with their versions, sharing and tags, plus the read-only
// catalog of AWS-owned documents (AWS-RunShellScript and others).
type Documents interface {
	CreateDocument(ctx context.Context, in *CreateDocumentInput) (*DocumentDescription, error)
	GetDocument(ctx context.Context, ref DocumentRef, format string) (*DocumentContent, error)
	DescribeDocument(ctx context.Context, ref DocumentRef) (*DocumentDescription, error)
	UpdateDocument(ctx context.Context, in *UpdateDocumentInput) (*DocumentDescription, error)
	// DeleteDocument removes every version, or only the selected one when ref
	// names a version.
	DeleteDocument(ctx context.Context, ref DocumentRef) error
	// ListDocuments returns the default version of every matching document,
	// sorted by name.
	ListDocuments(ctx context.Context, filters []DocumentFilter) ([]DocumentDescription, error)
	ListDocumentVersions(ctx context.Context, name string) ([]DocumentVersionInfo, error)
	UpdateDocumentDefaultVersion(ctx context.Context, name, version string) (*DefaultVersionResult, error)
	DescribeDocumentPermission(ctx context.Context, name, permissionType string) ([]AccountSharingInfo, error)
	ModifyDocumentPermission(ctx context.Context, in *ModifyPermissionInput) error
	TagDocument(ctx context.Context, name string, tags map[string]string) error
	UntagDocument(ctx context.Context, name string, keys []string) error
	ListDocumentTags(ctx context.Context, name string) (map[string]string, error)
}
