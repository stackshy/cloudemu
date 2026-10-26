// Package dataprotection parses CloudWatch Logs data protection policy
// documents and masks the sensitive data they name in log messages.
package dataprotection

import (
	"bytes"
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/stackshy/cloudemu/v2/errors"
)

// Policy document limits and fixed values, per the CloudWatch Logs docs.
const (
	MaxDocumentLength   = 30720
	policyVersion       = "2021-06-01"
	managedARNPrefix    = "arn:aws:dataprotection::aws:data-identifier/"
	maxCustomIDs        = 10
	maxCustomNameLength = 128
	maxCustomRegexLen   = 200
	statementCount      = 2
)

var (
	customNameRE    = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	managedNameRE   = regexp.MustCompile(`^[A-Za-z0-9-]+$`)
	customRegexChar = regexp.MustCompile(`^[A-Za-z0-9_#=@/;,\- ^$?\[\]{}|\\*+.()]+$`)
)

// Policy is a parsed, validated data protection policy.
type Policy struct {
	identifiers      []string
	findingsLogGroup string
	detectors        []detector
}

// FindingsLogGroup is the audit findings destination log group, or "".
func (p *Policy) FindingsLogGroup() string { return p.findingsLogGroup }

// Identifiers returns the policy's data identifiers in document order.
func (p *Policy) Identifiers() []string { return slices.Clone(p.identifiers) }

type document struct {
	Name          *string        `json:"Name"`
	Description   *string        `json:"Description"`
	Version       *string        `json:"Version"`
	Configuration *configuration `json:"Configuration"`
	Statement     []statement    `json:"Statement"`
}

type configuration struct {
	CustomDataIdentifier []customIdentifier `json:"CustomDataIdentifier"`
}

type customIdentifier struct {
	Name  string `json:"Name"`
	Regex string `json:"Regex"`
}

type statement struct {
	Sid            string                     `json:"Sid"`
	DataIdentifier []string                   `json:"DataIdentifier"`
	Operation      map[string]json.RawMessage `json:"Operation"`
}

type auditOperation struct {
	FindingsDestination *struct {
		CloudWatchLogs *struct {
			LogGroup string `json:"LogGroup"`
		} `json:"CloudWatchLogs"`
	} `json:"FindingsDestination"`
}

type deidentifyOperation struct {
	MaskConfig json.RawMessage `json:"MaskConfig"`
}

func invalid(format string, args ...any) error {
	return errors.Newf(errors.InvalidArgument, "Invalid data protection policy: "+format, args...)
}

// Parse validates a policy document: at most 30720 characters, Version
// 2021-06-01 when present, one Audit statement with a FindingsDestination and
// one Deidentify statement with an empty MaskConfig, both naming the same data
// identifiers. Identifiers are managed identifier ARNs or the names of custom
// identifiers declared under Configuration.
func Parse(doc string) (*Policy, error) {
	if utf8.RuneCountInString(doc) > MaxDocumentLength {
		return nil, errors.Newf(errors.InvalidArgument,
			"1 validation error detected: Value at 'policyDocument' failed to satisfy constraint: "+
				"Member must have length less than or equal to %d", MaxDocumentLength)
	}

	var d document

	dec := json.NewDecoder(strings.NewReader(doc))
	if err := dec.Decode(&d); err != nil || dec.More() {
		return nil, invalid("the policy document is not a valid JSON object")
	}

	if d.Version != nil && *d.Version != policyVersion {
		return nil, invalid("Version must be %s", policyVersion)
	}

	custom, err := parseCustom(d.Configuration)
	if err != nil {
		return nil, err
	}

	p := &Policy{}
	if err := p.parseStatements(d.Statement); err != nil {
		return nil, err
	}

	if err := p.resolveDetectors(custom); err != nil {
		return nil, err
	}

	return p, nil
}

// resolveDetectors builds the detectors for the policy's identifiers.
func (p *Policy) resolveDetectors(custom map[string]*regexp.Regexp) error {
	for _, id := range p.identifiers {
		det, err := resolveIdentifier(id, custom)
		if err != nil {
			return err
		}

		if det != nil {
			p.detectors = append(p.detectors, *det)
		}
	}

	p.appendIPv6()

	return nil
}

func (p *Policy) parseStatements(stmts []statement) error {
	if len(stmts) != statementCount {
		return invalid("the policy must have exactly two statements, one Audit and one Deidentify")
	}

	var audit, deidentify []string

	for i := range stmts {
		s := &stmts[i]

		op, err := parseStatement(s, i)
		if err != nil {
			return err
		}

		switch {
		case op.audit && audit == nil:
			audit = s.DataIdentifier
			p.findingsLogGroup = op.findingsLogGroup
		case !op.audit && deidentify == nil:
			deidentify = s.DataIdentifier
		default:
			return invalid("the policy must have exactly one Audit and one Deidentify statement")
		}
	}

	if !slices.Equal(audit, deidentify) {
		return invalid("the DataIdentifier arrays of the Audit and Deidentify statements must match exactly")
	}

	p.identifiers = audit

	return nil
}

// parseStatement checks one statement's shape and parses its operation.
func parseStatement(s *statement, i int) (parsedOperation, error) {
	if len(s.DataIdentifier) == 0 {
		return parsedOperation{}, invalid("statement %d must have a non-empty DataIdentifier array", i+1)
	}

	if len(s.Operation) != 1 {
		return parsedOperation{}, invalid("statement %d must have exactly one Operation, Audit or Deidentify", i+1)
	}

	return parseOperation(s.Operation)
}

type parsedOperation struct {
	audit            bool
	findingsLogGroup string
}

func parseOperation(ops map[string]json.RawMessage) (parsedOperation, error) {
	if raw, ok := ops["Audit"]; ok {
		var a auditOperation
		if err := json.Unmarshal(raw, &a); err != nil || a.FindingsDestination == nil {
			return parsedOperation{}, invalid("the Audit operation must contain a FindingsDestination object")
		}

		out := parsedOperation{audit: true}
		if cwl := a.FindingsDestination.CloudWatchLogs; cwl != nil {
			out.findingsLogGroup = cwl.LogGroup
		}

		return out, nil
	}

	raw, ok := ops["Deidentify"]
	if !ok {
		return parsedOperation{}, invalid("Operation must be Audit or Deidentify")
	}

	var d deidentifyOperation
	if err := json.Unmarshal(raw, &d); err != nil || d.MaskConfig == nil {
		return parsedOperation{}, invalid("the Deidentify operation must contain a MaskConfig object")
	}

	if !bytes.Equal(bytes.Join(bytes.Fields(d.MaskConfig), nil), []byte("{}")) {
		return parsedOperation{}, invalid("MaskConfig must be an empty object")
	}

	return parsedOperation{}, nil
}

func parseCustom(cfg *configuration) (map[string]*regexp.Regexp, error) {
	out := map[string]*regexp.Regexp{}
	if cfg == nil {
		return out, nil
	}

	if len(cfg.CustomDataIdentifier) > maxCustomIDs {
		return nil, invalid("a policy can have at most %d custom data identifiers", maxCustomIDs)
	}

	for _, c := range cfg.CustomDataIdentifier {
		re, err := compileCustom(c)
		if err != nil {
			return nil, err
		}

		if _, dup := out[c.Name]; dup {
			return nil, invalid("custom data identifier %q is defined more than once", c.Name)
		}

		out[c.Name] = re
	}

	return out, nil
}

func compileCustom(c customIdentifier) (*regexp.Regexp, error) {
	switch {
	case c.Name == "" || len(c.Name) > maxCustomNameLength || !customNameRE.MatchString(c.Name):
		return nil, invalid("custom data identifier name %q must be 1-%d characters of a-z, A-Z, 0-9, _ or -",
			c.Name, maxCustomNameLength)
	case isManagedName(c.Name):
		return nil, invalid("custom data identifier %q has the name of a managed data identifier", c.Name)
	case c.Regex == "" || len(c.Regex) > maxCustomRegexLen:
		return nil, invalid("custom data identifier %q: Regex must be 1-%d characters", c.Name, maxCustomRegexLen)
	case !customRegexChar.MatchString(c.Regex):
		return nil, invalid("custom data identifier %q: Regex contains an unsupported character", c.Name)
	}

	// Go's RE2 engine matches in linear time, so a hostile pattern cannot
	// backtrack catastrophically; compile rejects oversized repeats.
	re, err := regexp.Compile(c.Regex)
	if err != nil {
		return nil, invalid("custom data identifier %q: Regex is not valid", c.Name)
	}

	return re, nil
}

// resolveIdentifier maps a DataIdentifier entry to its detector. A managed
// identifier this emulator does not detect resolves to nil.
func resolveIdentifier(id string, custom map[string]*regexp.Regexp) (*detector, error) {
	if name, ok := strings.CutPrefix(id, managedARNPrefix); ok {
		if !managedNameRE.MatchString(name) {
			return nil, invalid("%q is not a valid managed data identifier ARN", id)
		}

		if det, known := managedDetector(name); known {
			return &det, nil
		}

		return nil, nil
	}

	re, ok := custom[id]
	if !ok {
		return nil, invalid("data identifier %q is neither a managed data identifier ARN nor a custom data identifier", id)
	}

	return &detector{re: re}, nil
}
