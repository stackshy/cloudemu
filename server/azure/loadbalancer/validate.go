package loadbalancer

import (
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	lbdriver "github.com/stackshy/cloudemu/v2/services/loadbalancer/driver"
)

// validateAzureLB checks a whole-LB PUT body for two classes of error real
// ARM rejects with 400 before ever storing the load balancer:
//
//   - duplicate names within one child collection (two backend pools named
//     "pool-a" in the same PUT), and
//   - a rule/pool/NAT-rule referencing a frontend, backend pool or probe that
//     is not present anywhere in the same body.
//
// Cross-references are resolved only against siblings in the same PUT body —
// a full-replace PUT that drops a pool while a rule still points at it is
// exactly the dangling-reference case this rejects.
func validateAzureLB(lb *lbdriver.AzureLoadBalancer) error {
	if err := checkUnique("frontend IP configuration", frontendNames(lb.Frontends)); err != nil {
		return err
	}

	if err := checkUnique("backend address pool", lb.BackendPools); err != nil {
		return err
	}

	if err := checkUnique("probe", probeNames(lb.Probes)); err != nil {
		return err
	}

	if err := checkUnique("load balancing rule", ruleNames(lb.Rules)); err != nil {
		return err
	}

	if err := checkUnique("inbound NAT rule", natRuleNames(lb.NatRules)); err != nil {
		return err
	}

	if err := checkUnique("inbound NAT pool", natPoolNames(lb.NatPools)); err != nil {
		return err
	}

	if err := checkUnique("outbound rule", outboundRuleNames(lb.OutboundRules)); err != nil {
		return err
	}

	return validateAzureLBRefs(lb)
}

// refSets is the set of sibling names a whole-LB PUT body's children may
// reference, resolved once and reused across every collection.
type refSets struct {
	frontends map[string]struct{}
	pools     map[string]struct{}
	probes    map[string]struct{}
}

// validateAzureLBRefs checks that every frontend/backend-pool/probe reference
// on a rule, NAT rule, NAT pool or outbound rule resolves to a sibling
// present in the same load balancer.
func validateAzureLBRefs(lb *lbdriver.AzureLoadBalancer) error {
	refs := refSets{
		frontends: toSet(frontendNames(lb.Frontends)),
		pools:     toSet(lb.BackendPools),
		probes:    toSet(probeNames(lb.Probes)),
	}

	if err := validateRuleRefs(lb.Rules, refs); err != nil {
		return err
	}

	if err := validateNatRuleRefs(lb.NatRules, refs); err != nil {
		return err
	}

	if err := validateNatPoolRefs(lb.NatPools, refs); err != nil {
		return err
	}

	return validateOutboundRuleRefs(lb.OutboundRules, refs)
}

func validateRuleRefs(rules []lbdriver.AzureLBRule, refs refSets) error {
	for i := range rules {
		r := &rules[i]
		if err := requireRef("load balancing rule", r.Name, "frontend IP configuration", r.FrontendName, refs.frontends); err != nil {
			return err
		}

		if err := requireRef("load balancing rule", r.Name, "backend address pool", r.BackendPoolName, refs.pools); err != nil {
			return err
		}

		if err := requireRef("load balancing rule", r.Name, "probe", r.ProbeName, refs.probes); err != nil {
			return err
		}
	}

	return nil
}

func validateNatRuleRefs(rules []lbdriver.AzureLBNatRule, refs refSets) error {
	for i := range rules {
		nr := &rules[i]
		if err := requireRef("inbound NAT rule", nr.Name, "frontend IP configuration", nr.FrontendName, refs.frontends); err != nil {
			return err
		}
	}

	return nil
}

func validateNatPoolRefs(pools []lbdriver.AzureLBNatPool, refs refSets) error {
	for i := range pools {
		np := &pools[i]
		if err := requireRef("inbound NAT pool", np.Name, "frontend IP configuration", np.FrontendName, refs.frontends); err != nil {
			return err
		}
	}

	return nil
}

func validateOutboundRuleRefs(rules []lbdriver.AzureLBOutboundRule, refs refSets) error {
	for i := range rules {
		or := &rules[i]
		if err := requireRef("outbound rule", or.Name, "backend address pool", or.BackendPoolName, refs.pools); err != nil {
			return err
		}

		for _, fe := range or.FrontendNames {
			if err := requireRef("outbound rule", or.Name, "frontend IP configuration", fe, refs.frontends); err != nil {
				return err
			}
		}
	}

	return nil
}

// requireRef returns an InvalidArgument error when ref is set but absent from
// available. An empty ref means the field was omitted and is not checked.
func requireRef(childKind, childName, refKind, ref string, available map[string]struct{}) error {
	if ref == "" {
		return nil
	}

	if _, ok := available[ref]; ok {
		return nil
	}

	return cerrors.Newf(cerrors.InvalidArgument,
		"%s %q references %s %q that does not exist on this load balancer", childKind, childName, refKind, ref)
}

// checkUnique returns a Conflict error if any name in names appears more than
// once. Empty names are ignored — an unnamed child is rejected by the wire
// decoder elsewhere, not here.
func checkUnique(childKind string, names []string) error {
	seen := make(map[string]struct{}, len(names))

	for _, name := range names {
		if name == "" {
			continue
		}

		if _, dup := seen[name]; dup {
			return cerrors.Newf(cerrors.InvalidArgument, "duplicate %s name %q in request", childKind, name)
		}

		seen[name] = struct{}{}
	}

	return nil
}

// toSet converts names to a lookup set, skipping empty entries.
func toSet(names []string) map[string]struct{} {
	out := make(map[string]struct{}, len(names))

	for _, name := range names {
		if name != "" {
			out[name] = struct{}{}
		}
	}

	return out
}

func frontendNames(in []lbdriver.AzureLBFrontend) []string {
	out := make([]string, len(in))
	for i := range in {
		out[i] = in[i].Name
	}

	return out
}

func probeNames(in []lbdriver.AzureLBProbe) []string {
	out := make([]string, len(in))
	for i := range in {
		out[i] = in[i].Name
	}

	return out
}

func ruleNames(in []lbdriver.AzureLBRule) []string {
	out := make([]string, len(in))
	for i := range in {
		out[i] = in[i].Name
	}

	return out
}

func natRuleNames(in []lbdriver.AzureLBNatRule) []string {
	out := make([]string, len(in))
	for i := range in {
		out[i] = in[i].Name
	}

	return out
}

func natPoolNames(in []lbdriver.AzureLBNatPool) []string {
	out := make([]string, len(in))
	for i := range in {
		out[i] = in[i].Name
	}

	return out
}

func outboundRuleNames(in []lbdriver.AzureLBOutboundRule) []string {
	out := make([]string, len(in))
	for i := range in {
		out[i] = in[i].Name
	}

	return out
}
