package asl

// validateState performs the structural checks real Step Functions applies at
// CreateStateMachine time for the given state.
func validateState(states map[string]*State, st *State) error {
	if st.Type == "" {
		return aslErrf("state %q is missing the required 'Type' field", st.name)
	}

	if !knownType(st.Type) {
		return aslErrf("state %q has unknown Type %q", st.name, st.Type)
	}

	if st.QueryLanguage == queryLangJSONata {
		return aslErrf("state %q QueryLanguage %q is not supported (JSONPath only)", st.name, st.QueryLanguage)
	}

	if err := validateFieldApplicability(st); err != nil {
		return err
	}

	if err := validateRetriers(st); err != nil {
		return err
	}

	if st.Type == TypeChoice {
		return validateChoice(states, st)
	}

	if err := validateSubMachines(st); err != nil {
		return err
	}

	return validateTransitions(states, st)
}

// validateSubMachines validates the nested state machines a Parallel (Branches)
// or Map (ItemProcessor/Iterator) state carries, mirroring the create-time
// structural checks AWS applies to the top-level definition.
func validateSubMachines(st *State) error {
	switch st.Type {
	case TypeParallel:
		if len(st.Branches) == 0 {
			return aslErrf("Parallel state %q must have a non-empty 'Branches' array", st.name)
		}

		for i, br := range st.Branches {
			if err := validateStates(br); err != nil {
				return aslErrf("Parallel state %q branch %d: %s", st.name, i, err.Error())
			}
		}
	case TypeMap:
		proc := st.processor()
		if proc == nil {
			return aslErrf("Map state %q must have an 'ItemProcessor' (or 'Iterator')", st.name)
		}

		if err := validateStates(proc); err != nil {
			return aslErrf("Map state %q processor: %s", st.name, err.Error())
		}
	}

	return nil
}

// validateRetriers rejects out-of-range Retrier fields at create time:
// IntervalSeconds and MaxAttempts must be >= 0, and BackoffRate must be >= 1.0.
func validateRetriers(st *State) error {
	for i, r := range st.Retry {
		switch {
		case r.IntervalSeconds != nil && *r.IntervalSeconds < 0:
			return aslErrf("state %q Retry[%d] 'IntervalSeconds' must be >= 0", st.name, i)
		case r.MaxAttempts != nil && *r.MaxAttempts < 0:
			return aslErrf("state %q Retry[%d] 'MaxAttempts' must be >= 0", st.name, i)
		case r.BackoffRate != nil && *r.BackoffRate < minBackoffRate:
			return aslErrf("state %q Retry[%d] 'BackoffRate' must be >= 1.0", st.name, i)
		}
	}

	return nil
}

// validateFieldApplicability rejects result-shaping fields on the state types
// that do not support them (Wait, Choice, Succeed, Fail), matching AWS's
// create-time rejection.
func validateFieldApplicability(st *State) error {
	switch st.Type {
	case TypePass:
		return validatePassFields(st)
	case TypeWait, TypeChoice, TypeSucceed, TypeFail:
		return validateNonResultFields(st)
	default:
		return nil
	}
}

// validatePassFields rejects the result-shaping fields a Pass state does not
// support: ResultSelector, Retry, and Catch (Task/Parallel/Map only).
func validatePassFields(st *State) error {
	switch {
	case st.ResultSelector != nil:
		return aslErrf("state %q (%s) does not support the 'ResultSelector' field", st.name, st.Type)
	case len(st.Retry) > 0:
		return aslErrf("state %q (%s) does not support the 'Retry' field", st.name, st.Type)
	case len(st.Catch) > 0:
		return aslErrf("state %q (%s) does not support the 'Catch' field", st.name, st.Type)
	}

	return nil
}

// validateNonResultFields rejects every result-shaping field on the state types
// (Wait, Choice, Succeed, Fail) that support none of them.
func validateNonResultFields(st *State) error {
	switch {
	case st.Parameters != nil:
		return aslErrf("state %q (%s) does not support the 'Parameters' field", st.name, st.Type)
	case st.Result != nil:
		return aslErrf("state %q (%s) does not support the 'Result' field", st.name, st.Type)
	case st.ResultSelector != nil:
		return aslErrf("state %q (%s) does not support the 'ResultSelector' field", st.name, st.Type)
	case st.ResultPath.set:
		return aslErrf("state %q (%s) does not support the 'ResultPath' field", st.name, st.Type)
	case len(st.Retry) > 0:
		return aslErrf("state %q (%s) does not support the 'Retry' field", st.name, st.Type)
	case len(st.Catch) > 0:
		return aslErrf("state %q (%s) does not support the 'Catch' field", st.name, st.Type)
	}

	return nil
}

// validateTransitions checks Next/End for a non-Choice state: terminal states
// (Succeed, Fail) take neither; every other state needs exactly one of Next or
// End, and a Next must name an existing state.
func validateTransitions(states map[string]*State, st *State) error {
	if st.Type == TypeSucceed || st.Type == TypeFail {
		return nil
	}

	if st.Next == "" && !st.End {
		return aslErrf("state %q must specify either 'Next' or 'End'", st.name)
	}

	if st.Next != "" && st.End {
		return aslErrf("state %q specifies both 'Next' and 'End'", st.name)
	}

	if st.Next != "" {
		if _, ok := states[st.Next]; !ok {
			return aslErrf("state %q Next %q does not name a state", st.name, st.Next)
		}
	}

	return nil
}

// validateChoice checks a Choice state has at least one rule, every rule names
// an existing Next target, and any Default names an existing state.
func validateChoice(states map[string]*State, st *State) error {
	if len(st.Choices) == 0 {
		return aslErrf("Choice state %q must have a non-empty 'Choices' array", st.name)
	}

	for i, rule := range st.Choices {
		if err := validateRule(states, st.name, i, rule); err != nil {
			return err
		}
	}

	if st.Default != "" {
		if _, ok := states[st.Default]; !ok {
			return aslErrf("Choice state %q Default %q does not name a state", st.name, st.Default)
		}
	}

	return nil
}

// validateRule checks a single choice rule: a top-level rule must carry Next to
// an existing state, and must be either a leaf comparator or a logical (And/Or/
// Not) combinator — never both, never neither.
func validateRule(states map[string]*State, stateName string, idx int, rule *ChoiceRule) error {
	if rule.Next == "" {
		return aslErrf("Choice state %q rule %d is missing 'Next'", stateName, idx)
	}

	if _, ok := states[rule.Next]; !ok {
		return aslErrf("Choice state %q rule %d Next %q does not name a state", stateName, idx, rule.Next)
	}

	return validateRuleShape(stateName, idx, rule)
}

// validateRuleShape checks the comparator/combinator well-formedness of a rule
// (and its nested logical rules), independent of the top-level Next.
func validateRuleShape(stateName string, idx int, rule *ChoiceRule) error {
	logical := len(rule.And) > 0 || len(rule.Or) > 0 || rule.Not != nil
	leaf := rule.Variable != "" || len(rule.ops) > 0

	if logical && leaf {
		return aslErrf("Choice state %q rule %d mixes a comparator with And/Or/Not", stateName, idx)
	}

	if !logical && !leaf {
		return aslErrf("Choice state %q rule %d has no comparator or And/Or/Not", stateName, idx)
	}

	if leaf {
		return validateLeaf(stateName, idx, rule)
	}

	return validateNestedRules(stateName, idx, rule)
}

// validateNestedRules validates the sub-rules of a logical (And/Or/Not) rule.
func validateNestedRules(stateName string, idx int, rule *ChoiceRule) error {
	for _, sub := range rule.And {
		if err := validateRuleShape(stateName, idx, sub); err != nil {
			return err
		}
	}

	for _, sub := range rule.Or {
		if err := validateRuleShape(stateName, idx, sub); err != nil {
			return err
		}
	}

	if rule.Not != nil {
		return validateRuleShape(stateName, idx, rule.Not)
	}

	return nil
}

// validateLeaf checks a leaf comparator carries a Variable and exactly one known
// operator, and that a ...Path operator is only used where a Path variant exists.
func validateLeaf(stateName string, idx int, rule *ChoiceRule) error {
	if rule.Variable == "" {
		return aslErrf("Choice state %q rule %d comparator is missing 'Variable'", stateName, idx)
	}

	if len(rule.ops) != 1 {
		return aslErrf("Choice state %q rule %d must have exactly one comparator operator", stateName, idx)
	}

	for op := range rule.ops {
		if comparatorFamily(op) == "" {
			return aslErrf("Choice state %q rule %d has unknown comparator %q", stateName, idx, op)
		}
	}

	return nil
}
