// Package asl is a dependency-free interpreter for a practical subset of the
// Amazon States Language (ASL). It parses a state-machine definition into typed
// structs, validates it structurally up front, and walks the state graph from
// StartAt following Next/End — computing the terminal status/output and a
// per-state event history. It is driven by config.Clock so Wait timing is
// deterministic under a FakeClock.
//
// Supported in this build: Pass, Choice, Wait, Succeed, Fail, and Task (the
// arn:aws:states:::lambda:invoke and bare Lambda-function-ARN forms, with Retry
// and Catch); the full InputPath -> Parameters -> Result -> ResultSelector ->
// ResultPath -> OutputPath I/O pipeline; a reference/selection JSONPath subset;
// the $$ context object; and a first-wave intrinsic set. Parallel and Map parse
// (so a definition containing them is accepted at create time) but fail loudly
// at run time until their handlers land. JSONata query language, and JSONPath
// filters/wildcards/recursive-descent, are rejected rather than silently
// mis-run.
package asl

import (
	"encoding/json"
	"fmt"
)

// State type names.
const (
	TypePass     = "Pass"
	TypeChoice   = "Choice"
	TypeWait     = "Wait"
	TypeSucceed  = "Succeed"
	TypeFail     = "Fail"
	TypeTask     = "Task"
	TypeParallel = "Parallel"
	TypeMap      = "Map"
)

// pathField is a three-state ASL path field: absent, explicit JSON null, or a
// string path. The distinction is load-bearing — e.g. ResultPath absent means
// "$" (replace), ResultPath:null means "discard the result, pass raw input".
type pathField struct {
	set  bool
	null bool
	path string
}

// UnmarshalJSON records presence and distinguishes null from a string value.
// It is only invoked when the key is present, so set stays false when absent.
func (p *pathField) UnmarshalJSON(b []byte) error {
	p.set = true
	if string(b) == "null" {
		p.null = true

		return nil
	}

	return json.Unmarshal(b, &p.path)
}

// State is one ASL state. Fields not relevant to a given Type are simply unset.
type State struct {
	name string // populated from the States map key

	Type           string          `json:"Type"`
	Comment        string          `json:"Comment"`
	Next           string          `json:"Next"`
	End            bool            `json:"End"`
	QueryLanguage  string          `json:"QueryLanguage"`
	InputPath      pathField       `json:"InputPath"`
	OutputPath     pathField       `json:"OutputPath"`
	Parameters     json.RawMessage `json:"Parameters"`
	Result         json.RawMessage `json:"Result"`
	ResultSelector json.RawMessage `json:"ResultSelector"`
	ResultPath     pathField       `json:"ResultPath"`

	// Choice.
	Choices []*ChoiceRule `json:"Choices"`
	Default string        `json:"Default"`

	// Wait.
	Seconds       *int   `json:"Seconds"`
	SecondsPath   string `json:"SecondsPath"`
	Timestamp     string `json:"Timestamp"`
	TimestampPath string `json:"TimestampPath"`

	// Fail.
	Error string `json:"Error"`
	Cause string `json:"Cause"`

	// Task.
	Resource string     `json:"Resource"`
	Retry    []*Retrier `json:"Retry"`
	Catch    []*Catcher `json:"Catch"`
}

// StateMachineDef is a parsed ASL definition.
type StateMachineDef struct {
	Comment       string            `json:"Comment"`
	StartAt       string            `json:"StartAt"`
	States        map[string]*State `json:"States"`
	QueryLanguage string            `json:"QueryLanguage"`
}

// knownTypes is the set of ASL state types the parser accepts structurally.
func knownType(t string) bool {
	switch t {
	case TypePass, TypeChoice, TypeWait, TypeSucceed, TypeFail, TypeTask, TypeParallel, TypeMap:
		return true
	default:
		return false
	}
}

// Parse unmarshals and structurally validates an ASL definition. A returned
// error carries a human-readable message; callers map it to their own
// InvalidDefinition-shaped API error.
func Parse(definition string) (*StateMachineDef, error) {
	var def StateMachineDef
	if err := json.Unmarshal([]byte(definition), &def); err != nil {
		return nil, fmt.Errorf("definition is not valid JSON: %w", err)
	}

	if def.QueryLanguage == queryLangJSONata {
		return nil, aslErrf("QueryLanguage %q is not supported (JSONPath only)", def.QueryLanguage)
	}

	if def.StartAt == "" {
		return nil, aslErrf("definition is missing the required top-level 'StartAt' field")
	}

	if len(def.States) == 0 {
		return nil, aslErrf("definition is missing the required top-level 'States' object")
	}

	if _, ok := def.States[def.StartAt]; !ok {
		return nil, aslErrf("StartAt %q does not name a state in 'States'", def.StartAt)
	}

	for name, st := range def.States {
		st.name = name
		if err := validateState(def.States, st); err != nil {
			return nil, err
		}
	}

	return &def, nil
}
