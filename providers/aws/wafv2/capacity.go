package wafv2

import (
	"context"
	"encoding/json"
)

// Web ACL Capacity Units (WCU) heuristics. Real WAF derives WCU from the exact
// statement types in each rule; the emulator does not model the full WCU cost
// table, so it computes a deterministic, self-consistent estimate: a base cost
// per rule plus a per-statement increment for rate-based/managed statements
// that WAF charges more for. This gives stable, plausible values for tooling
// that reads CheckCapacity without pretending to be the authoritative table.
const (
	wcuPerRule           = 1
	wcuRateBasedStmt     = 2
	wcuManagedGroupStmt  = 10
	wcuMinReported       = 1
	wcuEmptyRulesReports = 0
)

// checkCapacityRule is the minimal shape of a Rule needed to estimate WCU.
type checkCapacityRule struct {
	Statement json.RawMessage `json:"Statement"`
}

// CheckCapacity estimates the WCU capacity required by a set of rules in a
// scope. See the WCU heuristic constants for the (documented, non-authoritative)
// cost model. An empty rule set reports zero capacity.
func (*Mock) CheckCapacity(_ context.Context, scope string, rules json.RawMessage) (int64, error) {
	if scope == "" {
		return 0, invalidParameter("Scope is required")
	}

	if len(rules) == 0 {
		return wcuEmptyRulesReports, nil
	}

	var parsed []checkCapacityRule
	if err := json.Unmarshal(rules, &parsed); err != nil {
		return 0, invalidParameter("Rules must be a JSON array: %v", err)
	}

	var total int64
	for i := range parsed {
		total += wcuPerRule + statementCost(parsed[i].Statement)
	}

	if total < wcuMinReported && len(parsed) > 0 {
		total = wcuMinReported
	}

	return total, nil
}

// statementCost adds extra WCU for statement kinds WAF charges more for.
func statementCost(stmt json.RawMessage) int64 {
	if len(stmt) == 0 {
		return 0
	}

	var probe map[string]json.RawMessage
	if err := json.Unmarshal(stmt, &probe); err != nil {
		return 0
	}

	var extra int64

	if _, ok := probe["RateBasedStatement"]; ok {
		extra += wcuRateBasedStmt
	}

	if _, ok := probe["ManagedRuleGroupStatement"]; ok {
		extra += wcuManagedGroupStmt
	}

	return extra
}
