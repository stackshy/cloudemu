package cloudformation

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/yamlconv"
)

// Error texts CloudFormation returns for template format problems.
const (
	formatErrPrefix = "Template format error: "
	// MsgNoTemplate is returned when neither a body nor a URL is given.
	MsgNoTemplate = "Either Template URL or Template Body must be specified."
	msgYAMLAlias  = "Template error: YAML aliases are not allowed in CloudFormation templates"
)

// shortForms maps each YAML short-form tag to its long-form key. !GetAtt
// keeps its "Logical.Attr" string, which the resolver splits.
var shortForms = map[string]string{ //nolint:gochecknoglobals // static lookup table
	"!Ref":         fnRef,
	"!Condition":   "Condition",
	"!GetAtt":      fnGetAtt,
	"!Sub":         fnSub,
	"!Join":        fnJoin,
	"!Base64":      "Fn::Base64",
	"!Cidr":        "Fn::Cidr",
	"!FindInMap":   "Fn::FindInMap",
	"!GetAZs":      "Fn::GetAZs",
	"!ImportValue": "Fn::ImportValue",
	"!Select":      "Fn::Select",
	"!Split":       "Fn::Split",
	"!Transform":   "Fn::Transform",
	"!And":         "Fn::And",
	"!Equals":      "Fn::Equals",
	"!If":          "Fn::If",
	"!Not":         "Fn::Not",
	"!Or":          "Fn::Or",
}

// expandShortForm is the yamlconv tag hook for CloudFormation templates.
func expandShortForm(tag string, value any) (any, bool) {
	key, ok := shortForms[tag]
	if !ok {
		return nil, false
	}

	return map[string]any{key: value}, true
}

// decodeTemplate turns a JSON or YAML body into a generic tree.
func decodeTemplate(body string) (any, error) {
	if strings.HasPrefix(strings.TrimSpace(body), "{") {
		return decodeJSON(body)
	}

	tree, err := yamlconv.Decode([]byte(body), expandShortForm)
	if err != nil {
		return nil, yamlFormatError(err)
	}

	return tree, nil
}

func yamlFormatError(err error) error {
	var ye *yamlconv.Error
	if !errors.As(err, &ye) {
		return cerrors.New(cerrors.InvalidArgument, formatErrPrefix+"YAML not well-formed.")
	}

	if ye.Msg == yamlconv.MsgAlias {
		return cerrors.New(cerrors.InvalidArgument, msgYAMLAlias)
	}

	return cerrors.New(cerrors.InvalidArgument, formatErrPrefix+"YAML not well-formed."+position(ye.Line, ye.Column))
}

func decodeJSON(body string) (any, error) {
	dec := json.NewDecoder(strings.NewReader(body))
	dec.UseNumber()

	var tree any
	if err := dec.Decode(&tree); err != nil {
		return nil, jsonFormatError(body, err, dec.InputOffset())
	}

	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, jsonFormatError(body, nil, dec.InputOffset())
	}

	return tree, nil
}

// jsonFormatError reports a JSON syntax error with its line and column.
// offset counts the bytes read up to and including the bad one.
func jsonFormatError(body string, err error, fallback int64) error {
	offset := fallback

	var se *json.SyntaxError
	if errors.As(err, &se) {
		offset = se.Offset
	}

	line, col := lineColumn(body, offset-1)

	return cerrors.New(cerrors.InvalidArgument, formatErrPrefix+"JSON not well-formed."+position(line, col))
}

// lineColumn converts a 0-based byte index into a 1-based line and column.
func lineColumn(body string, idx int64) (line, col int) {
	idx = max(0, min(idx, int64(len(body))))

	prefix := []byte(body[:idx])
	line = bytes.Count(prefix, []byte("\n")) + 1
	col = len(prefix) - bytes.LastIndexByte(prefix, '\n')

	return line, col
}

// position renders " (line L, column C)", or less when a part is unknown.
func position(line, col int) string {
	switch {
	case line > 0 && col > 0:
		return fmt.Sprintf(" (line %d, column %d)", line, col)
	case line > 0:
		return fmt.Sprintf(" (line %d)", line)
	default:
		return ""
	}
}
