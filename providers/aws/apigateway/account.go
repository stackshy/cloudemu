package apigateway

import (
	"context"
	"regexp"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/apigateway/driver"
)

// Account defaults of a fresh region: 10000 requests/second with a burst of
// 5000, usage plans enabled and API key version 4.
const (
	defaultRateLimit     = 10000
	defaultBurstLimit    = 5000
	featureUsagePlans    = "UsagePlans"
	defaultAPIKeyVersion = "4"

	pathCloudWatchRoleARN = "/cloudwatchRoleArn"
	pathFeatures          = "/features"
)

// roleARNPattern is the shape of an IAM role ARN.
var roleARNPattern = regexp.MustCompile(`^arn:aws[a-zA-Z-]*:iam::\d{12}:role/.+$`)

func defaultAccount() driver.Account {
	return driver.Account{
		Throttle:      driver.ThrottleSettings{BurstLimit: defaultBurstLimit, RateLimit: defaultRateLimit},
		Features:      []string{featureUsagePlans},
		APIKeyVersion: defaultAPIKeyVersion,
	}
}

// GetAccount returns the region's account settings.
func (m *Mock) GetAccount(_ context.Context) (*driver.Account, error) {
	m.regionMu.RLock()
	defer m.regionMu.RUnlock()

	out := copyAccount(&m.account)

	return &out, nil
}

// UpdateAccount applies a patch document. Only the CloudWatch role and the
// feature list are writable; throttle limits and the API key version are
// set by the service.
func (m *Mock) UpdateAccount(_ context.Context, ops []driver.PatchOperation) (*driver.Account, error) {
	m.regionMu.Lock()
	defer m.regionMu.Unlock()

	next := copyAccount(&m.account)

	for _, op := range ops {
		if err := applyAccountPatch(&next, op); err != nil {
			return nil, err
		}
	}

	m.account = next
	out := copyAccount(&m.account)

	return &out, nil
}

func applyAccountPatch(acct *driver.Account, op driver.PatchOperation) error {
	switch op.Path {
	case pathCloudWatchRoleARN:
		role := patchRef(op)
		if role != "" && !roleARNPattern.MatchString(role) {
			return cerrors.Newf(cerrors.InvalidArgument, "The role ARN is not well formed: %s", role)
		}

		acct.CloudWatchRoleARN = role
	case pathFeatures:
		if op.Value != featureUsagePlans {
			return cerrors.Newf(cerrors.InvalidArgument, "Invalid feature '%s'. Must be one of: [%s]", op.Value, featureUsagePlans)
		}

		switch op.Op {
		case opAdd:
			acct.Features = patchStringSlice(acct.Features, opAdd, op.Value)
		case opRemove:
			acct.Features = patchStringSlice(acct.Features, opRemove, op.Value)
		default:
			return invalidPatchPath(op, pathCloudWatchRoleARN)
		}
	default:
		return invalidPatchPath(op, pathCloudWatchRoleARN, pathFeatures)
	}

	return nil
}

func copyAccount(a *driver.Account) driver.Account {
	out := *a
	out.Features = append([]string{}, a.Features...)

	return out
}
