package ssm

import (
	"strings"

	ssmdriver "github.com/stackshy/cloudemu/v2/services/parameterstore/driver"
)

// DescribeParameters filter keys and options.
const (
	keyName = "Name"

	optionEquals     = "Equals"
	optionBeginsWith = "BeginsWith"
	optionContains   = "Contains"
)

// matchesParameterFilters reports whether md satisfies every filter — the
// modern ParameterFilters ({Key, Option, Values}) and the legacy Filters
// ({Key, Values}). Values within one filter are OR'd; filters are AND'd. An
// unsupported filter key is ignored (pass-through) rather than excluding
// everything, so callers using keys cloudemu doesn't model still get results.
func matchesParameterFilters(
	md *ssmdriver.ParameterMetadata,
	filters []parameterStringFilter,
	legacy []parametersFilter,
) bool {
	for _, f := range filters {
		if !matchStringFilter(md, f) {
			return false
		}
	}

	for _, f := range legacy {
		// The legacy Filters shape has no Option; Name uses BeginsWith, other
		// keys use Equals, matching real SSM.
		opt := optionEquals
		if f.Key == keyName {
			opt = optionBeginsWith
		}

		if !matchStringFilter(md, parameterStringFilter{Key: f.Key, Option: opt, Values: f.Values}) {
			return false
		}
	}

	return true
}

func matchStringFilter(md *ssmdriver.ParameterMetadata, f parameterStringFilter) bool {
	field, ok := parameterFilterField(md, f.Key)
	if !ok {
		// Unsupported key: do not filter on it.
		return true
	}

	option := f.Option
	if option == "" {
		option = optionEquals
	}

	for _, v := range f.Values {
		if matchValue(field, option, v) {
			return true
		}
	}

	return len(f.Values) == 0
}

func parameterFilterField(md *ssmdriver.ParameterMetadata, key string) (string, bool) {
	switch key {
	case keyName:
		return md.Name, true
	case "Type":
		return md.Type, true
	case "Tier":
		return md.Tier, true
	case "DataType":
		return md.DataType, true
	default:
		return "", false
	}
}

func matchValue(field, option, value string) bool {
	switch option {
	case optionBeginsWith:
		return strings.HasPrefix(field, value)
	case optionContains:
		return strings.Contains(field, value)
	default: // Equals
		return field == value
	}
}
