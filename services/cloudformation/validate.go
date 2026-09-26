package cloudformation

import (
	"strings"
)

// validate runs the checks CloudFormation makes on a template before it
// looks at parameter values. Only "Unresolved resource dependencies" is a
// widely reported AWS text. The other texts are the best known forms and are
// not measured.
func validate(t *Template) error {
	if err := checkConditionNames(t); err != nil {
		return err
	}

	if err := checkConditionsBlock(t); err != nil {
		return err
	}

	if err := checkMappingNames(t); err != nil {
		return err
	}

	return checkReferences(t)
}

// walkIntrinsics calls visit for every intrinsic function in node, outermost
// first, and also walks into each function's argument.
func walkIntrinsics(node any, visit func(fn string, arg any)) {
	switch v := node.(type) {
	case map[string]any:
		if fn, arg, ok := intrinsic(v); ok {
			visit(fn, arg)
		}

		for _, e := range v {
			walkIntrinsics(e, visit)
		}
	case []any:
		for _, e := range v {
			walkIntrinsics(e, visit)
		}
	}
}

// valueNodes returns the parts of a template that may hold intrinsics:
// resource properties and output values and export names.
func valueNodes(t *Template) []any {
	nodes := make([]any, 0, len(t.Resources)+len(t.Outputs))

	for _, res := range t.Resources {
		nodes = append(nodes, res.Properties)
	}

	for _, od := range t.Outputs {
		nodes = append(nodes, od.Value)

		if od.Export != nil {
			nodes = append(nodes, od.Export.Name)
		}
	}

	return nodes
}

// checkConditionNames checks that every Condition key and every Fn::If names
// a declared condition.
func checkConditionNames(t *Template) error {
	for _, id := range sortedKeys(t.Resources) {
		if c := t.Resources[id].Condition; c != "" && !hasCondition(t, c) {
			return formatErr("Unresolved condition dependency %s in the Resources block of the template", c)
		}
	}

	for _, name := range sortedKeys(t.Outputs) {
		if c := t.Outputs[name].Condition; c != "" && !hasCondition(t, c) {
			return formatErr("Unresolved condition dependency %s in the Outputs block of the template", c)
		}
	}

	return checkIfConditions(t)
}

// checkIfConditions checks that every Fn::If names a declared condition.
func checkIfConditions(t *Template) error {
	var missing string

	for _, node := range valueNodes(t) {
		walkIntrinsics(node, func(fn string, arg any) {
			l, ok := arg.([]any)
			if !ok || fn != fnIf || len(l) == 0 || missing != "" {
				return
			}

			if name := scalarString(l[0]); !hasCondition(t, name) {
				missing = name
			}
		})
	}

	if missing != "" {
		return templateErr("unresolved condition dependency %s in Fn::If", missing)
	}

	return nil
}

func hasCondition(t *Template, name string) bool {
	_, ok := t.Conditions[name]
	return ok
}

// checkConditionsBlock checks that conditions only Ref parameters and pseudo
// parameters, only name declared conditions, and do not form a cycle.
func checkConditionsBlock(t *Template) error {
	bad := map[string]bool{}
	graph := make(map[string][]string, len(t.Conditions))

	for name, def := range t.Conditions {
		walkIntrinsics(def, func(fn string, arg any) {
			if s, ok := arg.(string); ok && fn == fnRef && !pseudoParams[s] {
				if _, isParam := t.Parameters[s]; !isParam {
					bad[s] = true
				}
			}
		})

		refs := map[string]bool{}
		conditionRefs(def, refs)
		graph[name] = sortedKeys(refs)
	}

	if len(bad) > 0 {
		return formatErr("Unresolved dependencies [%s]. Cannot reference resources in the Conditions block of the template",
			strings.Join(sortedKeys(bad), ", "))
	}

	return checkConditionGraph(graph)
}

// conditionRefs records the names of the conditions a condition refers to.
func conditionRefs(node any, out map[string]bool) {
	switch v := node.(type) {
	case map[string]any:
		if name, ok := v[keyCondition].(string); ok && len(v) == 1 {
			out[name] = true
		}

		for _, e := range v {
			conditionRefs(e, out)
		}
	case []any:
		for _, e := range v {
			conditionRefs(e, out)
		}
	}
}

// checkConditionGraph rejects a reference to an undeclared condition and a
// cycle between conditions.
func checkConditionGraph(graph map[string][]string) error {
	const (
		visiting = 1
		done     = 2
	)

	state := map[string]int{}

	var visit func(name string) error

	visit = func(name string) error {
		switch state[name] {
		case visiting:
			return formatErr("Circular dependency between conditions: [%s]", name)
		case done:
			return nil
		}

		state[name] = visiting

		for _, dep := range graph[name] {
			if _, ok := graph[dep]; !ok {
				return templateErr("unresolved condition dependency %s in Condition", dep)
			}

			if err := visit(dep); err != nil {
				return err
			}
		}

		state[name] = done

		return nil
	}

	for _, name := range sortedKeys(graph) {
		if err := visit(name); err != nil {
			return err
		}
	}

	return nil
}

// checkMappingNames checks that each Fn::FindInMap with a literal map name
// names a declared mapping.
func checkMappingNames(t *Template) error {
	nodes := valueNodes(t)
	for _, def := range t.Conditions {
		nodes = append(nodes, def)
	}

	var missing string

	for _, node := range nodes {
		walkIntrinsics(node, func(fn string, arg any) {
			l, ok := arg.([]any)
			if !ok || fn != fnFindInMap || len(l) == 0 || missing != "" {
				return
			}

			if name, isStr := l[0].(string); isStr {
				if _, found := t.Mappings[name]; !found {
					missing = name
				}
			}
		})
	}

	if missing != "" {
		return templateErr("Mapping named '%s' is not present in the 'Mappings' section of template.", missing)
	}

	return nil
}

// checkReferences checks that every Ref, Fn::GetAtt, Fn::Sub variable and
// DependsOn in the resources and outputs names something the template has.
func checkReferences(t *Template) error {
	resNodes := make([]any, 0, len(t.Resources))

	var dependsOn []string

	for _, res := range t.Resources {
		resNodes = append(resNodes, res.Properties)
		dependsOn = append(dependsOn, res.DependsOnList()...)
	}

	if bad := unresolvedNames(t, resNodes, dependsOn); len(bad) > 0 {
		return formatErr("Unresolved resource dependencies [%s] in the Resources block of the template", strings.Join(bad, ", "))
	}

	outNodes := make([]any, 0, len(t.Outputs))

	for _, od := range t.Outputs {
		outNodes = append(outNodes, od.Value)

		if od.Export != nil {
			outNodes = append(outNodes, od.Export.Name)
		}
	}

	if bad := unresolvedNames(t, outNodes, nil); len(bad) > 0 {
		return formatErr("Unresolved resource dependencies [%s] in the Outputs block of the template", strings.Join(bad, ", "))
	}

	return nil
}

// unresolvedNames returns, sorted, the names the nodes refer to that the
// template does not declare. Fn::GetAtt and DependsOn must name resources.
func unresolvedNames(t *Template, nodes []any, dependsOn []string) []string {
	refs, atts := map[string]bool{}, map[string]bool{}

	for _, n := range nodes {
		collectNames(n, refs, atts)
	}

	for _, d := range dependsOn {
		atts[d] = true
	}

	bad := map[string]bool{}

	for name := range refs {
		_, isParam := t.Parameters[name]
		_, isRes := t.Resources[name]

		if !pseudoParams[name] && !isParam && !isRes {
			bad[name] = true
		}
	}

	for name := range atts {
		if _, isRes := t.Resources[name]; !isRes {
			bad[name] = true
		}
	}

	return sortedKeys(bad)
}

// collectNames records the names each Ref and Fn::Sub variable uses in refs,
// and the resources each Fn::GetAtt and dotted Fn::Sub variable uses in atts.
func collectNames(node any, refs, atts map[string]bool) {
	switch v := node.(type) {
	case map[string]any:
		if fn, arg, ok := intrinsic(v); ok {
			collectFnNames(fn, arg, refs, atts)
			return
		}

		for _, e := range v {
			collectNames(e, refs, atts)
		}
	case []any:
		for _, e := range v {
			collectNames(e, refs, atts)
		}
	}
}

func collectFnNames(fn string, arg any, refs, atts map[string]bool) {
	switch fn {
	case fnRef:
		if s, ok := arg.(string); ok {
			refs[s] = true
			return
		}
	case fnGetAtt:
		if logical, _ := splitGetAtt(arg); logical != "" {
			atts[logical] = true
		}

		return
	case fnSub:
		collectSubNames(arg, refs, atts)
		return
	}

	collectNames(arg, refs, atts)
}

// collectSubNames records the variables of an Fn::Sub template. Names given
// in its variable map are local and need no declaration.
func collectSubNames(arg any, refs, atts map[string]bool) {
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
			collectNames(v[1], refs, atts)
		}
	}

	for _, tok := range subVarPattern.FindAllString(tmpl, -1) {
		name := tok[2 : len(tok)-1]
		if _, local := locals[name]; local || strings.HasPrefix(name, "!") {
			continue
		}

		if i := strings.Index(name, "."); i >= 0 {
			atts[name[:i]] = true
			continue
		}

		refs[name] = true
	}
}
