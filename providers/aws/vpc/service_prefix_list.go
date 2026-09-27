package vpc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

const (
	// servicePrefixListOwner is the ownerId EC2 reports on the AWS-managed
	// service prefix lists.
	servicePrefixListOwner = "AWS"
	// servicePrefixListIDHexLen is the length of the hex part of a service
	// list id (pl-63a5400a style).
	servicePrefixListIDHexLen = 8
	// serviceNamePrefix starts every AWS service endpoint name.
	serviceNamePrefix = "com.amazonaws."

	routeStateActive = "active"
)

// RouteTargetVPCEndpoint marks the route a Gateway endpoint adds. It encodes
// as gatewayId on the wire, like real EC2.
const RouteTargetVPCEndpoint = "vpc-endpoint"

// gatewayServiceCIDRs returns the services that have a gateway endpoint (and so
// a prefix list), each with the address ranges its list holds. The ranges are
// the published us-east-1 ones; every region reuses them.
func gatewayServiceCIDRs() map[string][]string {
	return map[string][]string{
		"s3": {
			"3.5.0.0/19", "16.182.0.0/16", "18.34.0.0/19",
			"18.34.232.0/21", "52.216.0.0/15", "54.231.0.0/16",
		},
		"dynamodb": {
			"3.218.180.0/22", "3.218.184.0/22", "52.94.0.0/22", "52.119.224.0/20",
		},
	}
}

// gatewayServices is the fixed order the service lists are returned in.
func gatewayServices() []string {
	return []string{"s3", "dynamodb"}
}

// servicePrefixListID derives the pl- id for a service list from its name, so
// the id is the same on every call and differs between regions.
func servicePrefixListID(name string) string {
	sum := sha256.Sum256([]byte(name))

	return "pl-" + hex.EncodeToString(sum[:])[:servicePrefixListIDHexLen]
}

// servicePrefixListForEndpoint returns the pl- id a Gateway endpoint for
// serviceName routes to, or "" when the service has no prefix list.
func servicePrefixListForEndpoint(serviceName string) string {
	rest, ok := strings.CutPrefix(serviceName, serviceNamePrefix)
	if !ok {
		return ""
	}

	dot := strings.LastIndex(rest, ".")
	if dot <= 0 {
		return ""
	}

	if _, known := gatewayServiceCIDRs()[rest[dot+1:]]; !known {
		return ""
	}

	return servicePrefixListID(serviceName)
}

func (m *Mock) servicePrefixLists(region string) []driver.ServicePrefixList {
	region = orDefaultStr(region, m.opts.Region)
	cidrs := gatewayServiceCIDRs()

	out := make([]driver.ServicePrefixList, 0, len(cidrs))

	for _, svc := range gatewayServices() {
		name := serviceNamePrefix + region + "." + svc
		out = append(out, driver.ServicePrefixList{
			ID:    servicePrefixListID(name),
			Name:  name,
			CIDRs: append([]string(nil), cidrs[svc]...),
		})
	}

	return out
}

// DescribePrefixLists returns the AWS service prefix lists for region.
func (m *Mock) DescribePrefixLists(
	_ context.Context, region string, ids []string,
) ([]driver.ServicePrefixList, error) {
	all := m.servicePrefixLists(region)
	if len(ids) == 0 {
		return all, nil
	}

	byID := make(map[string]driver.ServicePrefixList, len(all))
	for _, pl := range all {
		byID[pl.ID] = pl
	}

	out := make([]driver.ServicePrefixList, 0, len(ids))

	for _, id := range ids {
		pl, ok := byID[id]
		if !ok {
			return nil, errors.Newf(errors.NotFound, "The prefix list ID '%s' does not exist", id)
		}

		out = append(out, pl)
	}

	return out, nil
}

// DescribeAWSManagedPrefixLists returns the service lists in the managed
// prefix list shape. Unknown ids are skipped. MaxEntries and Version stay
// zero: EC2 reports neither for an AWS-owned list.
func (m *Mock) DescribeAWSManagedPrefixLists(
	_ context.Context, region string, ids []string,
) ([]driver.PrefixList, error) {
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}

	var out []driver.PrefixList

	for _, pl := range m.servicePrefixLists(region) {
		if len(ids) > 0 && !want[pl.ID] {
			continue
		}

		entries := make([]driver.PrefixListEntry, 0, len(pl.CIDRs))
		for _, c := range pl.CIDRs {
			entries = append(entries, driver.PrefixListEntry{CIDR: c})
		}

		out = append(out, driver.PrefixList{
			ID: pl.ID, Name: pl.Name, AddressFamily: "IPv4",
			State: "create-complete", Entries: entries, OwnerID: servicePrefixListOwner,
		})
	}

	return out, nil
}

// syncEndpointRoutes makes the prefix-list routes of a Gateway endpoint match
// its route table set: one route per table to the service's pl- id, removed
// from tables the endpoint no longer uses. Tables that do not exist are
// skipped. Other endpoint types hold no routes. The caller holds m.mu.
func (m *Mock) syncEndpointRoutes(ep *driver.VPCEndpoint) {
	plID := endpointPrefixList(ep)

	want := map[string]bool{}

	if plID != "" {
		for _, id := range ep.RouteTableIDs {
			want[id] = true
		}
	}

	for _, rt := range m.routeTables.All() {
		rt.Routes = endpointRoutes(rt.Routes, ep.ID, plID, want[rt.ID])
	}
}

// endpointPrefixList returns the pl- id a Gateway endpoint routes to, or ""
// for other endpoint types and services without a prefix list.
func endpointPrefixList(ep *driver.VPCEndpoint) string {
	if ep.EndpointType != "" && ep.EndpointType != vpcEndpointTypeGateway {
		return ""
	}

	return servicePrefixListForEndpoint(ep.ServiceName)
}

// endpointRoutes returns routes with at most one route to endpointID, kept
// (or added, pointing at plID) only when keep is set.
func endpointRoutes(routes []driver.Route, endpointID, plID string, keep bool) []driver.Route {
	out := routes[:0:0]
	has := false

	for _, r := range routes {
		if r.TargetID != endpointID {
			out = append(out, r)
			continue
		}

		if keep && !has {
			has = true

			out = append(out, r)
		}
	}

	if keep && !has {
		out = append(out, driver.Route{
			DestinationPrefixListID: plID,
			TargetID:                endpointID,
			TargetType:              RouteTargetVPCEndpoint,
			State:                   routeStateActive,
		})
	}

	return out
}
