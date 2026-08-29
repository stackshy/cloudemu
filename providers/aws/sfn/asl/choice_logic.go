package asl

// evalAll reports whether every sub-rule matches (And).
func (it *interp) evalAll(rules []*ChoiceRule, input any) (bool, error) {
	for _, r := range rules {
		m, err := it.evalRule(r, input)
		if err != nil {
			return false, err
		}

		if !m {
			return false, nil
		}
	}

	return true, nil
}

// evalAny reports whether any sub-rule matches (Or).
func (it *interp) evalAny(rules []*ChoiceRule, input any) (bool, error) {
	for _, r := range rules {
		m, err := it.evalRule(r, input)
		if err != nil {
			return false, err
		}

		if m {
			return true, nil
		}
	}

	return false, nil
}
