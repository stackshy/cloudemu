package cloudformation

import (
	"encoding/json"
	"sort"
	"strings"
)

// LiveResource is a deployed resource as the change planner sees it.
type LiveResource struct {
	Type       string
	PhysicalID string
	// Props holds the resolved properties the resource was last applied with.
	Props map[string]any
}

// ChangePlanInput is what PlanChanges compares. Old and New have their
// conditions applied. Old is nil for a stack that does not exist yet.
type ChangePlanInput struct {
	Old, New *Template
	Live     map[string]LiveResource
	// NewProps holds each resource's resolved new properties. A resource is
	// missing when its properties cannot be resolved before execution, such
	// as when they reference a resource that is not created yet.
	NewProps map[string]map[string]any
	// ChangedParams names the parameters whose values change.
	ChangedParams map[string]bool
	Registry      Registry
}

// How a planned change can affect the values other resources read from it.
const (
	effectModify = iota + 1
	effectReplace
)

// PlanChanges lists what an update from Old to New does to each resource, in
// logical ID order: Add for a new resource, Remove for a dropped one, and
// Modify for a kept resource with a changed property. A Modify reports each
// cause. A template edit is a static DirectModification. A changed parameter
// is a static ParameterReference. A reference to a resource that may be
// replaced, or an attribute of one that changes, is a dynamic
// ResourceReference or ResourceAttribute, whose value is only known at
// execution. Resources are walked in dependency order, so a change reaches
// the resources that read from it.
func PlanChanges(in *ChangePlanInput) []ResourceChange {
	order, err := OrderResources(in.New)
	if err != nil {
		order = sortedKeys(in.New.Resources)
	}

	effects := map[string]int{}

	var out []ResourceChange

	for _, id := range order {
		rdef := in.New.Resources[id]

		live, ok := in.Live[id]
		if !ok {
			out = append(out, ResourceChange{
				Action: ChangeActionAdd, LogicalID: id, ResourceType: rdef.Type,
				AfterContext: afterContext(in.NewProps[id], rdef.Properties, effects),
			})

			// A new resource's Ref and attributes are only known once it
			// exists.
			effects[id] = effectReplace

			continue
		}

		if c, changed := in.modifyChange(id, rdef.Type, &live, effects); changed {
			out = append(out, c)
		}
	}

	for id, live := range in.Live {
		if _, kept := in.New.Resources[id]; !kept {
			out = append(out, ResourceChange{
				Action: ChangeActionRemove, LogicalID: id, PhysicalID: live.PhysicalID,
				ResourceType: live.Type, PolicyAction: in.removePolicy(id, live.Type),
				BeforeContext: propertiesContext(live.Props),
			})
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].LogicalID < out[j].LogicalID })

	return out
}

// removePolicy is the PolicyAction of removing id, from the DeletionPolicy
// it was deployed with.
func (in *ChangePlanInput) removePolicy(id, rtype string) string {
	rdef := ResourceDef{Type: rtype}
	if in.Old != nil {
		rdef.DeletionPolicy = in.Old.Resources[id].DeletionPolicy
	}

	return removePolicyAction(&rdef)
}

// modifyChange builds the Modify change of a kept resource, and records how
// it affects the resources that read from it. It reports false when the
// resource does not change.
func (in *ChangePlanInput) modifyChange(id, rtype string, live *LiveResource, effects map[string]int) (ResourceChange, bool) {
	var oldRaw map[string]any
	if in.Old != nil {
		oldRaw = in.Old.Resources[id].Properties
	}

	newRaw := in.New.Resources[id].Properties
	newProps, resolved := in.NewProps[id]

	names := propertyNames(oldRaw, newRaw, live.Props, newProps)
	details := make([]ChangeDetail, 0, len(names))

	for _, name := range names {
		p := propertyChange{
			oldRaw: oldRaw[name], newRaw: newRaw[name], hasOldRaw: in.Old != nil,
			before: live.Props[name], after: newProps[name], resolved: resolved,
		}
		target := in.target(rtype, name)
		details = append(details, in.propertyDetails(&p, &target, effects)...)
	}

	if len(details) == 0 {
		return ResourceChange{}, false
	}

	c := ResourceChange{
		Action: ChangeActionModify, LogicalID: id, PhysicalID: live.PhysicalID, ResourceType: rtype,
		Replacement: replacementOf(details), Scope: scopeOf(details), Details: details,
		BeforeContext: propertiesContext(live.Props),
	}

	if resolved {
		c.AfterContext = afterContext(newProps, newRaw, effects)
	}

	effects[id] = effectModify

	if c.Replacement != ReplacementFalse {
		effects[id] = effectReplace
	}

	if c.Replacement == ReplacementTrue {
		rdef := in.New.Resources[id]
		c.PolicyAction = replacePolicyAction(&rdef)
	}

	return c, true
}

// propertyChange is one property of a kept resource: its template values and
// its resolved values before and after.
type propertyChange struct {
	oldRaw, newRaw any
	hasOldRaw      bool
	before, after  any
	resolved       bool
}

// propertyDetails lists the causes of a change to one property.
func (in *ChangePlanInput) propertyDetails(p *propertyChange, target *ChangeTarget, effects map[string]int) []ChangeDetail {
	var out []ChangeDetail

	add := func(evaluation, source, entity string) {
		out = append(out, ChangeDetail{Target: *target, Evaluation: evaluation, ChangeSource: source, CausingEntity: entity})
	}

	if p.hasOldRaw && !sameValue(p.oldRaw, p.newRaw) {
		add(EvaluationStatic, SourceDirectModification, "")
	}

	in.referenceCauses(p.newRaw, effects, add)

	// A resolved value can change with no cause in the template, such as a
	// pseudo parameter that now resolves differently.
	if len(out) == 0 && p.resolved && !sameValue(p.before, p.after) {
		add(EvaluationStatic, SourceDirectModification, "")
	}

	for i := range out {
		setValues(&out[i].Target, p, out[i].Evaluation)
	}

	return out
}

// referenceCauses reports the references in a property value whose values
// change: a changed parameter, a resource that may be replaced, or an
// attribute of a resource that changes.
func (in *ChangePlanInput) referenceCauses(node any, effects map[string]int, add func(evaluation, source, entity string)) {
	refs, atts := referencedEntities(node)

	for _, name := range refs {
		switch {
		case in.ChangedParams[name]:
			add(EvaluationStatic, SourceParameterReference, name)
		case effects[name] == effectReplace:
			add(EvaluationDynamic, SourceResourceReference, name)
		}
	}

	for _, att := range atts {
		logical, _, _ := strings.Cut(att, ".")
		if effects[logical] != 0 {
			add(EvaluationDynamic, SourceResourceAttribute, att)
		}
	}
}

// target is the change target of one property. A Tags change targets the
// Tags attribute, which never recreates the resource.
func (in *ChangePlanInput) target(rtype, name string) ChangeTarget {
	if name == AttributeTags {
		return ChangeTarget{Attribute: AttributeTags, RequiresRecreation: RecreationNever}
	}

	t := ChangeTarget{Attribute: AttributeProperties, Name: name, RequiresRecreation: RecreationAlways}

	if schema, ok := in.Registry[rtype].(ReplacementSchema); ok && !schema.RequiresReplacement(name) {
		t.RequiresRecreation = RecreationNever
	}

	return t
}

// knownAfterApply is the after value of a change that is only known once
// the change set runs.
const knownAfterApply = "{{changeSet:KNOWN_AFTER_APPLY}}"

// setValues fills a target's before and after values.
func setValues(t *ChangeTarget, p *propertyChange, evaluation string) {
	t.BeforeValue = valueString(p.before)
	t.AttributeChangeType = ChangeActionModify

	switch {
	case evaluation == EvaluationDynamic:
		t.AfterValue = knownAfterApply
	case p.resolved:
		t.AfterValue = valueString(p.after)
	}

	switch {
	case p.before == nil:
		t.AttributeChangeType = ChangeActionAdd
	case p.resolved && p.after == nil:
		t.AttributeChangeType = ChangeActionRemove
	}
}

// replacementOf rolls a resource's details up into its Replacement value. A
// property that always recreates the resource makes it True when its value
// is known now, and Conditional when it is only known at execution.
func replacementOf(details []ChangeDetail) string {
	out := ReplacementFalse

	for i := range details {
		d := &details[i]
		if d.Target.RequiresRecreation != RecreationAlways {
			continue
		}

		if d.Evaluation == EvaluationStatic {
			return ReplacementTrue
		}

		out = ReplacementConditional
	}

	return out
}

// scopeOf lists the attributes a resource's details touch.
func scopeOf(details []ChangeDetail) []string {
	var props, tags bool

	for i := range details {
		attr := details[i].Target.Attribute
		props = props || attr == AttributeProperties
		tags = tags || attr == AttributeTags
	}

	var out []string

	if props {
		out = append(out, AttributeProperties)
	}

	if tags {
		out = append(out, AttributeTags)
	}

	return out
}

// propertyNames returns the sorted names any of the property maps set.
func propertyNames(maps ...map[string]any) []string {
	names := map[string]bool{}

	for _, m := range maps {
		for k := range m {
			names[k] = true
		}
	}

	return sortedKeys(names)
}

// referencedEntities returns, sorted, the names a property value reads with
// Ref or a plain Fn::Sub variable, and the "Resource.Attribute" names it
// reads with Fn::GetAtt or a dotted Fn::Sub variable.
func referencedEntities(node any) (refs, atts []string) {
	refSet, attSet := map[string]bool{}, map[string]bool{}

	walkIntrinsics(node, func(fn string, arg any) {
		switch fn {
		case fnRef:
			if s, ok := arg.(string); ok {
				refSet[s] = true
			}
		case fnGetAtt:
			if logical, attr := splitGetAtt(arg); logical != "" {
				attSet[logical+"."+attr] = true
			}
		case fnSub:
			subEntities(arg, refSet, attSet)
		}
	})

	return sortedKeys(refSet), sortedKeys(attSet)
}

// subEntities records the variables of an Fn::Sub template that are not
// given in its variable map.
func subEntities(arg any, refs, atts map[string]bool) {
	var (
		tmpl   string
		locals map[string]any
	)

	switch v := arg.(type) {
	case string:
		tmpl = v
	case []any:
		if len(v) > 0 {
			tmpl, _ = v[0].(string)
		}

		if len(v) > 1 {
			locals, _ = v[1].(map[string]any)
		}
	}

	for _, tok := range subVarPattern.FindAllString(tmpl, -1) {
		name := tok[2 : len(tok)-1]
		if _, local := locals[name]; local || strings.HasPrefix(name, "!") {
			continue
		}

		if strings.Contains(name, ".") {
			atts[name] = true
		} else {
			refs[name] = true
		}
	}
}

// valueString renders a property value the way a change target reports it:
// a string as is, anything else as JSON.
func valueString(v any) string {
	if v == nil {
		return ""
	}

	if s, ok := v.(string); ok {
		return s
	}

	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}

	return string(b)
}

// afterContext renders a resource's resolved new properties as its after
// context. A property that reads a value only known at execution shows the
// KNOWN_AFTER_APPLY placeholder instead of the value it resolves to now.
func afterContext(props, raw map[string]any, effects map[string]int) string {
	if props == nil {
		return ""
	}

	out := make(map[string]any, len(props))

	for name, v := range props {
		out[name] = v

		if dynamicValue(raw[name], effects) {
			out[name] = knownAfterApply
		}
	}

	return propertiesContext(out)
}

// dynamicValue reports whether a property value reads a resource that may be
// replaced, or an attribute of a resource that changes.
func dynamicValue(node any, effects map[string]int) bool {
	refs, atts := referencedEntities(node)

	for _, name := range refs {
		if effects[name] == effectReplace {
			return true
		}
	}

	for _, att := range atts {
		if logical, _, _ := strings.Cut(att, "."); effects[logical] != 0 {
			return true
		}
	}

	return false
}

// propertiesContext renders a resource's properties as the JSON context a
// change reports, or "" when they are not known.
func propertiesContext(props map[string]any) string {
	if props == nil {
		return ""
	}

	b, err := json.Marshal(map[string]any{AttributeProperties: props})
	if err != nil {
		return ""
	}

	return string(b)
}
