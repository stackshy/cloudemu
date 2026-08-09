// Package vcn provides an in-memory mock implementation of OCI Virtual Cloud
// Network. It implements the portable networking driver: a VCN is the VPC, a
// network security group is the security group, and a security list is the
// network ACL.
package vcn

import (
	"context"
	"encoding/binary"
	"net"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/networking/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

const timeFormat = time.RFC3339

// OCI lifecycle states. Real VCN resources report these rather than EC2's
// lowercase ones, and the wire layer passes them straight through.
const (
	StateAvailable = "AVAILABLE"
	StateDetached  = "DETACHED"
	StateAttached  = "ATTACHED"
)

// OCID resource type segments.
const (
	typeVCN            = "vcn"
	typeSubnet         = "subnet"
	typeNSG            = "networksecuritygroup"
	typeSecurityList   = "securitylist"
	typeRouteTable     = "routetable"
	typeInternetGW     = "internetgateway"
	typeNATGateway     = "natgateway"
	typeServiceGateway = "servicegateway"
	typeDHCPOptions    = "dhcpoptions"
	typeVNIC           = "vnic"
	typePrivateIP      = "privateip"
	typePublicIP       = "publicip"
	typeLocalPeering   = "localpeeringgateway"
	typeLog            = "log"
)

// Compile-time check that Mock implements driver.Networking.
var _ driver.Networking = (*Mock)(nil)

// Optional driver capabilities, discovered by type assertion.
var (
	_ driver.VPCAttributes           = (*Mock)(nil)
	_ driver.NetworkInterfaces       = (*Mock)(nil)
	_ driver.NetworkInterfaceCreator = (*Mock)(nil)
)

type vcnData struct {
	ID                    string
	CIDRBlock             string
	State                 string
	Tags                  map[string]string
	DNSSupport            bool
	DNSHostnames          bool
	DefaultRouteTableID   string
	DefaultSecurityListID string
	DefaultDHCPOptionsID  string
}

// Mock is an in-memory mock implementation of the OCI VCN service.
type Mock struct {
	// mu guards the fields of stored values and spans the reads and writes a
	// single operation makes across stores. Each store locks its own map, but
	// the pointers it hands back are mutated in place while Describe calls
	// walk them, and checks such as AssociateAddress's 1:1 guard read one
	// store before writing another.
	mu sync.RWMutex

	vcns          *memstore.Store[*vcnData]
	subnets       *memstore.Store[*subnetData]
	nsgs          *memstore.Store[*nsgData]
	securityLists *memstore.Store[*securityListData]
	routeTables   *memstore.Store[*routeTableData]
	rtAssocs      *memstore.Store[*rtAssocData]
	igws          *memstore.Store[*igwData]
	natGateways   *memstore.Store[*natGatewayData]
	serviceGWs    *memstore.Store[*serviceGatewayData]
	publicIPs     *memstore.Store[*publicIPData]
	vnics         *memstore.Store[*vnicData]
	privateIPs    *memstore.Store[*privateIPData]
	dhcpOptions   *memstore.Store[*dhcpOptionsData]
	peerings      *memstore.Store[*peeringData]
	flowLogs      *memstore.Store[*flowLogData]
	// scopes and created hold what the portable projections have no room for:
	// the compartment a resource was created in and when, keyed by OCID.
	scopes  *memstore.Store[scope.Scope]
	created *memstore.Store[string]
	opts    *config.Options
}

// New creates a new OCI VCN mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		vcns:          memstore.New[*vcnData](),
		subnets:       memstore.New[*subnetData](),
		nsgs:          memstore.New[*nsgData](),
		securityLists: memstore.New[*securityListData](),
		routeTables:   memstore.New[*routeTableData](),
		rtAssocs:      memstore.New[*rtAssocData](),
		igws:          memstore.New[*igwData](),
		natGateways:   memstore.New[*natGatewayData](),
		serviceGWs:    memstore.New[*serviceGatewayData](),
		publicIPs:     memstore.New[*publicIPData](),
		vnics:         memstore.New[*vnicData](),
		privateIPs:    memstore.New[*privateIPData](),
		dhcpOptions:   memstore.New[*dhcpOptionsData](),
		peerings:      memstore.New[*peeringData](),
		flowLogs:      memstore.New[*flowLogData](),
		scopes:        memstore.New[scope.Scope](),
		created:       memstore.New[string](),
		opts:          opts,
	}
}

// Scope returns the compartment a resource was created in. It is an OPTIONAL
// capability, discovered by type assertion: the portable Networking driver
// has no compartment parameter, so OCI scoping is exposed alongside it.
func (m *Mock) Scope(id string) scope.Scope {
	m.mu.RLock()
	defer m.mu.RUnlock()

	s, _ := m.scopes.Get(id)

	return s
}

// SetScope records the compartment a resource belongs to, replacing the
// default recorded at create time. Deleting the resource forgets it.
func (m *Mock) SetScope(id string, s scope.Scope) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if s.IsZero() {
		m.scopes.Delete(id)
		return
	}

	m.scopes.Set(id, s)
}

// Created returns the OCI timestamp a resource was created at, or the empty
// string for an unknown OCID. Part of the same optional capability as Scope.
func (m *Mock) Created(id string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	t, _ := m.created.Get(id)

	return t
}

// newOCID mints an OCID for the given resource type in the configured realm
// and region.
func (m *Mock) newOCID(resourceType string) string {
	return idgen.OCID(resourceType, m.opts.Realm, m.opts.OCIRegion())
}

// now returns the current time in OCI's timestamp format.
func (m *Mock) now() string {
	return m.opts.Clock.Now().UTC().Format(timeFormat)
}

// record stamps a newly created resource with its creation time and the
// default compartment. A wire caller naming another compartment overwrites
// the latter with SetScope.
func (m *Mock) record(id string) {
	m.scopes.Set(id, scope.Scope{Compartment: m.opts.CompartmentID})
	m.created.Set(id, m.now())
}

// forget drops the scope and creation time of a deleted resource.
func (m *Mock) forget(id string) {
	m.scopes.Delete(id)
	m.created.Delete(id)
}

// CreateVPC creates a VCN along with the default route table, security list
// and DHCP options real OCI creates with it.
func (m *Mock) CreateVPC(_ context.Context, cfg driver.VPCConfig) (*driver.VPCInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := validateCIDR(cfg.CIDRBlock, "VCN"); err != nil {
		return nil, err
	}

	id := m.newOCID(typeVCN)
	v := &vcnData{
		ID:         id,
		CIDRBlock:  cfg.CIDRBlock,
		State:      StateAvailable,
		Tags:       copyTags(cfg.Tags),
		DNSSupport: true,
	}

	m.vcns.Set(id, v)
	m.record(id)

	v.DefaultRouteTableID = m.newDefaultRouteTable(v)
	v.DefaultSecurityListID = m.newDefaultSecurityList(v)
	v.DefaultDHCPOptionsID = m.newDefaultDHCPOptions(v)

	info := toVPCInfo(v)

	return &info, nil
}

// DeleteVPC deletes a VCN. Real OCI refuses while the VCN still holds
// subnets or gateways, and takes its default resources down with it.
func (m *Mock) DeleteVPC(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	v, ok := m.vcns.Get(id)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "VCN %q not found", id)
	}

	if err := m.vcnIsEmpty(id); err != nil {
		return err
	}

	m.routeTables.Delete(v.DefaultRouteTableID)
	m.securityLists.Delete(v.DefaultSecurityListID)
	m.dhcpOptions.Delete(v.DefaultDHCPOptionsID)
	m.forget(v.DefaultRouteTableID)
	m.forget(v.DefaultSecurityListID)
	m.forget(v.DefaultDHCPOptionsID)
	m.vcns.Delete(id)
	m.forget(id)

	return nil
}

// vcnIsEmpty reports whether anything still references the VCN. Real OCI
// refuses the delete until every attached resource is gone; only the three
// default resources OCI created with the VCN go down with it, so a non-default
// route table, security list or DHCP options set blocks the same way a subnet
// does.
func (m *Mock) vcnIsEmpty(id string) error {
	attached := []struct {
		what string
		held bool
	}{
		{"subnets", anyIn(m.subnets, func(s *subnetData) bool { return s.VCNID == id })},
		{"internet gateways", anyIn(m.igws, func(g *igwData) bool { return g.VCNID == id })},
		{"NAT gateways", anyIn(m.natGateways, func(g *natGatewayData) bool { return g.VCNID == id })},
		{"service gateways", anyIn(m.serviceGWs, func(g *serviceGatewayData) bool { return g.VCNID == id })},
		{"network security groups", anyIn(m.nsgs, func(n *nsgData) bool { return n.VCNID == id })},
		{"route tables", anyIn(m.routeTables, func(rt *routeTableData) bool {
			return rt.VCNID == id && !rt.IsDefault
		})},
		{"security lists", anyIn(m.securityLists, func(sl *securityListData) bool {
			return sl.VCNID == id && !sl.IsDefault
		})},
		{"DHCP options", anyIn(m.dhcpOptions, func(d *dhcpOptionsData) bool {
			return d.VCNID == id && !d.IsDefault
		})},
		{"peerings", anyIn(m.peerings, func(p *peeringData) bool {
			return p.RequesterVCN == id || p.AccepterVCN == id
		})},
	}

	for _, a := range attached {
		if a.held {
			return cerrors.Newf(cerrors.FailedPrecondition, "VCN %q still has %s", id, a.what)
		}
	}

	return nil
}

// DescribeVPCs returns VCNs matching the given OCIDs, or all if ids is empty.
func (m *Mock) DescribeVPCs(_ context.Context, ids []string) ([]driver.VPCInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return describeResources(m.vcns, ids, toVPCInfo), nil
}

// ModifyVPCAttribute toggles the VCN's internal DNS resolver, which OCI
// drives off the VCN's DNS label rather than a pair of standalone flags.
func (m *Mock) ModifyVPCAttribute(_ context.Context, id string, update driver.VPCAttributeUpdate) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.vcns.Update(id, func(v *vcnData) *vcnData {
		if update.EnableDNSSupport != nil {
			v.DNSSupport = *update.EnableDNSSupport
		}

		if update.EnableDNSHostnames != nil {
			v.DNSHostnames = *update.EnableDNSHostnames
		}

		return v
	}) {
		return cerrors.Newf(cerrors.NotFound, "VCN %q not found", id)
	}

	return nil
}

// UpdateVPCTags merges freeform tags into the VCN's tag map. The mutation
// runs under memstore.Update's lock; a fresh map is swapped in so concurrent
// readers iterating the old map are unaffected.
func (m *Mock) UpdateVPCTags(_ context.Context, id string, tags map[string]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.vcns.Update(id, func(v *vcnData) *vcnData {
		v.Tags = mergeTagMap(v.Tags, tags)
		return v
	}) {
		return cerrors.Newf(cerrors.NotFound, "VCN %q not found", id)
	}

	return nil
}

// RemoveVPCTags removes the given freeform tag keys from a VCN.
func (m *Mock) RemoveVPCTags(_ context.Context, id string, keys []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.vcns.Update(id, func(v *vcnData) *vcnData {
		v.Tags = removeTagMapKeys(v.Tags, keys)
		return v
	}) {
		return cerrors.Newf(cerrors.NotFound, "VCN %q not found", id)
	}

	return nil
}

// DefaultResources names the resources OCI creates alongside a VCN and
// reports on the VCN itself. The portable VPC projection has no room for them.
type DefaultResources struct {
	RouteTableID   string
	SecurityListID string
	DHCPOptionsID  string
}

// Defaults returns the default resources created with a VCN.
func (m *Mock) Defaults(vcnID string) DefaultResources {
	m.mu.RLock()
	defer m.mu.RUnlock()

	v, ok := m.vcns.Get(vcnID)
	if !ok {
		return DefaultResources{}
	}

	return DefaultResources{
		RouteTableID:   v.DefaultRouteTableID,
		SecurityListID: v.DefaultSecurityListID,
		DHCPOptionsID:  v.DefaultDHCPOptionsID,
	}
}

func toVPCInfo(v *vcnData) driver.VPCInfo {
	return driver.VPCInfo{
		ID:                 v.ID,
		CIDRBlock:          v.CIDRBlock,
		State:              v.State,
		Tags:               copyTags(v.Tags),
		EnableDNSSupport:   v.DNSSupport,
		EnableDNSHostnames: v.DNSHostnames,
	}
}

// describeResources lists a store, filtered to ids when any are given.
// Unfiltered lists come back ordered by OCID so paging is deterministic.
func describeResources[T any, R any](store *memstore.Store[T], ids []string, toInfo func(T) R) []R {
	if len(ids) == 0 {
		all := store.SortedValues()
		result := make([]R, 0, len(all))

		for _, item := range all {
			result = append(result, toInfo(item))
		}

		return result
	}

	result := make([]R, 0, len(ids))

	for _, id := range ids {
		item, ok := store.Get(id)
		if !ok {
			continue
		}

		result = append(result, toInfo(item))
	}

	return result
}

// mutate applies fn to a stored resource under the store's write lock, so a
// read-modify-write never races another writer. It reports notFound when the
// OCID is unknown, and otherwise whatever fn returns.
func mutate[T any](store *memstore.Store[T], id string, notFound error, fn func(T) error) error {
	err := notFound

	store.Update(id, func(v T) T {
		err = fn(v)

		return v
	})

	return err
}

// validateCIDR rejects an empty or unparseable CIDR block.
func validateCIDR(cidr, what string) error {
	if cidr == "" {
		return cerrors.Newf(cerrors.InvalidArgument, "%s CIDR block is required", what)
	}

	if _, _, err := net.ParseCIDR(cidr); err != nil {
		return cerrors.Newf(cerrors.InvalidArgument, "invalid %s CIDR block %q", what, cidr)
	}

	return nil
}

// cidrContains reports whether outer fully contains inner.
func cidrContains(outer, inner string) bool {
	_, outerNet, err := net.ParseCIDR(outer)
	if err != nil {
		return false
	}

	innerIP, innerNet, err := net.ParseCIDR(inner)
	if err != nil {
		return false
	}

	outerOnes, _ := outerNet.Mask.Size()
	innerOnes, _ := innerNet.Mask.Size()

	return outerNet.Contains(innerIP) && outerOnes <= innerOnes
}

// cidrsOverlap reports whether two CIDR blocks share any address.
func cidrsOverlap(cidrA, cidrB string) bool {
	_, netA, errA := net.ParseCIDR(cidrA)
	_, netB, errB := net.ParseCIDR(cidrB)

	if errA != nil || errB != nil {
		return cidrA == cidrB
	}

	return netA.Contains(netB.IP) || netB.Contains(netA.IP)
}

// hostIP returns the address offset hosts into a CIDR block, or the empty
// string when the block is not a parseable IPv4 network.
func hostIP(cidr string, offset uint32) string {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return ""
	}

	base := ipnet.IP.To4()
	if base == nil {
		return ""
	}

	out := make(net.IP, net.IPv4len)
	binary.BigEndian.PutUint32(out, binary.BigEndian.Uint32(base)+offset)

	return out.String()
}

// mergeTagMap returns a fresh map containing existing's keys plus tags's keys
// (tags wins on overlap). The original map is not modified.
func mergeTagMap(existing, tags map[string]string) map[string]string {
	out := make(map[string]string, len(existing)+len(tags))

	for k, v := range existing {
		out[k] = v
	}

	for k, v := range tags {
		out[k] = v
	}

	return out
}

// removeTagMapKeys returns a fresh map with the listed keys removed.
func removeTagMapKeys(existing map[string]string, keys []string) map[string]string {
	if len(existing) == 0 {
		return existing
	}

	drop := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		drop[k] = struct{}{}
	}

	out := make(map[string]string, len(existing))

	for k, v := range existing {
		if _, gone := drop[k]; gone {
			continue
		}

		out[k] = v
	}

	return out
}

// copyTags creates a shallow copy of a tags map.
func copyTags(tags map[string]string) map[string]string {
	if tags == nil {
		return make(map[string]string)
	}

	out := make(map[string]string, len(tags))
	for k, v := range tags {
		out[k] = v
	}

	return out
}

// copyStringSlice creates a shallow copy of a string slice.
func copyStringSlice(src []string) []string {
	if src == nil {
		return nil
	}

	out := make([]string, len(src))
	copy(out, src)

	return out
}

// anyIn reports whether any value in a store satisfies match.
func anyIn[T any](store *memstore.Store[T], match func(T) bool) bool {
	for _, v := range store.All() {
		if match(v) {
			return true
		}
	}

	return false
}

// appendItem returns a fresh slice with v appended. Mutation under a store's
// lock swaps whole slices rather than growing one in place, so a reader
// holding the old slice keeps a consistent view of it.
func appendItem[T any](src []T, v T) []T {
	out := make([]T, len(src), len(src)+1)
	copy(out, src)

	return append(out, v)
}

// removeAt returns a fresh slice with index i removed.
func removeAt[T any](src []T, i int) []T {
	out := make([]T, 0, len(src)-1)
	out = append(out, src[:i]...)

	return append(out, src[i+1:]...)
}
