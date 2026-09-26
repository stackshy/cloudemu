package cloudformation

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// Parameter types with special handling.
const (
	typeString    = "String"
	typeNumber    = "Number"
	typeNumList   = "List<Number>"
	typeCommaList = "CommaDelimitedList"
	ssmValuePre   = "AWS::SSM::Parameter::Value<"
)

// validParamType reports whether CloudFormation accepts a parameter type. Any
// AWS-specific type (AWS::..., List<AWS::...>) is accepted as a string.
func validParamType(typ string) bool {
	switch typ {
	case typeString, typeNumber, typeNumList, typeCommaList:
		return true
	}

	if inner, ok := SSMValueType(typ); ok {
		return inner != ""
	}

	if strings.HasPrefix(typ, "List<AWS::") && strings.HasSuffix(typ, ">") {
		return true
	}

	return strings.HasPrefix(typ, "AWS::")
}

// SSMValueType reports whether typ is AWS::SSM::Parameter::Value<T> and
// returns T. The value of such a parameter is a Parameter Store name.
func SSMValueType(typ string) (inner string, ok bool) {
	rest, found := strings.CutPrefix(typ, ssmValuePre)
	if !found || !strings.HasSuffix(rest, ">") {
		return "", false
	}

	return strings.TrimSuffix(rest, ">"), true
}

// IsListType reports whether a parameter of this type holds a list, which Ref
// returns as a list of strings.
func IsListType(typ string) bool {
	if inner, ok := SSMValueType(typ); ok {
		typ = inner
	}

	return typ == typeCommaList || strings.HasPrefix(typ, "List<")
}

// splitList splits a comma separated parameter value the way CloudFormation
// does, trimming the spaces around each item.
func splitList(v string) []string {
	parts := strings.Split(v, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}

	return parts
}

// CheckParameterValue checks a parameter value against its declared type and
// constraints. List types check each item. For an SSM type the value checked
// is the Parameter Store name.
func CheckParameterValue(name string, def *ParameterDef, value string) error {
	typ := def.Type
	items := []string{value}

	switch {
	case isSSM(typ):
		typ = typeString
	case IsListType(typ):
		items = splitList(value)
	}

	for _, v := range items {
		if err := checkItem(name, def, typ, v); err != nil {
			return err
		}
	}

	return nil
}

func isSSM(typ string) bool {
	_, ok := SSMValueType(typ)
	return ok
}

// checkItem checks one value. The error texts are the ones CloudFormation
// returns. Only the pattern text was measured against AWS. The length and
// range texts follow the same form.
func checkItem(name string, def *ParameterDef, typ, v string) error {
	if typ == typeNumber || typ == typeNumList {
		return checkNumber(name, def, v)
	}

	if len(def.AllowedValues) > 0 && !allowedValue(def.AllowedValues, v) {
		return constraintErr(name, def, "must be one of AllowedValues")
	}

	if def.AllowedPattern != "" && !fullMatch(def.AllowedPattern, v) {
		return constraintErr(name, def, "must match pattern "+def.AllowedPattern)
	}

	if typ != typeString {
		return nil
	}

	return checkLength(name, def, v)
}

func checkLength(name string, def *ParameterDef, v string) error {
	n := utf8.RuneCountInString(v)

	if def.MinLength != nil && n < *def.MinLength {
		return constraintErr(name, def, fmt.Sprintf("must contain at least %d characters", *def.MinLength))
	}

	if def.MaxLength != nil && n > *def.MaxLength {
		return constraintErr(name, def, fmt.Sprintf("must contain at most %d characters", *def.MaxLength))
	}

	return nil
}

func checkNumber(name string, def *ParameterDef, v string) error {
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil {
		return paramErr(name, "must be a number")
	}

	if len(def.AllowedValues) > 0 && !allowedValue(def.AllowedValues, v) {
		return constraintErr(name, def, "must be one of AllowedValues")
	}

	if def.MinValue != nil && f < *def.MinValue {
		return constraintErr(name, def, "must be a number not less than "+formatNumber(*def.MinValue))
	}

	if def.MaxValue != nil && f > *def.MaxValue {
		return constraintErr(name, def, "must be a number not greater than "+formatNumber(*def.MaxValue))
	}

	return nil
}

func allowedValue(allowed []any, v string) bool {
	for _, a := range allowed {
		if scalarString(a) == v {
			return true
		}
	}

	return false
}

// fullMatch reports whether pattern matches all of v. CloudFormation uses
// Java regular expressions. A pattern Go cannot compile is not enforced
// rather than rejecting a template AWS accepts.
func fullMatch(pattern, v string) bool {
	re, err := regexp.Compile(`^(?:` + pattern + `)$`)
	if err != nil {
		return true
	}

	return re.MatchString(v)
}

func formatNumber(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

func paramErr(name, reason string) error {
	return cerrors.Newf(cerrors.InvalidArgument, "Parameter '%s' %s", name, reason)
}

// constraintErr reports a failed constraint. A ConstraintDescription replaces
// the built-in reason.
func constraintErr(name string, def *ParameterDef, reason string) error {
	if def.ConstraintDescription != "" {
		return cerrors.Newf(cerrors.InvalidArgument, "Parameter '%s' failed to satisfy constraint: %s",
			name, def.ConstraintDescription)
	}

	return paramErr(name, reason)
}
