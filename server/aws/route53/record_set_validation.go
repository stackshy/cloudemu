package route53

import (
	"errors"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	dnsdriver "github.com/stackshy/cloudemu/v2/services/dns/driver"
)

// recordTypes is the RRType enum real Route 53 accepts.
//
//nolint:gochecknoglobals // read-only lookup table.
var recordTypes = map[string]bool{
	"SOA": true, "A": true, "TXT": true, "NS": true, "CNAME": true, "MX": true,
	"NAPTR": true, "PTR": true, "SRV": true, "SPF": true, "AAAA": true, "CAA": true,
	"DS": true, "TLSA": true, "SSHFP": true, "SVCB": true, "HTTPS": true,
}

// exactlyOneOfMsg is the InvalidInput text real Route 53 returns when a record
// set has both or neither of an alias and a TTL with values.
const exactlyOneOfMsg = "Invalid request: Expected exactly one of [AliasTarget, all of [TTL, and ResourceRecords], " +
	"or TrafficPolicyInstanceId], but found %s in Change with [Name=%s, Type=%s]"

// validateRecordType rejects a type outside the RRType enum. Case is ignored,
// the same way rrSetKey ignores it.
func validateRecordType(rtype string) error {
	if recordTypes[strings.ToUpper(rtype)] {
		return nil
	}

	return cerrors.Newf(cerrors.InvalidArgument,
		"1 validation error detected: Value '%s' at 'resourceRecordSet.type' failed to satisfy constraint: "+
			"Member must satisfy enum value set: [SOA, A, TXT, NS, CNAME, MX, NAPTR, PTR, SRV, SPF, AAAA, CAA, DS, "+
			"TLSA, SSHFP, SVCB, HTTPS]", rtype)
}

// validateRecordShape checks that a record set is either an alias or a plain
// record with a TTL and at least one value. Real Route 53 rejects any other mix
// as InvalidInput.
func validateRecordShape(rr *resourceRecordSetXML) error {
	hasPlain := rr.TTL != nil || len(rr.ResourceRecords) > 0

	if rr.AliasTarget != nil && hasPlain {
		return cerrors.Newf(cerrors.InvalidArgument, exactlyOneOfMsg, "more than one", rr.Name, rr.Type)
	}

	if rr.AliasTarget == nil && (rr.TTL == nil || len(rr.ResourceRecords) == 0) {
		return cerrors.Newf(cerrors.InvalidArgument, exactlyOneOfMsg, "none", rr.Name, rr.Type)
	}

	return nil
}

// validateRecordValues checks the values of a plain record set. An A value
// must be an IPv4 address, an AAAA value an IPv6 address, and a CNAME holds
// exactly one value. Real Route 53 rejects these as InvalidChangeBatch.
func validateRecordValues(rr *resourceRecordSetXML) error {
	if rr.AliasTarget != nil {
		return nil
	}

	switch strings.ToUpper(rr.Type) {
	case "A":
		return checkAddresses(rr, true)
	case "AAAA":
		return checkAddresses(rr, false)
	case "CNAME":
		if len(rr.ResourceRecords) != 1 {
			return cerrors.Newf(cerrors.FailedPrecondition,
				"RRSet of type CNAME with DNS name %s must have exactly one value", ensureTrailingDot(rr.Name))
		}
	}

	return nil
}

// checkAddresses reports the first value that is not an address of the wanted
// family.
func checkAddresses(rr *resourceRecordSetXML, v4 bool) error {
	values := make([]string, len(rr.ResourceRecords))
	for i, v := range rr.ResourceRecords {
		values[i] = v.Value
	}

	var ae *dnsdriver.InvalidAddressError
	if !errors.As(dnsdriver.ValidateAddresses(rr.Type, values), &ae) {
		return nil
	}

	if v4 {
		return cerrors.Newf(cerrors.FailedPrecondition,
			"[Invalid Resource Record: 'FATAL problem: ARRDATAIllegalIPv4Address "+
				"(Value is not a valid IPv4 address) encountered with '%s'']", ae.Value)
	}

	return cerrors.Newf(cerrors.FailedPrecondition,
		"[Invalid Resource Record: 'FATAL problem: AAAARRDATAIllegalIPv6Address "+
			"(Value is not a valid IPv6 address) encountered with '%s'']", ae.Value)
}
