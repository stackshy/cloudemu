package asl

import cerrors "github.com/stackshy/cloudemu/v2/errors"

// Query-language and payload literals shared across the parser and interpreter.
const (
	queryLangJSONata = "JSONata"
	emptyObject      = "{}"
	opStringMatches  = "StringMatches"
)

// aslErrf builds a definition/parse error carrying the canonical
// InvalidArgument code. Callers at the sfn layer surface it as an
// InvalidDefinition-shaped API error via err.Error().
func aslErrf(format string, args ...any) error {
	return cerrors.Newf(cerrors.InvalidArgument, format, args...)
}
