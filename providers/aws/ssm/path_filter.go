package ssm

import (
	"strings"

	"github.com/stackshy/cloudemu/v2/services/parameterstore/driver"
)

// GetParametersByPath filter keys and options. GetParametersByPath supports the
// Type, KeyId, and Label keys only (unlike DescribeParameters, which also
// supports Name/Tier/DataType/tag).
const (
	byPathKeyType  = "Type"
	byPathKeyKeyID = "KeyId"
	byPathKeyLabel = "Label"

	filterOptionEquals     = "Equals"
	filterOptionBeginsWith = "BeginsWith"
)

// validateByPathFilters checks each ParameterFilters entry uses a key and option
// GetParametersByPath supports, returning the AWS-distinct error otherwise.
func validateByPathFilters(filters []driver.ParameterStringFilter) error {
	for _, f := range filters {
		switch f.Key {
		case byPathKeyType, byPathKeyKeyID, byPathKeyLabel:
		default:
			return driver.ErrInvalidFilterKey
		}

		switch f.Option {
		case "", filterOptionEquals, filterOptionBeginsWith:
		default:
			return driver.ErrInvalidFilterOption
		}
	}

	return nil
}

// matchesByPathFilters reports whether version v of a parameter satisfies every
// filter (filters are AND'd; a filter's Values are OR'd).
func matchesByPathFilters(v *version, filters []driver.ParameterStringFilter) bool {
	for i := range filters {
		if !matchByPathFilter(v, &filters[i]) {
			return false
		}
	}

	return true
}

func matchByPathFilter(v *version, f *driver.ParameterStringFilter) bool {
	switch f.Key {
	case byPathKeyType:
		return fieldMatchesFilter(v.typ, f.Option, f.Values)
	case byPathKeyLabel:
		return labelsMatchFilter(v.labels, f.Option, f.Values)
	default:
		// KeyId is a valid key but cloudemu doesn't model a per-parameter KMS
		// KeyId (SecureString isn't encrypted), so it can't constrain results.
		return true
	}
}

// fieldMatchesFilter reports whether a single string field matches the option
// against any of the values. Empty Values matches (an empty filter is a no-op).
func fieldMatchesFilter(field, option string, values []string) bool {
	if len(values) == 0 {
		return true
	}

	for _, val := range values {
		if optionMatch(field, option, val) {
			return true
		}
	}

	return false
}

// labelsMatchFilter reports whether any of a version's labels matches the option
// against any of the values.
func labelsMatchFilter(labels []string, option string, values []string) bool {
	if len(values) == 0 {
		return true
	}

	for _, val := range values {
		for _, lbl := range labels {
			if optionMatch(lbl, option, val) {
				return true
			}
		}
	}

	return false
}

func optionMatch(field, option, val string) bool {
	if option == filterOptionBeginsWith {
		return strings.HasPrefix(field, val)
	}

	return field == val
}
