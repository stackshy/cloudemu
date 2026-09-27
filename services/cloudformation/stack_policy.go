package cloudformation

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// Stack policy update actions. An update that changes a resource in place is
// Update:Modify, one that recreates it is Update:Replace, and one that drops
// it from the template is Update:Delete.
const (
	StackPolicyModify  = "Update:Modify"
	StackPolicyReplace = "Update:Replace"
	StackPolicyDelete  = "Update:Delete"
	stackPolicyAll     = "Update:*"
)

// MaxStackPolicyLength is the size limit of a stack policy body.
const MaxStackPolicyLength = 16384

const (
	policyEffectAllow = "Allow"
	policyEffectDeny  = "Deny"
	policyResourceAll = "*"
	policyResourcePfx = "LogicalResourceId/"
	condStringEquals  = "StringEquals"
	condStringLike    = "StringLike"
	condResourceType  = "ResourceType"
)

// Reasons a resource update fails with when the stack policy refuses it.
const (
	msgPolicyDenied  = "Action denied by stack policy: Statement [#%d] does not allow [%s] for resource [LogicalResourceId/%s]"
	msgPolicyNoAllow = "Action denied by stack policy: No statement allows [%s] for resource [LogicalResourceId/%s]"
	msgPolicyInvalid = "Error validating stack policy: %s"
	msgPolicyTooLong = "1 validation error detected: Value at 'stackPolicyBody' failed to satisfy constraint: " +
		"Member must have length less than or equal to 16384"
	policyActionValues = "Update:Modify, Update:Replace, Update:Delete, Update:*"
)

// StackPolicy is a parsed stack policy. Once a stack has one, an update of a
// resource is allowed only when a statement allows it and none denies it.
type StackPolicy struct {
	statements []policyStatement
}

// policyStatement is one statement of a stack policy.
type policyStatement struct {
	allow        bool
	actions      []string
	notActions   []string
	resources    []string
	notResources []string
	// typeEquals and typeLike are the ResourceType condition values.
	typeEquals []string
	typeLike   []string
}

// rawStatement is a statement as JSON gives it. A string or a list is
// accepted wherever the grammar allows one or more values.
type rawStatement struct {
	Sid         string                             `json:"Sid"`
	Effect      string                             `json:"Effect"`
	Action      policyValues                       `json:"Action"`
	NotAction   policyValues                       `json:"NotAction"`
	Principal   any                                `json:"Principal"`
	Resource    policyValues                       `json:"Resource"`
	NotResource policyValues                       `json:"NotResource"`
	Condition   map[string]map[string]policyValues `json:"Condition"`
}

// errPolicyValues reports a policy element that is neither a string nor a
// list of strings.
var errPolicyValues = errors.New("expected a string or a list of strings")

// policyValues is a JSON string or list of strings.
type policyValues []string

// UnmarshalJSON accepts "x" or ["x", ...].
func (s *policyValues) UnmarshalJSON(b []byte) error {
	var one string
	if err := json.Unmarshal(b, &one); err == nil {
		*s = policyValues{one}
		return nil
	}

	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return errPolicyValues
	}

	*s = many

	return nil
}

// ParseStackPolicy checks a stack policy body and parses it. A body that is
// not valid JSON or breaks the grammar is a ValidationError.
func ParseStackPolicy(body string) (*StackPolicy, error) {
	if len(body) > MaxStackPolicyLength {
		return nil, cerrors.New(cerrors.InvalidArgument, msgPolicyTooLong)
	}

	var doc struct {
		Statement json.RawMessage `json:"Statement"`
	}

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		return nil, policyError("the policy is not valid JSON")
	}

	raws, err := rawStatements(doc.Statement)
	if err != nil {
		return nil, err
	}

	p := &StackPolicy{statements: make([]policyStatement, 0, len(raws))}

	for i := range raws {
		st, serr := checkStatement(&raws[i])
		if serr != nil {
			return nil, serr
		}

		p.statements = append(p.statements, st)
	}

	return p, nil
}

func policyError(detail string) error {
	return cerrors.Newf(cerrors.InvalidArgument, msgPolicyInvalid, detail)
}

// rawStatements decodes Statement, a single statement or a list of them.
func rawStatements(raw json.RawMessage) ([]rawStatement, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return nil, policyError("the policy must contain a Statement")
	}

	var out []rawStatement

	if raw[0] == '{' {
		var one rawStatement
		if err := json.Unmarshal(raw, &one); err != nil {
			return nil, policyError("invalid Statement: " + err.Error())
		}

		return []rawStatement{one}, nil
	}

	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, policyError("invalid Statement: " + err.Error())
	}

	return out, nil
}

// checkStatement validates one statement against the stack policy grammar.
func checkStatement(r *rawStatement) (policyStatement, error) {
	st := policyStatement{allow: r.Effect == policyEffectAllow, actions: r.Action, notActions: r.NotAction,
		resources: r.Resource, notResources: r.NotResource}

	if r.Effect != policyEffectAllow && r.Effect != policyEffectDeny {
		return st, policyError(fmt.Sprintf("Effect must be Allow or Deny, not [%s]", r.Effect))
	}

	if p, ok := r.Principal.(string); !ok || p != "*" {
		return st, policyError("Principal is required and must be \"*\"")
	}

	if err := checkActions(r.Action, r.NotAction); err != nil {
		return st, err
	}

	if err := checkResources(r.Resource, r.NotResource); err != nil {
		return st, err
	}

	if err := st.addConditions(r.Condition); err != nil {
		return st, err
	}

	return st, nil
}

// addConditions checks a statement's Condition and records its ResourceType
// values.
func (st *policyStatement) addConditions(cond map[string]map[string]policyValues) error {
	for op, keys := range cond {
		if op != condStringEquals && op != condStringLike {
			return policyError(fmt.Sprintf("unsupported condition [%s]; use StringEquals or StringLike", op))
		}

		for key, values := range keys {
			if key != condResourceType {
				return policyError(fmt.Sprintf("unsupported condition key [%s]; only ResourceType is allowed", key))
			}

			if op == condStringEquals {
				st.typeEquals = append(st.typeEquals, values...)
			} else {
				st.typeLike = append(st.typeLike, values...)
			}
		}
	}

	return nil
}

func checkActions(actions, notActions []string) error {
	if (len(actions) == 0) == (len(notActions) == 0) {
		return policyError("each statement must have exactly one of Action and NotAction")
	}

	valid := []string{StackPolicyModify, StackPolicyReplace, StackPolicyDelete, stackPolicyAll}

	for _, a := range slices.Concat(actions, notActions) {
		if !slices.Contains(valid, a) {
			return policyError(fmt.Sprintf("invalid action [%s]; valid actions are %s", a, policyActionValues))
		}
	}

	return nil
}

func checkResources(resources, notResources []string) error {
	if (len(resources) == 0) == (len(notResources) == 0) {
		return policyError("each statement must have exactly one of Resource and NotResource")
	}

	for _, r := range slices.Concat(resources, notResources) {
		if r != policyResourceAll && !strings.HasPrefix(r, policyResourcePfx) {
			return policyError(fmt.Sprintf("invalid resource [%s]; use \"*\" or LogicalResourceId/<id>", r))
		}
	}

	return nil
}

// Allows reports whether the policy allows action on a resource. When it does
// not, reason is the status reason CloudFormation fails the resource with. An
// explicit Deny wins over any Allow, and a resource no statement allows is
// denied.
func (p *StackPolicy) Allows(action, logicalID, resourceType string) (reason string, allowed bool) {
	for i := range p.statements {
		st := &p.statements[i]
		if !st.allow && st.matches(action, logicalID, resourceType) {
			return fmt.Sprintf(msgPolicyDenied, i+1, action, logicalID), false
		}
	}

	for i := range p.statements {
		st := &p.statements[i]
		if st.allow && st.matches(action, logicalID, resourceType) {
			return "", true
		}
	}

	return fmt.Sprintf(msgPolicyNoAllow, action, logicalID), false
}

func (st *policyStatement) matches(action, logicalID, resourceType string) bool {
	return st.matchesAction(action) && st.matchesResource(logicalID) && st.matchesType(resourceType)
}

func (st *policyStatement) matchesAction(action string) bool {
	hit := func(list []string) bool {
		return slices.ContainsFunc(list, func(a string) bool { return a == stackPolicyAll || a == action })
	}

	if len(st.actions) > 0 {
		return hit(st.actions)
	}

	return !hit(st.notActions)
}

// matchesResource matches the logical id against Resource or NotResource.
// CloudFormation evaluates an Allow against the logical id and the resource
// type separately, and denies by default only when both deny. An Allow whose
// NotResource excludes a resource but that has no type condition therefore
// still allows the resource by its type.
func (st *policyStatement) matchesResource(logicalID string) bool {
	hit := func(list []string) bool {
		return slices.ContainsFunc(list, func(r string) bool {
			if r == policyResourceAll {
				return true
			}

			ok, err := path.Match(strings.TrimPrefix(r, policyResourcePfx), logicalID)

			return err == nil && ok
		})
	}

	if len(st.resources) > 0 {
		return hit(st.resources)
	}

	if st.allow && len(st.typeEquals) == 0 && len(st.typeLike) == 0 {
		return true
	}

	return !hit(st.notResources)
}

func (st *policyStatement) matchesType(resourceType string) bool {
	if len(st.typeEquals) == 0 && len(st.typeLike) == 0 {
		return true
	}

	if slices.Contains(st.typeEquals, resourceType) {
		return true
	}

	return slices.ContainsFunc(st.typeLike, func(pattern string) bool {
		ok, err := path.Match(pattern, resourceType)
		return err == nil && ok
	})
}
