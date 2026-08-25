package ec2

import (
	"context"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/compute/driver"
	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
)

// InstanceProfileResolver is the slice of the IAM mock EC2 needs to resolve an
// IamInstanceProfile reference (Arn or Name) supplied to RunInstances into the
// profile's canonical ARN and ID. Real EC2 echoes both on the launched instance
// and on DescribeInstances, so the reference has to be resolved, not stored raw.
type InstanceProfileResolver interface {
	GetInstanceProfile(ctx context.Context, name string) (*iamdriver.InstanceProfileInfo, error)
}

// SetInstanceProfileResolver wires the IAM mock in. Without it an instance
// launched with an IamInstanceProfile still records the supplied ARN, but its
// profile ID is left empty (nothing to resolve it against).
func (m *Mock) SetInstanceProfileResolver(r InstanceProfileResolver) {
	m.instanceProfileResolver = r
}

// resolveInstanceProfile turns the IamInstanceProfile reference on cfg into the
// association stored on the instance. It returns nil, nil when no profile is
// referenced. When a resolver is wired and the profile exists, both the
// canonical ARN and the ID are filled in; otherwise it falls back to the
// supplied ARN so a caller that passed an ARN still reads it back.
//
// A Name or ARN that does not resolve to any created instance profile is
// rejected synchronously, matching real EC2's RunInstances behavior: it
// answers InvalidParameterValue rather than launching the instance with a
// dangling/empty profile association.
func (m *Mock) resolveInstanceProfile(ctx context.Context, cfg *driver.InstanceConfig) (*driver.IamInstanceProfile, error) {
	name := cfg.IamInstanceProfileName
	if name == "" {
		name = instanceProfileNameFromARN(cfg.IamInstanceProfileARN)
	}

	if name == "" && cfg.IamInstanceProfileARN == "" {
		return nil, nil //nolint:nilnil // no profile referenced is not an error
	}

	if m.instanceProfileResolver == nil || name == "" {
		return &driver.IamInstanceProfile{ARN: cfg.IamInstanceProfileARN}, nil
	}

	info, err := m.instanceProfileResolver.GetInstanceProfile(ctx, name)
	if err != nil {
		if cerrors.IsNotFound(err) {
			return nil, invalidInstanceProfileError(cfg)
		}

		return nil, err
	}

	return &driver.IamInstanceProfile{ARN: info.ARN, ID: info.ID}, nil
}

// invalidInstanceProfileError reproduces the exact wording real EC2 returns
// for RunInstances when IamInstanceProfile.Name/.Arn does not resolve to an
// existing instance profile, e.g.:
//
//	Value (my-profile) for parameter iamInstanceProfile.name is invalid.
//	Invalid IAM Instance Profile name
func invalidInstanceProfileError(cfg *driver.InstanceConfig) error {
	if cfg.IamInstanceProfileName != "" {
		return cerrors.Newf(cerrors.InvalidArgument,
			"Value (%s) for parameter iamInstanceProfile.name is invalid. Invalid IAM Instance Profile name",
			cfg.IamInstanceProfileName)
	}

	return cerrors.Newf(cerrors.InvalidArgument,
		"Value (%s) for parameter iamInstanceProfile.arn is invalid. Invalid IAM Instance Profile ARN",
		cfg.IamInstanceProfileARN)
}

// instanceProfileNameFromARN extracts the profile name from an instance-profile
// ARN (arn:aws:iam::<acct>:instance-profile/<path>/<name>), returning the last
// path segment. It returns "" for a non-ARN string.
func instanceProfileNameFromARN(arn string) string {
	const marker = ":instance-profile/"

	idx := strings.Index(arn, marker)
	if idx < 0 {
		return ""
	}

	rest := arn[idx+len(marker):]
	if slash := strings.LastIndex(rest, "/"); slash >= 0 {
		return rest[slash+1:]
	}

	return rest
}
