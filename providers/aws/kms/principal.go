package kms

import (
	"regexp"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/kms/driver"
)

const maxPrincipalLen = 256

var (
	// principalPattern is the PrincipalIdType constraint KMS models for
	// GranteePrincipal/RetiringPrincipal (^[\w+=,.@:/-]+$, 1-256 chars).
	principalPattern = regexp.MustCompile(`^[\w+=,.@:/-]+$`)
	// servicePrincipalPattern is the shape of an AWS service principal
	// (e.g. "dynamodb.us-east-1.amazonaws.com"), as KMS models it for
	// GranteeServicePrincipal.
	servicePrincipalPattern = regexp.MustCompile(`^([A-Za-z0-9\-]+)\.([A-Za-z0-9\-]+)(\.[A-Za-z0-9\-]+)+$`)
	// accountIDPattern is a bare 12-digit AWS account ID.
	accountIDPattern = regexp.MustCompile(`^\d{12}$`)
)

// arnFieldCount is the number of ':'-separated fields of an ARN
// (arn:partition:service:region:account:resource).
const arnFieldCount = 6

// validateGrantPrincipal checks a grant principal. KMS documents the value as
// "the ARN of an AWS principal" (accounts, IAM users, roles, federated users,
// assumed-role users); a value that is not a well-formed IAM/STS principal ARN
// is rejected with InvalidArnException. Values outside the modeled
// PrincipalIdType constraint fail with ValidationException. Service principals
// and bare account IDs are accepted, since AWS services and older clients
// create grants with those forms.
//
//nolint:gocyclo // a flat sequence of the documented principal-format checks
func validateGrantPrincipal(field, p string) error {
	if len(p) > maxPrincipalLen || !principalPattern.MatchString(p) {
		return errors.Newf(errors.InvalidArgument,
			"1 validation error detected: Value at '%s' failed to satisfy constraint: "+
				"Member must satisfy regular expression pattern: ^[\\w+=,.@:/-]+$ "+
				"and have length less than or equal to %d", lowerFirst(field), maxPrincipalLen)
	}

	if !strings.HasPrefix(p, "arn:") {
		if servicePrincipalPattern.MatchString(p) || accountIDPattern.MatchString(p) {
			return nil
		}

		return driver.NewAPIError(driver.ExInvalidArn, errors.InvalidArgument,
			"%s %s is not a valid principal ARN", field, p)
	}

	parts := strings.SplitN(p, ":", arnFieldCount)
	if len(parts) != arnFieldCount || parts[1] == "" || parts[4] == "" || parts[5] == "" ||
		(parts[2] != "iam" && parts[2] != "sts") {
		return driver.NewAPIError(driver.ExInvalidArn, errors.InvalidArgument,
			"%s %s does not refer to a valid principal", field, p)
	}

	return nil
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}

	return strings.ToLower(s[:1]) + s[1:]
}
