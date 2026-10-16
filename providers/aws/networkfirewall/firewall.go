package networkfirewall

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	nfdriver "github.com/stackshy/cloudemu/v2/services/networkfirewall/driver"
)

// Enum values copied verbatim from the AWS SDK (networkfirewall types).
//
//nolint:gochecknoglobals // immutable enum value sets.
var (
	analysisTypes = []string{"TLS_SNI", "HTTP_HOST"}
	ipAddressType = []string{"DUALSTACK", "IPV4", "IPV6"}
)

//nolint:gocritic // cfg matches the driver signature.
func (m *Mock) CreateFirewall(_ context.Context, cfg nfdriver.CreateFirewallConfig) (*nfdriver.Firewall, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.validateCreateFirewall(&cfg); err != nil {
		return nil, err
	}

	fw := &nfdriver.Firewall{
		Name:                             cfg.Name,
		ARN:                              m.arn("firewall", cfg.Name),
		ID:                               idgen.GenerateID(""),
		PolicyARN:                        cfg.PolicyARN,
		VPCID:                            cfg.VPCID,
		SubnetIDs:                        cloneStrings(cfg.SubnetIDs),
		AvailabilityZones:                cloneStrings(cfg.AvailabilityZones),
		TransitGatewayID:                 cfg.TransitGatewayID,
		Description:                      cfg.Description,
		DeleteProtection:                 cfg.DeleteProtection,
		SubnetChangeProtection:           cfg.SubnetChangeProtection,
		FirewallPolicyChangeProtection:   cfg.FirewallPolicyChangeProtection,
		AvailabilityZoneChangeProtection: cfg.AvailabilityZoneChangeProtection,
		EnabledAnalysisTypes:             cloneStrings(cfg.EnabledAnalysisTypes),
		Status:                           statusReady,
		UpdateToken:                      newToken(),
		Tags:                             copyTags(cfg.Tags),
	}
	if !m.firewalls.SetIfAbsent(cfg.Name, fw) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "firewall %q already exists", cfg.Name)
	}

	return m.outFirewall(fw), nil
}

func (m *Mock) validateCreateFirewall(cfg *nfdriver.CreateFirewallConfig) error {
	if err := validateName("FirewallName", cfg.Name); err != nil {
		return err
	}

	if err := validateDescription(cfg.Description); err != nil {
		return err
	}

	if err := validateTags(cfg.Tags); err != nil {
		return err
	}

	if err := validateARN("FirewallPolicyArn", cfg.PolicyARN); err != nil {
		return err
	}

	if !m.policyExists(cfg.PolicyARN) {
		return invalid("firewall policy %q not found", cfg.PolicyARN)
	}

	if err := validateFirewallPlacement(cfg); err != nil {
		return err
	}

	for _, t := range cfg.EnabledAnalysisTypes {
		if !oneOf(t, analysisTypes...) {
			return invalid("EnabledAnalysisTypes value %q is not valid", t)
		}
	}

	if m.firewalls.Has(cfg.Name) {
		return cerrors.Newf(cerrors.AlreadyExists, "firewall %q already exists", cfg.Name)
	}

	return nil
}

// validateFirewallPlacement checks where the firewall lives: a VPC (VpcId plus
// subnets) or a transit gateway (TransitGatewayId plus Availability Zones).
func validateFirewallPlacement(cfg *nfdriver.CreateFirewallConfig) error {
	if cfg.TransitGatewayID != "" {
		if cfg.VPCID != "" || len(cfg.SubnetIDs) > 0 {
			return invalid("a transit gateway-attached firewall takes AvailabilityZoneMappings, not VpcId or SubnetMappings")
		}

		if len(cfg.AvailabilityZones) == 0 {
			return invalid("AvailabilityZoneMappings is required for a transit gateway-attached firewall")
		}

		return nil
	}

	if len(cfg.AvailabilityZones) > 0 {
		return invalid("AvailabilityZoneMappings is only valid with TransitGatewayId")
	}

	if cfg.VPCID == "" {
		return invalid("VpcId is required")
	}

	return validateVPCID(cfg.VPCID)
}

func (m *Mock) DescribeFirewall(_ context.Context, name, arn string) (*nfdriver.Firewall, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if err := requireIdentifier("Firewall", name, arn); err != nil {
		return nil, err
	}

	fw, ok := m.lookupFirewall(name, arn)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "firewall %q not found", nonEmpty(name, arn))
	}

	return m.outFirewall(fw), nil
}

func (m *Mock) DeleteFirewall(_ context.Context, name, arn string) (*nfdriver.Firewall, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := requireIdentifier("Firewall", name, arn); err != nil {
		return nil, err
	}

	fw, ok := m.lookupFirewall(name, arn)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "firewall %q not found", nonEmpty(name, arn))
	}

	if fw.DeleteProtection {
		return nil, nfdriver.InvalidOperation("firewall %q has delete protection enabled", fw.Name)
	}

	if n := m.associationCount(fw.ARN); n > 0 {
		return nil, nfdriver.InvalidOperation("firewall %q still has %d VPC endpoint association(s)", fw.Name, n)
	}

	out := m.outFirewall(fw)
	out.Status = statusDeleting

	m.firewalls.Delete(fw.Name)
	m.dropFirewallState(fw)

	return out, nil
}

// dropFirewallState removes everything keyed by a deleted firewall so no entry
// is orphaned: logging, resource policy, flow operations and analysis reports.
func (m *Mock) dropFirewallState(fw *nfdriver.Firewall) {
	delete(m.logging, fw.Name)
	delete(m.resourcePolicies, fw.ARN)

	for id, op := range m.flowOperations.All() {
		if op.FirewallARN == fw.ARN {
			m.flowOperations.Delete(id)
		}
	}

	for id, rep := range m.analysisReports.All() {
		if rep.FirewallARN == fw.ARN {
			m.analysisReports.Delete(id)
		}
	}
}

func (m *Mock) ListFirewalls(_ context.Context, vpcIDs []string, opts nfdriver.ListOptions) ([]nfdriver.Firewall, string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, v := range vpcIDs {
		if err := validateVPCID(v); err != nil {
			return nil, "", err
		}
	}

	var matched []*nfdriver.Firewall

	for _, f := range m.firewalls.SortedValues() {
		if len(vpcIDs) == 0 || oneOf(f.VPCID, vpcIDs...) {
			matched = append(matched, f)
		}
	}

	items, next, err := paginate(matched, opts)
	if err != nil {
		return nil, "", err
	}

	out := make([]nfdriver.Firewall, 0, len(items))
	for _, f := range items {
		out = append(out, *m.outFirewall(f))
	}

	return out, next, nil
}

func (m *Mock) lookupFirewall(name, arn string) (*nfdriver.Firewall, bool) {
	if name != "" {
		return m.firewalls.Get(name)
	}

	for _, f := range m.firewalls.SortedValues() {
		if f.ARN == arn {
			return f, true
		}
	}

	return nil, false
}

// outFirewall returns a deep copy of the stored firewall with its computed
// fields filled in.
func (m *Mock) outFirewall(f *nfdriver.Firewall) *nfdriver.Firewall {
	out := *f
	out.SubnetIDs = cloneStrings(f.SubnetIDs)
	out.AvailabilityZones = cloneStrings(f.AvailabilityZones)
	out.EnabledAnalysisTypes = cloneStrings(f.EnabledAnalysisTypes)
	out.Tags = copyTags(f.Tags)
	out.NumberOfAssociations = m.associationCount(f.ARN)

	return &out
}

// mutateFirewall runs one update on a firewall as a single critical section:
// existence, the optimistic-lock token (optional: "" is an unconditional
// update), the change itself, then the token rotation.
func (m *Mock) mutateFirewall(name, token string, change func(*nfdriver.Firewall) error) (*nfdriver.Firewall, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	fw, err := m.firewallForUpdate(name, token)
	if err != nil {
		return nil, err
	}

	if err := change(fw); err != nil {
		return nil, err
	}

	fw.UpdateToken = newToken()

	return m.outFirewall(fw), nil
}

func (m *Mock) firewallForUpdate(name, token string) (*nfdriver.Firewall, error) {
	if err := validateName("FirewallName", name); err != nil {
		return nil, err
	}

	fw, ok := m.firewalls.Get(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "firewall %q not found", name)
	}

	if err := checkToken(fw.UpdateToken, token); err != nil {
		return nil, err
	}

	return fw, nil
}

// AssociateFirewallPolicy attaches a firewall policy to a firewall.
func (m *Mock) AssociateFirewallPolicy(_ context.Context, firewallName, policyARN, updateToken string) (*nfdriver.Firewall, error) {
	return m.mutateFirewall(firewallName, updateToken, func(fw *nfdriver.Firewall) error {
		if fw.FirewallPolicyChangeProtection {
			return nfdriver.InvalidOperation("firewall %q has firewall policy change protection enabled", fw.Name)
		}

		if err := validateARN("FirewallPolicyArn", policyARN); err != nil {
			return err
		}

		if !m.policyExists(policyARN) {
			return invalid("firewall policy %q not found", policyARN)
		}

		fw.PolicyARN = policyARN

		return nil
	})
}

// AssociateSubnets adds subnet mappings to a VPC-attached firewall.
func (m *Mock) AssociateSubnets(
	_ context.Context,
	firewallName string,
	subnetIDs []string,
	updateToken string,
) (*nfdriver.Firewall, error) {
	return m.mutateFirewall(firewallName, updateToken, func(fw *nfdriver.Firewall) error {
		if err := subnetOpAllowed(fw); err != nil {
			return err
		}

		if len(subnetIDs) == 0 {
			return invalid("SubnetMappings is required")
		}

		seen := make(map[string]bool, len(fw.SubnetIDs))
		for _, s := range fw.SubnetIDs {
			seen[s] = true
		}

		for _, s := range subnetIDs {
			if !seen[s] {
				fw.SubnetIDs = append(fw.SubnetIDs, s)
				seen[s] = true
			}
		}

		return nil
	})
}

// DisassociateSubnets removes subnet mappings from a VPC-attached firewall.
func (m *Mock) DisassociateSubnets(
	_ context.Context,
	firewallName string,
	subnetIDs []string,
	updateToken string,
) (*nfdriver.Firewall, error) {
	return m.mutateFirewall(firewallName, updateToken, func(fw *nfdriver.Firewall) error {
		if err := subnetOpAllowed(fw); err != nil {
			return err
		}

		if len(subnetIDs) == 0 {
			return invalid("SubnetIds is required")
		}

		remove := make(map[string]bool, len(subnetIDs))
		for _, s := range subnetIDs {
			remove[s] = true
		}

		kept := fw.SubnetIDs[:0:0]

		for _, s := range fw.SubnetIDs {
			if !remove[s] {
				kept = append(kept, s)
			}
		}

		fw.SubnetIDs = kept

		return nil
	})
}

// subnetOpAllowed gates the subnet associations: a transit gateway-attached
// firewall has no subnets, and a protected firewall refuses changes.
func subnetOpAllowed(fw *nfdriver.Firewall) error {
	if fw.TransitGatewayID != "" {
		return nfdriver.InvalidOperation("firewall %q is transit gateway-attached; use the Availability Zone operations", fw.Name)
	}

	if fw.SubnetChangeProtection {
		return nfdriver.InvalidOperation("firewall %q has subnet change protection enabled", fw.Name)
	}

	return nil
}

// UpdateFirewallDeleteProtection toggles delete protection on a firewall.
func (m *Mock) UpdateFirewallDeleteProtection(
	_ context.Context,
	firewallName string,
	enabled bool,
	updateToken string,
) (*nfdriver.Firewall, error) {
	return m.mutateFirewall(firewallName, updateToken, func(fw *nfdriver.Firewall) error {
		fw.DeleteProtection = enabled

		return nil
	})
}

// UpdateFirewallDescription sets (or, when empty, clears) the description.
func (m *Mock) UpdateFirewallDescription(_ context.Context, firewallName, description, updateToken string) (*nfdriver.Firewall, error) {
	return m.mutateFirewall(firewallName, updateToken, func(fw *nfdriver.Firewall) error {
		if err := validateDescription(description); err != nil {
			return err
		}

		fw.Description = description

		return nil
	})
}

// UpdateSubnetChangeProtection toggles the subnet change protection flag.
func (m *Mock) UpdateSubnetChangeProtection(
	_ context.Context,
	firewallName string,
	enabled bool,
	updateToken string,
) (*nfdriver.Firewall, error) {
	return m.mutateFirewall(firewallName, updateToken, func(fw *nfdriver.Firewall) error {
		fw.SubnetChangeProtection = enabled

		return nil
	})
}

// UpdateFirewallPolicyChangeProtection toggles the policy change protection flag.
func (m *Mock) UpdateFirewallPolicyChangeProtection(
	_ context.Context,
	firewallName string,
	enabled bool,
	updateToken string,
) (*nfdriver.Firewall, error) {
	return m.mutateFirewall(firewallName, updateToken, func(fw *nfdriver.Firewall) error {
		fw.FirewallPolicyChangeProtection = enabled

		return nil
	})
}

// UpdateAvailabilityZoneChangeProtection toggles the AZ change protection flag.
func (m *Mock) UpdateAvailabilityZoneChangeProtection(
	_ context.Context,
	firewallName string,
	enabled bool,
	updateToken string,
) (*nfdriver.Firewall, error) {
	return m.mutateFirewall(firewallName, updateToken, func(fw *nfdriver.Firewall) error {
		fw.AvailabilityZoneChangeProtection = enabled

		return nil
	})
}

// UpdateFirewallAnalysisSettings sets the analysis types enabled on a firewall
// (an empty list disables analysis).
func (m *Mock) UpdateFirewallAnalysisSettings(
	_ context.Context,
	firewallName string,
	types []string,
	updateToken string,
) (*nfdriver.Firewall, error) {
	return m.mutateFirewall(firewallName, updateToken, func(fw *nfdriver.Firewall) error {
		for _, t := range types {
			if !oneOf(t, analysisTypes...) {
				return invalid("EnabledAnalysisTypes value %q is not valid", t)
			}
		}

		fw.EnabledAnalysisTypes = cloneStrings(types)

		return nil
	})
}

// AssociateAvailabilityZones adds Availability Zones to a transit
// gateway-attached firewall.
func (m *Mock) AssociateAvailabilityZones(
	_ context.Context,
	firewallName string,
	zones []string,
	updateToken string,
) (*nfdriver.Firewall, error) {
	return m.mutateFirewall(firewallName, updateToken, func(fw *nfdriver.Firewall) error {
		if err := zoneOpAllowed(fw, zones); err != nil {
			return err
		}

		seen := make(map[string]bool, len(fw.AvailabilityZones))
		for _, z := range fw.AvailabilityZones {
			seen[z] = true
		}

		for _, z := range zones {
			if !seen[z] {
				fw.AvailabilityZones = append(fw.AvailabilityZones, z)
				seen[z] = true
			}
		}

		return nil
	})
}

// DisassociateAvailabilityZones removes Availability Zones from a transit
// gateway-attached firewall.
func (m *Mock) DisassociateAvailabilityZones(
	_ context.Context,
	firewallName string,
	zones []string,
	updateToken string,
) (*nfdriver.Firewall, error) {
	return m.mutateFirewall(firewallName, updateToken, func(fw *nfdriver.Firewall) error {
		if err := zoneOpAllowed(fw, zones); err != nil {
			return err
		}

		remove := make(map[string]bool, len(zones))

		for _, z := range zones {
			if !oneOf(z, fw.AvailabilityZones...) {
				return invalid("Availability Zone %q is not associated with firewall %q", z, fw.Name)
			}

			remove[z] = true
		}

		kept := fw.AvailabilityZones[:0:0]

		for _, z := range fw.AvailabilityZones {
			if !remove[z] {
				kept = append(kept, z)
			}
		}

		fw.AvailabilityZones = kept

		return nil
	})
}

func zoneOpAllowed(fw *nfdriver.Firewall, zones []string) error {
	if fw.TransitGatewayID == "" {
		return nfdriver.InvalidOperation("firewall %q is VPC-attached; use the subnet operations", fw.Name)
	}

	if fw.AvailabilityZoneChangeProtection {
		return nfdriver.InvalidOperation("firewall %q has Availability Zone change protection enabled", fw.Name)
	}

	if len(zones) == 0 {
		return invalid("AvailabilityZoneMappings is required")
	}

	for _, z := range zones {
		if z == "" {
			return invalid("AvailabilityZone must not be empty")
		}
	}

	return nil
}
