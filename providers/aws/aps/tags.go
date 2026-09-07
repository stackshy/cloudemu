package aps

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/aps/driver"
)

// arnResource holds the parsed pieces of an APS resource ARN.
type arnResource struct {
	kind        string
	workspaceID string
	name        string
}

// parseARN extracts the resource kind and identifiers from an APS resource ARN
// of the form arn:aws:aps:{region}:{acct}:workspace/{id} or
// arn:aws:aps:{region}:{acct}:rulegroupsnamespace/{id}/{name}.
func parseARN(arn string) (arnResource, error) {
	if !strings.Contains(arn, arnMarker) {
		return arnResource{}, validation("invalid resource ARN: %q", arn)
	}

	parts := strings.SplitN(arn, ":", arnFieldCount)
	if len(parts) < arnFieldCount {
		return arnResource{}, validation("invalid resource ARN: %q", arn)
	}

	segs := strings.Split(parts[arnFieldCount-1], "/")

	switch {
	case segs[0] == kindWorkspace && len(segs) == workspaceSegs:
		return arnResource{kind: kindWorkspace, workspaceID: segs[1]}, nil
	case segs[0] == kindRuleGroupsNamespace && len(segs) == rgNamespaceSegs:
		return arnResource{kind: kindRuleGroupsNamespace, workspaceID: segs[1], name: segs[2]}, nil
	default:
		return arnResource{}, validation("invalid resource ARN: %q", arn)
	}
}

// TagResource adds or overwrites tags on a workspace or rule-groups namespace.
func (m *Mock) TagResource(_ context.Context, resourceArn string, tags map[string]string) error {
	return m.mutateTags(resourceArn, func(t map[string]string) map[string]string {
		if t == nil {
			t = map[string]string{}
		}

		for k, v := range tags {
			t[k] = v
		}

		return t
	})
}

// UntagResource removes tags by key from a workspace or rule-groups namespace.
func (m *Mock) UntagResource(_ context.Context, resourceArn string, tagKeys []string) error {
	return m.mutateTags(resourceArn, func(t map[string]string) map[string]string {
		for _, k := range tagKeys {
			delete(t, k)
		}

		return t
	})
}

// ListTagsForResource returns a copy of a workspace's or rule-groups namespace's
// tags.
func (m *Mock) ListTagsForResource(_ context.Context, resourceArn string) (map[string]string, error) {
	res, err := parseARN(resourceArn)
	if err != nil {
		return nil, err
	}

	w, ok := m.workspaces.Get(res.workspaceID)
	if !ok {
		return nil, notFound("workspace %s not found", res.workspaceID)
	}

	if res.kind == kindWorkspace {
		return copyTags(w.Tags), nil
	}

	ns, ok := w.RuleGroups[res.name]
	if !ok {
		return nil, notFound("rule groups namespace %s not found in workspace %s", res.name, res.workspaceID)
	}

	return copyTags(ns.Tags), nil
}

// mutateTags applies mutate to the tags of the resource named by resourceArn.
func (m *Mock) mutateTags(resourceArn string, mutate func(map[string]string) map[string]string) error {
	res, err := parseARN(resourceArn)
	if err != nil {
		return err
	}

	var missingNamespace bool

	ok := m.workspaces.Update(res.workspaceID, func(w driver.Workspace) driver.Workspace {
		if res.kind == kindWorkspace {
			w.Tags = mutate(w.Tags)

			return w
		}

		ns, exists := w.RuleGroups[res.name]
		if !exists {
			missingNamespace = true

			return w
		}

		ns.Tags = mutate(ns.Tags)
		w.RuleGroups[res.name] = ns

		return w
	})
	if !ok {
		return notFound("workspace %s not found", res.workspaceID)
	}

	if missingNamespace {
		return notFound("rule groups namespace %s not found in workspace %s", res.name, res.workspaceID)
	}

	return nil
}
