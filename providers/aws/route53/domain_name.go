package route53

import (
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
)

// invalidDomainNamePrefix starts the message of a rejected zone name. The wire
// layer reads it to answer with the InvalidDomainName code.
const invalidDomainNamePrefix = "InvalidDomainName: "

// Domain name limits from the Route 53 domain name format rules.
const (
	maxDomainNameLen = 255
	maxLabelLen      = 63
)

// validateZoneName checks a hosted zone name the way real Route 53 does.
// Route 53 allows most punctuation, so only spaces, control and non-ASCII
// bytes, empty or long labels, long names and a leading wildcard fail.
func validateZoneName(name string) error {
	trimmed := strings.TrimSuffix(name, ".")
	if len(trimmed) > maxDomainNameLen {
		return invalidDomainName(name)
	}

	labels := strings.Split(trimmed, ".")
	if labels[0] == "*" {
		return invalidDomainName(name)
	}

	for _, label := range labels {
		if label == "" || len(label) > maxLabelLen || !printableASCII(label) {
			return invalidDomainName(name)
		}
	}

	return nil
}

// printableASCII reports whether s has only bytes from '!' to '~'.
func printableASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] <= ' ' || s[i] > '~' {
			return false
		}
	}

	return true
}

func invalidDomainName(name string) error {
	return errors.Newf(errors.InvalidArgument, "%s%s is not a valid domain name", invalidDomainNamePrefix, name)
}
