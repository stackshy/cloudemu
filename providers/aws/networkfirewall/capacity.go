package networkfirewall

import (
	"encoding/json"
	"strings"
)

// ruleSource is the part of a rule group payload the emulator reads to work out
// the capacity the rules consume (everything else is stored untouched).
type ruleSource struct {
	RulesSource struct {
		RulesString     string            `json:"RulesString"`
		StatefulRules   []json.RawMessage `json:"StatefulRules"`
		RulesSourceList struct {
			Targets []string `json:"Targets"`
		} `json:"RulesSourceList"`
		Stateless struct {
			Rules []struct {
				RuleDefinition struct {
					MatchAttributes map[string][]json.RawMessage `json:"MatchAttributes"`
				} `json:"RuleDefinition"`
			} `json:"StatelessRules"`
		} `json:"StatelessRulesAndCustomActions"`
	} `json:"RulesSource"`
}

// maxRulesStringLen is the AWS limit on a Suricata rules string.
const maxRulesStringLen = 2000000

// consumedCapacity validates a rule group payload and returns the capacity units its rules consume, per the AWS
// CreateRuleGroup reference: a stateful rule group needs one unit per rule; a
// stateless rule needs the product of the element counts of its match settings
// (an unset setting counts 1) and the group needs the sum over its rules.
func consumedCapacity(raw json.RawMessage) (int, error) {
	if len(raw) == 0 {
		return 0, nil
	}

	var src ruleSource
	if err := json.Unmarshal(raw, &src); err != nil {
		return 0, invalid("RuleGroup is not valid: %v", err)
	}

	if len(src.RulesSource.RulesString) > maxRulesStringLen {
		return 0, invalid("RulesString exceeds %d characters", maxRulesStringLen)
	}

	total := len(src.RulesSource.StatefulRules) + len(src.RulesSource.RulesSourceList.Targets)

	for _, line := range strings.Split(src.RulesSource.RulesString, "\n") {
		if isRuleLine(line) {
			total++
		}
	}

	for _, r := range src.RulesSource.Stateless.Rules {
		cost := 1

		for _, elems := range r.RuleDefinition.MatchAttributes {
			if len(elems) > 1 {
				cost *= len(elems)
			}
		}

		total += cost
	}

	return total, nil
}

// isRuleLine reports whether a Suricata rules-string line is a rule (not blank
// and not a comment).
func isRuleLine(line string) bool {
	t := strings.TrimSpace(line)

	return t != "" && !strings.HasPrefix(t, "#")
}
