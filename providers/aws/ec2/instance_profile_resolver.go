package ec2

import (
	"context"
	"strings"

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
// association stored on the instance. It returns nil when no profile is
// referenced. When a resolver is wired and the profile exists, both the
// canonical ARN and the ID are filled in; otherwise it falls back to the
// supplied ARN so a caller that passed an ARN still reads it back.
func (m *Mock) resolveInstanceProfile(ctx context.Context, cfg *driver.InstanceConfig) *driver.IamInstanceProfile {
	name := cfg.IamInstanceProfileName
	if name == "" {
		name = instanceProfileNameFromARN(cfg.IamInstanceProfileARN)
	}

	if name == "" && cfg.IamInstanceProfileARN == "" {
		return nil
	}

	if m.instanceProfileResolver != nil && name != "" {
		if info, err := m.instanceProfileResolver.GetInstanceProfile(ctx, name); err == nil {
			return &driver.IamInstanceProfile{ARN: info.ARN, ID: info.ID}
		}
	}

	return &driver.IamInstanceProfile{ARN: cfg.IamInstanceProfileARN}
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
