package driver

import (
	"net"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// Address record types.
const (
	recordTypeA    = "A"
	recordTypeAAAA = "AAAA"
)

// InvalidAddressError reports an A or AAAA value that is not an address of
// the right family. It unwraps to a cerrors.InvalidArgument error, so callers
// that only check the code still see InvalidArgument. Wire handlers use the
// fields to build each cloud's own error.
type InvalidAddressError struct {
	RecordType string // "A" or "AAAA"
	Index      int    // position of the bad value
	Value      string
}

// Error implements the error interface.
func (e *InvalidAddressError) Error() string {
	return e.Unwrap().Error()
}

// Unwrap returns the InvalidArgument error this one stands for.
func (e *InvalidAddressError) Unwrap() error {
	field, family := "ipv4Address", "IPv4"
	if e.RecordType == recordTypeAAAA {
		field, family = "ipv6Address", "IPv6"
	}

	if e.Value == "" {
		return cerrors.Newf(cerrors.InvalidArgument, "The resource record is missing field '%s'.", field)
	}

	return cerrors.Newf(cerrors.InvalidArgument,
		"The value '%s' of field '%s' is not a valid %s address.", e.Value, field, family)
}

// ValidateAddresses checks the values of an A or AAAA record. An A value must
// be IPv4 and an AAAA value must be IPv6. It returns an *InvalidAddressError
// for the first bad value, or nil. Other record types always pass.
func ValidateAddresses(recordType string, values []string) error {
	rtype := strings.ToUpper(recordType)
	if rtype != recordTypeA && rtype != recordTypeAAAA {
		return nil
	}

	for i, v := range values {
		ip := net.ParseIP(v)
		if ip == nil || (ip.To4() != nil) != (rtype == recordTypeA) {
			return &InvalidAddressError{RecordType: rtype, Index: i, Value: v}
		}
	}

	return nil
}
