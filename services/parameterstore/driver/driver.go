// Package driver defines the interface for SSM Parameter Store service implementations.
package driver

import "context"

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
