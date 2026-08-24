package vnet

import (
	"context"
	"fmt"
	"net"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// Compile-time check that Mock serves the Azure network-interface surface.
var _ driver.AzureNetworkInterfaces = (*Mock)(nil)

// Azure reserves the first four host addresses of every subnet (the network
// address, the default gateway, and two for Azure-internal use), so the first
// assignable private IP is the fourth host. (Azure VNet subnet-address FAQ.)
const azureReservedHosts = 4

// Allocation methods for an ipConfiguration's private IP.
const (
	allocationDynamic = "Dynamic"
	allocationStatic  = "Static"
)

// nicData is the stored Azure network interface, keyed by (resourceGroup, name).
type nicData struct {
	Name              string
	ResourceGroup     string
	Location          string
	Tags              map[string]string
	IPConfigs         []driver.AzureIPConfig
	IPForwarding      bool
	MACAddress        string
	ResourceGUID      string
	ProvisioningState string
	ETag              string
	VirtualMachineID  string
}

// nicKey composes the store key from the ARM addressing pair. Resource-group
// names are case-insensitive in Azure, so it is lower-cased; the interface name
// is preserved as-is.
func nicKey(resourceGroup, name string) string {
	return strings.ToLower(resourceGroup) + "/" + name
}

// CreateOrUpdateNetworkInterface creates a NIC or updates it in place. It is
// idempotent by (resourceGroup, name): a second PUT to the same pair updates
// the existing interface rather than creating a duplicate, matching ARM.
func (m *Mock) CreateOrUpdateNetworkInterface(
	_ context.Context, resourceGroup, name string, cfg driver.AzureNICConfig,
) (*driver.AzureNIC, error) {
	if resourceGroup == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "resource group is required")
	}

	if name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "network interface name is required")
	}

	m.nicMu.Lock()
	defer m.nicMu.Unlock()

	key := nicKey(resourceGroup, name)
	existing, isUpdate := m.nics.Get(key)

	ipConfigs, err := m.resolveIPConfigs(cfg.IPConfigs, key)
	if err != nil {
		return nil, err
	}

	nic := &nicData{
		Name:              name,
		ResourceGroup:     resourceGroup,
		Location:          cfg.Location,
		Tags:              copyTags(cfg.Tags),
		IPConfigs:         ipConfigs,
		IPForwarding:      cfg.IPForwarding,
		ProvisioningState: "Succeeded",
		ETag:              `W/"` + idgen.GenerateID("etag-") + `"`,
	}

	if isUpdate {
		// Preserve server-assigned identity across updates, matching ARM.
		nic.MACAddress = existing.MACAddress
		nic.ResourceGUID = existing.ResourceGUID
		nic.VirtualMachineID = existing.VirtualMachineID
	} else {
		nic.MACAddress = generateMAC()
		nic.ResourceGUID = generateGUID()
	}

	m.nics.Set(key, nic)

	out := toAzureNIC(nic)

	return &out, nil
}

// GetNetworkInterface returns the NIC identified by (resourceGroup, name).
func (m *Mock) GetNetworkInterface(_ context.Context, resourceGroup, name string) (*driver.AzureNIC, error) {
	m.nicMu.RLock()
	defer m.nicMu.RUnlock()

	nic, ok := m.nics.Get(nicKey(resourceGroup, name))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "network interface %q not found in resource group %q", name, resourceGroup)
	}

	out := toAzureNIC(nic)

	return &out, nil
}

// DeleteNetworkInterface removes a NIC. A NIC attached to a VM cannot be
// deleted, matching ARM's InUseNetworkInterfaceCannotBeDeleted.
func (m *Mock) DeleteNetworkInterface(_ context.Context, resourceGroup, name string) error {
	m.nicMu.Lock()
	defer m.nicMu.Unlock()

	key := nicKey(resourceGroup, name)

	nic, ok := m.nics.Get(key)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "network interface %q not found in resource group %q", name, resourceGroup)
	}

	if nic.VirtualMachineID != "" {
		return cerrors.Newf(cerrors.FailedPrecondition,
			"network interface %q cannot be deleted because it is attached to %q", name, nic.VirtualMachineID)
	}

	m.nics.Delete(key)

	return nil
}

// ListNetworkInterfaces returns NICs in a resource group, or all when
// resourceGroup is empty (subscription-wide list).
func (m *Mock) ListNetworkInterfaces(_ context.Context, resourceGroup string) ([]driver.AzureNIC, error) {
	m.nicMu.RLock()
	defer m.nicMu.RUnlock()

	out := make([]driver.AzureNIC, 0)

	for _, nic := range m.nics.All() {
		if resourceGroup != "" && !strings.EqualFold(nic.ResourceGroup, resourceGroup) {
			continue
		}

		out = append(out, toAzureNIC(nic))
	}

	return out, nil
}

// resolveIPConfigs fills in a private IP for every Dynamic ipConfiguration by
// allocating the next free host from the referenced subnet's prefix, leaving
// Static configs' submitted address untouched. selfKey is excluded from the
// in-use scan so a re-PUT of the same NIC doesn't collide with its own IPs.
func (m *Mock) resolveIPConfigs(configs []driver.AzureIPConfig, selfKey string) ([]driver.AzureIPConfig, error) {
	out := make([]driver.AzureIPConfig, 0, len(configs))

	for i := range configs {
		cfg := configs[i]

		method := cfg.AllocationMethod
		if method == "" {
			method = allocationDynamic
		}

		cfg.AllocationMethod = method

		if method == allocationStatic {
			if cfg.PrivateIP == "" {
				return nil, cerrors.New(cerrors.InvalidArgument,
					"a Static ipConfiguration requires privateIPAddress")
			}

			out = append(out, cfg)

			continue
		}

		// Dynamic: allocate from the subnet prefix if one was resolved.
		if cfg.SubnetCIDR != "" && cfg.PrivateIP == "" {
			ip, err := m.allocatePrivateIP(cfg.SubnetID, cfg.SubnetCIDR, selfKey)
			if err != nil {
				return nil, err
			}

			cfg.PrivateIP = ip
		}

		out = append(out, cfg)
	}

	return out, nil
}

// allocatePrivateIP returns the lowest free host in cidr (starting past Azure's
// reserved addresses) that no other NIC's ipConfiguration in the same subnet
// already holds.
func (m *Mock) allocatePrivateIP(subnetID, cidr, selfKey string) (string, error) {
	baseIP, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return "", cerrors.Newf(cerrors.InvalidArgument, "invalid subnet CIDR %q: %v", cidr, err)
	}

	used := m.usedPrivateIPs(subnetID, selfKey)

	candidate := make(net.IP, len(baseIP.To4()))
	copy(candidate, baseIP.To4())

	for host := azureReservedHosts; ; host++ {
		ip := addHost(candidate, host)
		if !ipNet.Contains(ip) {
			return "", cerrors.Newf(cerrors.ResourceExhausted, "subnet %q has no free private IP", subnetID)
		}

		s := ip.String()
		if !used[s] {
			return s, nil
		}
	}
}

// usedPrivateIPs collects the private IPs already assigned in subnetID across
// every stored NIC except the one being updated (selfKey).
func (m *Mock) usedPrivateIPs(subnetID, selfKey string) map[string]bool {
	used := make(map[string]bool)

	for key, nic := range m.nics.All() {
		if key == selfKey {
			continue
		}

		for _, ipc := range nic.IPConfigs {
			if ipc.SubnetID == subnetID && ipc.PrivateIP != "" {
				used[ipc.PrivateIP] = true
			}
		}
	}

	return used
}

// addHost returns base (an IPv4 network address) plus n host addresses.
func addHost(base net.IP, n int) net.IP {
	v4 := base.To4()
	out := make(net.IP, len(v4))
	copy(out, v4)

	carry := n
	for i := len(out) - 1; i >= 0 && carry > 0; i-- {
		sum := int(out[i]) + carry
		out[i] = byte(sum % maxOctetValue) //nolint:gosec // sum % 256 is always within byte range
		carry = sum / maxOctetValue
	}

	return out
}

func toAzureNIC(n *nicData) driver.AzureNIC {
	configs := make([]driver.AzureIPConfig, len(n.IPConfigs))
	copy(configs, n.IPConfigs)

	return driver.AzureNIC{
		Name:              n.Name,
		ResourceGroup:     n.ResourceGroup,
		Location:          n.Location,
		Tags:              copyTags(n.Tags),
		IPConfigs:         configs,
		IPForwarding:      n.IPForwarding,
		MACAddress:        n.MACAddress,
		ResourceGUID:      n.ResourceGUID,
		ProvisioningState: n.ProvisioningState,
		ETag:              n.ETag,
		VirtualMachineID:  n.VirtualMachineID,
	}
}

// generateMAC returns a deterministic locally-administered MAC using Azure's
// 00-0D-3A OUI prefix, so tests under a fake clock stay reproducible.
func generateMAC() string {
	id := idgen.GenerateID("")

	return fmt.Sprintf("00-0D-3A-%02X-%02X-%02X",
		len(id)%maxOctetValue, (len(id)*7)%maxOctetValue, (len(id)*13)%maxOctetValue)
}

// generateGUID returns a GUID-shaped identifier for resourceGuid fields.
func generateGUID() string {
	return fmt.Sprintf("%s-0000-0000-0000-000000000000", padHex(idgen.GenerateID("")))
}

func padHex(s string) string {
	const width = 8

	h := fmt.Sprintf("%x", s)
	if len(h) >= width {
		return h[:width]
	}

	return strings.Repeat("0", width-len(h)) + h
}
