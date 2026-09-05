package ec2

import (
	"encoding/xml"
	"net/http"
	"sort"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
)

// Shared EC2 Describe filter names, reused across the VPC-family handlers.
const (
	tagFilterPrefix     = "tag:"
	filterTagKey        = "tag-key"
	filterVPCID         = "vpc-id"
	filterSubnetID      = "subnet-id"
	filterCIDR          = "cidr"
	filterCIDRBlock     = "cidr-block"
	filterState         = "state"
	filterAssocSubnetID = "association.subnet-id"
	filterDHCPOptionsID = "dhcp-options-id"
)

// filterXML keeps the items that satisfy every filter and projects them to
// their XML form. The VPC-family Describe handlers share it so each is just
// "parse ids -> driver Describe -> validate filters -> filterXML -> write".
func filterXML[T any, X any](
	items []T,
	filters []awsquery.Filter,
	match func(*T, []awsquery.Filter) bool,
	toXML func(*T) X,
) []X {
	out := make([]X, 0, len(items))

	for i := range items {
		if !match(&items[i], filters) {
			continue
		}

		out = append(out, toXML(&items[i]))
	}

	return out
}

// pageNetworkingXML stable-sorts the already-filtered items by their id, then
// applies the request's MaxResults/NextToken paging, returning the page and the
// NextToken to echo (empty on the last page). The VPC-family Describe handlers
// share it so every resource paginates identically. The driver/memstore iterate
// in map order, so sorting first is what makes the base64 cursor stable across
// calls (matching how groupReservations sorts before paging DescribeInstances).
func pageNetworkingXML[X any](items []X, r *http.Request, idOf func(X) string) (page []X, next string) {
	sort.Slice(items, func(i, j int) bool { return idOf(items[i]) < idOf(items[j]) })

	return paginateXML(items, r.Form.Get("MaxResults"), r.Form.Get("NextToken"), idOf)
}

// validateNetworkingFilters rejects any filter name the matcher does not model,
// mirroring how real EC2 answers an unmodeled filter with InvalidParameterValue
// rather than silently matching nothing (which would tell a data-source lookup a
// resource is absent). The matcher reports (matched, known); only known is
// consulted here — probing a zero value — so the accepted set can never drift
// from what the matcher actually honors.
func validateNetworkingFilters[T any](
	filters []awsquery.Filter,
	match func(*T, awsquery.Filter) (matched, known bool),
) error {
	var zero T

	for _, f := range filters {
		if _, known := match(&zero, f); !known {
			return newInvalidParameterErr("The filter '" + f.Name + "' is invalid")
		}
	}

	return nil
}

// matchNetworkingFilters reports whether item satisfies every filter (filters are
// AND-ed, values within one filter are OR-ed), using a matcher that also reports
// whether each name is known. Filter names are validated up front, so an unknown
// name never reaches here.
func matchNetworkingFilters[T any](
	item *T,
	filters []awsquery.Filter,
	match func(*T, awsquery.Filter) (matched, known bool),
) bool {
	for _, f := range filters {
		if matched, _ := match(item, f); !matched {
			return false
		}
	}

	return true
}

// tagFilterMatch evaluates a "tag:<key>" or "tag-key" filter against a resource's
// tags. The second result reports whether name was a tag filter at all, so a
// caller can fall through to its own non-tag filters.
func tagFilterMatch(name string, values []string, tags map[string]string) (matched, isTag bool) {
	switch {
	case strings.HasPrefix(name, tagFilterPrefix):
		v, ok := tags[strings.TrimPrefix(name, tagFilterPrefix)]
		return ok && containsString(values, v), true
	case name == filterTagKey:
		for k := range tags {
			if containsString(values, k) {
				return true, true
			}
		}

		return false, true
	default:
		return false, false
	}
}

// stripCodePrefix removes the internal "<Code>: " routing marker some driver
// messages carry (used by the wire layer to pick the right EC2 error code) so it
// never leaks into the user-facing <Message>. Real EC2 messages carry no such
// prefix. The prefix is only trimmed when present, leaving other messages as-is.
func stripCodePrefix(msg, prefix string) string {
	return strings.TrimPrefix(msg, prefix)
}

// writeInvalidVPCIfMissing reports whether err is the driver's "referenced VPC
// does not exist" NotFound and, if so, answers InvalidVpcID.NotFound. The
// Create{Subnet,SecurityGroup,RouteTable} actions all take a VpcId; when that
// VPC is absent real EC2 reports InvalidVpcID.NotFound (the VPC is the missing
// resource) rather than the created resource's own NotFound code, which would
// wrongly tell the caller the not-yet-created subnet/group/table is missing.
func writeInvalidVPCIfMissing(w http.ResponseWriter, err error) bool {
	if cerrors.IsNotFound(err) && strings.Contains(strings.ToLower(cerrors.Message(err)), "vpc ") {
		awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidVpcID.NotFound", cerrors.Message(err))
		return true
	}

	return false
}

// writeReturnTrue writes the common EC2 "<return>true</return>" acknowledgement
// with a caller-supplied response root element (set at runtime via xml.Name).
// SDK output shapes ignore unknown fields, so a return element is harmless even
// for actions whose real response carries only a requestId.
func writeReturnTrue(w http.ResponseWriter, rootElement string) {
	awsquery.WriteXMLResponse(w, struct {
		XMLName   xml.Name
		Xmlns     string `xml:"xmlns,attr"`
		RequestID string `xml:"requestId"`
		Return    bool   `xml:"return"`
	}{
		XMLName:   xml.Name{Local: rootElement},
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		Return:    true,
	})
}
