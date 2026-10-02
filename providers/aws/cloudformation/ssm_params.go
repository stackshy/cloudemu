package cloudformation

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// ParameterReader reads a Parameter Store parameter by name and returns its
// value and type.
type ParameterReader func(ctx context.Context, name string) (value, paramType string, err error)

// secureStringType is the Parameter Store type the SSM parameter types cannot
// read.
const secureStringType = "SecureString"

// SetParameterReader installs the Parameter Store reader that
// AWS::SSM::Parameter::Value<T> parameters use. The provider factory wires it
// to the emulated SSM service.
func (m *Mock) SetParameterReader(r ParameterReader) {
	m.readParameter = r
}

// resolveSSMParameter returns the value of the named Parameter Store
// parameter, the way CloudFormation resolves an SSM parameter type.
func (m *Mock) resolveSSMParameter(ctx context.Context, name string) (string, error) {
	if m.readParameter == nil {
		return "", fetchErr(name)
	}

	value, typ, err := m.readParameter(ctx, name)
	if err != nil {
		return "", fetchErr(name)
	}

	if typ == secureStringType {
		return "", cerrors.Newf(cerrors.InvalidArgument,
			"Parameters [%s] referenced by template have types not supported by CloudFormation.", name)
	}

	return value, nil
}

func fetchErr(name string) error {
	return cerrors.Newf(cerrors.InvalidArgument,
		"Unable to fetch parameters [%s] from parameter store for this account.", name)
}
