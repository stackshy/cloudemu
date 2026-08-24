// Package driver defines the interface for SSM Parameter Store service implementations.
package driver

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
)

// ErrVersionNotFound is returned by GetParameter when the parameter itself
// exists but the requested version or label does not — distinct from the
// parameter being absent. It carries the NotFound code so generic handling
// still treats it as not-found, while the SDK-compat layer can match it with
// errors.Is to return AWS's distinct ParameterVersionNotFound error instead of
// ParameterNotFound.
var ErrVersionNotFound = errors.New(errors.NotFound, "requested parameter version or label not found")

// ErrTypeMismatch is returned by PutParameter when an Overwrite=true update
// specifies a Type that differs from the parameter's existing type. Real
// Parameter Store rejects this with HierarchyTypeMismatchException — you can't
// change a parameter from, e.g., String to SecureString. It carries the
// InvalidArgument code so generic handling still treats it as a bad request,
// while the SDK-compat layer matches it with errors.Is to return the distinct
// HierarchyTypeMismatchException wire error.
var ErrTypeMismatch = errors.New(errors.InvalidArgument,
	"Parameter Store doesn't support changing a parameter type in a hierarchy. "+
		"For example, you can't change a parameter from a String type to a SecureString type. "+
		"You must create a new, unique parameter.")

// Parameter types, matching AWS SSM Parameter Store.
const (
	// TypeString is a plain single-value string parameter.
	TypeString = "String"
	// TypeStringList is a comma-separated list parameter.
	TypeStringList = "StringList"
	// TypeSecureString is an (nominally) encrypted parameter. cloudemu stores
	// the value as-is: there is no real KMS integration.
	TypeSecureString = "SecureString"
)

// PutConfig describes a PutParameter request.
type PutConfig struct {
	Name        string
	Value       string
	Type        string
	Description string
	Overwrite   bool
	Tier        string
	DataType    string
}

// Parameter is a single version of a stored parameter.
type Parameter struct {
	Name         string
	Type         string
	Value        string
	Version      int64
	ARN          string
	DataType     string
	LastModified string
	// Selector records how this value was addressed (e.g. ":3" for a version
	// or ":prod" for a label), for the SDK's Selector response field.
	Selector string
	// Labels, Description, Tier, and LastModifiedUser are populated by
	// GetParameterHistory so labelled/tiered versions round-trip. They are left
	// empty by the value-read paths (GetParameter et al.), matching real SSM.
	Labels           []string
	Description      string
	Tier             string
	LastModifiedUser string
}

// ParameterMetadata describes a parameter without its value.
type ParameterMetadata struct {
	Name             string
	Type             string
	Description      string
	Version          int64
	ARN              string
	Tier             string
	DataType         string
	LastModified     string
	LastModifiedUser string
}

// GetByPathInput describes a GetParametersByPath request.
type GetByPathInput struct {
	Path           string
	Recursive      bool
	WithDecryption bool
}

// ParameterStore is the interface SSM Parameter Store provider implementations
// must satisfy.
type ParameterStore interface {
	PutParameter(ctx context.Context, cfg PutConfig) (version int64, tier string, err error)
	GetParameter(ctx context.Context, name string, withDecryption bool) (*Parameter, error)
	GetParameters(ctx context.Context, names []string, withDecryption bool) (found []Parameter, invalid []string, err error)
	GetParametersByPath(ctx context.Context, in GetByPathInput) ([]Parameter, error)
	DeleteParameter(ctx context.Context, name string) error
	DeleteParameters(ctx context.Context, names []string) (deleted, invalid []string, err error)
	DescribeParameters(ctx context.Context) ([]ParameterMetadata, error)

	// GetParameterHistory returns every version of a parameter, oldest first.
	GetParameterHistory(ctx context.Context, name string) ([]Parameter, error)
	// LabelParameterVersion attaches labels to a specific version (0 = latest).
	LabelParameterVersion(ctx context.Context, name string, version int64, labels []string) (appliedVersion int64, invalid []string, err error)
}

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

// CommandConfig describes a Run Command send.
type CommandConfig struct {
	InstanceIDs  []string
	DocumentName string
	Comment      string
	Parameters   map[string][]string
}

// RunCommand is an OPTIONAL capability, discovered by type assertion.
//
// Targets are validated — sending to an instance that does not exist is
// InvalidInstanceId, as it is against the real service.
//
// IMPORTANT: an emulated instance has no guest operating system, so nothing
// executes. Invocations report success and empty output. This exercises a
// caller's send/poll orchestration — that it waits for a terminal status, reads
// the response code, and handles failure — but it does NOT validate the script
// itself. A caller whose bootstrap script is wrong will still see success here.
type RunCommand interface {
	SendCommand(ctx context.Context, cfg CommandConfig) (string, error)
	GetCommandInvocation(ctx context.Context, commandID, instanceID string) (*CommandInvocation, error)
}
