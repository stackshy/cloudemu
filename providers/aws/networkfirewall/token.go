package networkfirewall

import nfdriver "github.com/stackshy/cloudemu/v2/services/networkfirewall/driver"

// checkToken compares the update token a caller sent with the resource's
// current one. An empty token is an unconditional update (allowed only where
// AWS marks the token optional; use requireToken where it is required). A stale
// token fails with InvalidTokenException, as in real Network Firewall.
func checkToken(current, supplied string) error {
	if supplied != "" && supplied != current {
		return nfdriver.InvalidToken("the update token is stale; describe the resource again to get a current token")
	}

	return nil
}

// requireToken fails with a request error when a token AWS marks required is
// missing (UpdateFirewallPolicy, UpdateRuleGroup, UpdateTLSInspectionConfiguration).
func requireToken(supplied string) error {
	if supplied == "" {
		return invalid("UpdateToken is required")
	}

	return nil
}
