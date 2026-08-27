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

// ErrTagsWithOverwrite is returned by PutParameter when Tags are supplied
// together with Overwrite=true. Real Parameter Store rejects that combination —
// tags can only be set when a parameter is first created (AddTagsToResource
// changes tags on an existing one). It carries the InvalidArgument code so the
// SDK-compat layer surfaces it as ValidationException.
var ErrTagsWithOverwrite = errors.New(errors.InvalidArgument,
	"The Tags and Overwrite parameters "+
		"can't be used at the same time.")

// ErrUnsupportedType is returned by PutParameter when Type is set to a value
// outside {String, StringList, SecureString}. Real Parameter Store rejects an
// unrecognized type with UnsupportedParameterType rather than silently coercing
// it. It carries the InvalidArgument code; the SDK-compat layer matches it with
// errors.Is to return the distinct UnsupportedParameterType wire error.
var ErrUnsupportedType = errors.New(errors.InvalidArgument,
	"The parameter type "+
		"isn't supported.")

// ErrInvalidFilterKey is returned by GetParametersByPath when a ParameterFilters
// entry uses a Key the operation doesn't support (only Type, KeyId, and Label
// are valid). It carries InvalidArgument; the SDK-compat layer maps it to the
// distinct InvalidFilterKey wire error.
var ErrInvalidFilterKey = errors.New(errors.InvalidArgument,
	"The specified key "+
		"isn't valid.")

// ErrInvalidFilterOption is returned by GetParametersByPath when a
// ParameterFilters entry uses an Option other than Equals or BeginsWith. It
// carries InvalidArgument; the SDK-compat layer maps it to InvalidFilterOption.
var ErrInvalidFilterOption = errors.New(errors.InvalidArgument,
	"The specified filter option isn't valid. "+
		"Valid options are Equals and BeginsWith.")

// ErrKeyIDOnNonSecure is returned by PutParameter when a KeyId is supplied for a
// String or StringList parameter. Real Parameter Store only honors KeyId for
// SecureString parameters and rejects it otherwise with ValidationException. It
// carries InvalidArgument so the SDK-compat layer surfaces ValidationException.
var ErrKeyIDOnNonSecure = errors.New(errors.InvalidArgument,
	"The parameter type isn't supported for the specified KeyId. "+
		"KeyId is only supported for SecureString parameters.")

// ErrInvalidAllowedPattern is returned by PutParameter when AllowedPattern is
// not a valid regular expression. It carries InvalidArgument so the SDK-compat
// layer surfaces ValidationException.
var ErrInvalidAllowedPattern = errors.New(errors.InvalidArgument,
	"The following parameter values are not valid: AllowedPattern. "+
		"The allowed pattern isn't a valid regular expression.")

// ErrValuePatternMismatch is returned by PutParameter when the Value does not
// match the parameter's AllowedPattern. It carries InvalidArgument so the
// SDK-compat layer surfaces ValidationException.
var ErrValuePatternMismatch = errors.New(errors.InvalidArgument,
	"Parameter value "+
		"doesn't match the allowed pattern.")

// DefaultSecureStringKeyID is the KMS key Parameter Store assigns to a
// SecureString parameter when PutParameter omits KeyId — the AWS-managed
// default key alias.
const DefaultSecureStringKeyID = "alias/aws/ssm"

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
	// KeyID is the KMS key (id or alias) used to encrypt a SecureString value.
	// It is only valid for SecureString parameters; supplying it for a
	// String/StringList is rejected. When omitted for a SecureString it defaults
	// to DefaultSecureStringKeyID (alias/aws/ssm).
	KeyID string
	// AllowedPattern is an optional regular expression the Value must match.
	// A non-empty pattern that is not a valid regexp, or a Value that fails to
	// match it, is rejected.
	AllowedPattern string
	// Tags are applied to the parameter at create time. Real Parameter Store
	// rejects supplying Tags together with Overwrite=true, so Tags are only
	// meaningful on a create.
	Tags map[string]string
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
	// Labels, Description, Tier, LastModifiedUser, KeyID, and AllowedPattern are
	// populated by GetParameterHistory so labeled/tiered versions round-trip.
	// They are left empty by the value-read paths (GetParameter et al.) —
	// matching real SSM, whose Parameter shape has no KeyId or AllowedPattern
	// even though its ParameterHistory entry does.
	Labels           []string
	Description      string
	Tier             string
	LastModifiedUser string
	KeyID            string
	AllowedPattern   string
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
	// KeyID is the KMS key of a SecureString parameter (empty otherwise), and
	// AllowedPattern is the parameter's value-validation regex. Real
	// DescribeParameters reflects both in ParameterMetadata.
	KeyID          string
	AllowedPattern string
}

// ParameterStringFilter is a GetParametersByPath filter: a Key, an Option
// (Equals or BeginsWith; empty means Equals), and one or more Values that are
// OR'd. Multiple filters are AND'd.
type ParameterStringFilter struct {
	Key    string
	Option string
	Values []string
}

// GetByPathInput describes a GetParametersByPath request.
type GetByPathInput struct {
	Path           string
	Recursive      bool
	WithDecryption bool
	// ParameterFilters narrows the result. GetParametersByPath supports the
	// Type, KeyId, and Label keys only; other keys are rejected.
	ParameterFilters []ParameterStringFilter
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
