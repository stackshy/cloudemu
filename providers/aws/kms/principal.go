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
// "the ARN of an AWS principal": an account (iam ...:root), an IAM user or
// role, a federated user, or an assumed-role user. A value that is not one of
// those ARN forms is rejected with InvalidArnException. Values outside the
// modeled PrincipalIdType constraint fail with ValidationException. Service
// principals and bare account IDs are accepted, since AWS services and older
// clients create grants with those forms.
func validateGrantPrincipal(field, p string) error {
	if err := validatePrincipalConstraint(field, p); err != nil {
		return err
	}

	if !strings.HasPrefix(p, "arn:") {
		if servicePrincipalPattern.MatchString(p) || accountIDPattern.MatchString(p) {
			return nil
		}

		return driver.NewAPIError(driver.ExInvalidArn, errors.InvalidArgument,
			"%s %s is not a valid principal ARN", field, p)
	}

	if !isPrincipalARN(p) {
		return driver.NewAPIError(driver.ExInvalidArn, errors.InvalidArgument,
			"%s %s does not refer to a valid principal", field, p)
	}

	return nil
}

// validatePrincipalConstraint enforces the modeled PrincipalIdType length and
// pattern, with the message AWS returns for a failed constraint.
func validatePrincipalConstraint(field, p string) error {
	const prefix = "1 validation error detected: Value '%s' at '%s' failed to satisfy constraint: "

	if len(p) > maxPrincipalLen {
		return errors.Newf(errors.InvalidArgument,
			prefix+"Member must have length less than or equal to %d", p, lowerFirst(field), maxPrincipalLen)
	}

	if !principalPattern.MatchString(p) {
		return errors.Newf(errors.InvalidArgument,
			prefix+"Member must satisfy regular expression pattern: ^[\\w+=,.@:/-]+$", p, lowerFirst(field))
	}

	return nil
}

// isPrincipalARN reports whether arn names a principal a grant can be given
// to: arn:<partition>:iam::<account>:root|user/...|role/... or
// arn:<partition>:sts::<account>:assumed-role/...|federated-user/.... IAM
// and STS principal ARNs are global, so the Region field must be empty.
func isPrincipalARN(arn string) bool {
	parts := strings.SplitN(arn, ":", arnFieldCount)
	if len(parts) != arnFieldCount || parts[1] == "" || parts[3] != "" || !accountIDPattern.MatchString(parts[4]) {
		return false
	}

	resource := parts[5]

	switch parts[2] {
	case "iam":
		return resource == "root" || hasNamedPrefix(resource, "user/") || hasNamedPrefix(resource, "role/")
	case "sts":
		return hasNamedPrefix(resource, "assumed-role/") || hasNamedPrefix(resource, "federated-user/")
	default:
		return false
	}
}

// hasNamedPrefix reports whether s is prefix followed by a non-empty name.
func hasNamedPrefix(s, prefix string) bool {
	return strings.HasPrefix(s, prefix) && len(s) > len(prefix)
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}

	return strings.ToLower(s[:1]) + s[1:]
}
