package networkfirewall

import (
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// Limits from the AWS Network Firewall API reference.
const (
	maxNameLen        = 128
	maxDescriptionLen = 512
	maxTags           = 200
	maxARNLen         = 256
	arnPrefix         = "arn:aws"
	vpcIDPrefix       = "vpc-"
)

func invalid(format string, args ...any) error {
	return cerrors.Newf(cerrors.InvalidArgument, format, args...)
}

// validateName checks a resource name: 1-128 characters of [a-zA-Z0-9-].
func validateName(what, name string) error {
	if name == "" {
		return invalid("%s is required", what)
	}

	if len(name) > maxNameLen {
		return invalid("%s %q exceeds %d characters", what, name, maxNameLen)
	}

	for _, r := range name {
		if !isNameRune(r) {
			return invalid("%s %q must match ^[a-zA-Z0-9-]+$", what, name)
		}
	}

	return nil
}

func isNameRune(r rune) bool {
	return r == '-' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

func validateDescription(d string) error {
	if len(d) > maxDescriptionLen {
		return invalid("Description exceeds %d characters", maxDescriptionLen)
	}

	return nil
}

func validateTags(tags map[string]string) error {
	if len(tags) > maxTags {
		return invalid("at most %d tags are allowed, got %d", maxTags, len(tags))
	}

	return nil
}

// validateARN rejects an ARN that is empty, too long or not an AWS ARN, before
// any store lookup (a malformed ARN is a request error, not "not found").
func validateARN(what, arn string) error {
	if arn == "" {
		return invalid("%s is required", what)
	}

	if len(arn) > maxARNLen || !strings.HasPrefix(arn, arnPrefix) {
		return invalid("%s %q is not a valid ARN", what, arn)
	}

	return nil
}

// validateVPCID checks a VPC id against ^vpc-[0-9a-f]+$ (max 128 characters).
func validateVPCID(id string) error {
	hex, ok := strings.CutPrefix(id, vpcIDPrefix)
	if !ok || hex == "" || len(id) > maxNameLen {
		return invalid("VpcId %q must match ^vpc-[0-9a-f]+$", id)
	}

	for _, r := range hex {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return invalid("VpcId %q must match ^vpc-[0-9a-f]+$", id)
		}
	}

	return nil
}

// requireIdentifier checks that a name-or-ARN lookup carries one of them and
// that a supplied ARN is well formed.
func requireIdentifier(what, name, arn string) error {
	if name == "" && arn == "" {
		return invalid("either %sName or %sArn is required", what, what)
	}

	if name == "" {
		return validateARN(what+"Arn", arn)
	}

	return nil
}

// oneOf reports whether v is one of allowed (SDK enum values, copied verbatim).
func oneOf(v string, allowed ...string) bool {
	for _, a := range allowed {
		if v == a {
			return true
		}
	}

	return false
}
