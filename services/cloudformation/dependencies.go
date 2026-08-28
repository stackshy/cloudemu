package cloudformation

import (
	"sort"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// OrderResources returns the template's resource logical IDs in a deterministic
// creation order: dependencies (via Ref / Fn::GetAtt / Fn::Sub references and
// explicit DependsOn) come before the resources that reference them. Ties are
// broken by logical-ID sort so the order — and the resulting event stream — is
// stable across runs. A dependency cycle is an error.
func OrderResources(t *Template) ([]string, error) {
	return topoSort(Dependencies(t))
}

// Dependencies returns, for each resource logical ID, the other resource logical
// IDs it directly depends on (via Ref / Fn::GetAtt / Fn::Sub references and
// explicit DependsOn). Exported so an update planner can propagate a replacement
// to the resources that reference the replaced one.
func Dependencies(t *Template) map[string][]string {
	deps := make(map[string][]string, len(t.Resources))

	for id, r := range t.Resources {
		deps[id] = resourceDeps(id, r, t.Resources)
	}

	return deps
}

// resourceDeps collects the logical IDs resource id depends on: every reference
// inside its properties that names another resource, plus explicit DependsOn.
func resourceDeps(id string, r ResourceDef, all map[string]ResourceDef) []string {
	refs := map[string]bool{}
	collectRefs(r.Properties, refs)

	for _, d := range r.DependsOnList() {
		refs[d] = true
	}

	out := make([]string, 0, len(refs))

	for name := range refs {
		if name == id {
			continue
		}

		if _, ok := all[name]; ok {
			out = append(out, name)
		}
	}

	sort.Strings(out)

	return out
}

// collectRefs walks a decoded property tree and records every logical name a
// Ref / Fn::GetAtt / Fn::Sub mentions (parameters and pseudo-parameters are
// harmlessly included; the caller keeps only names that are resources).
func collectRefs(node any, out map[string]bool) {
	switch v := node.(type) {
	case map[string]any:
		if fn, arg, ok := intrinsic(v); ok {
			collectIntrinsicRefs(fn, arg, out)
			return
		}

		for _, e := range v {
			collectRefs(e, out)
		}
	case []any:
		for _, e := range v {
			collectRefs(e, out)
		}
	}
}

func collectIntrinsicRefs(fn string, arg any, out map[string]bool) {
	switch fn {
	case fnRef:
		if s, ok := arg.(string); ok {
			out[s] = true
		}
	case fnGetAtt:
		if logical, _ := splitGetAtt(arg); logical != "" {
			out[logical] = true
		}
	case fnSub:
		collectSubRefs(arg, out)
	default:
		collectRefs(arg, out)
	}
}

func collectSubRefs(arg any, out map[string]bool) {
	var tmpl string

	switch v := arg.(type) {
	case string:
		tmpl = v
	case []any:
		if len(v) > 0 {
			tmpl, _ = v[0].(string)
		}

		for _, e := range v[1:] {
			collectRefs(e, out)
		}
	}

	for _, tok := range subVarPattern.FindAllString(tmpl, -1) {
		name := tok[2 : len(tok)-1]
		if strings.HasPrefix(name, "!") {
			continue
		}

		if i := strings.Index(name, "."); i >= 0 {
			name = name[:i]
		}

		out[name] = true
	}
}

// topoSort returns a stable topological order of the dependency graph, or an
// error naming a resource involved in a cycle.
func topoSort(deps map[string][]string) ([]string, error) {
	ids := make([]string, 0, len(deps))
	for id := range deps {
		ids = append(ids, id)
	}

	sort.Strings(ids)

	const (
		unvisited = 0
		visiting  = 1
		done      = 2
	)

	state := make(map[string]int, len(ids))

	var order []string

	var visit func(id string) error

	visit = func(id string) error {
		switch state[id] {
		case visiting:
			return cerrors.Newf(cerrors.InvalidArgument, "circular dependency involving resource %q", id)
		case done:
			return nil
		}

		state[id] = visiting

		for _, dep := range deps[id] {
			if err := visit(dep); err != nil {
				return err
			}
		}

		state[id] = done

		order = append(order, id)

		return nil
	}

	for _, id := range ids {
		if err := visit(id); err != nil {
			return nil, err
		}
	}

	return order, nil
}
