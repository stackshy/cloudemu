// Package driver defines the AWS-native Systems Manager families that have no
// portable counterpart: Run Command, managed nodes and Documents. Parameter
// Store stays in services/parameterstore/driver because Azure App
// Configuration and GCP Secret Manager share its shape.
//
// Each family is an optional capability the SSM wire handler discovers by type
// assertion on the configured Parameter Store driver.
package driver

import (
	"context"
	"time"
)

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
