package aps

import (
	"context"
	"sort"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/aps/driver"
)

// CreateRuleGroupsNamespace creates a rule-groups namespace under a workspace,
// directly in the ACTIVE state. The definition blob is stored verbatim. A name
// already in use yields a ConflictException.
func (m *Mock) CreateRuleGroupsNamespace(
	_ context.Context, in *driver.RuleGroupsNamespaceInput,
) (*driver.RuleGroupsNamespace, error) {
	if in.Name == "" {
		return nil, validation("name is required")
	}

	var (
		result driver.RuleGroupsNamespace
		dupErr error
	)

	ok := m.workspaces.Update(in.WorkspaceID, func(w driver.Workspace) driver.Workspace {
		if w.RuleGroups == nil {
			w.RuleGroups = map[string]driver.RuleGroupsNamespace{}
		}

		if _, exists := w.RuleGroups[in.Name]; exists {
			dupErr = conflict("rule groups namespace %s already exists in workspace %s", in.Name, in.WorkspaceID)

			return w
		}

		now := m.now()
		ns := driver.RuleGroupsNamespace{
			Name:       in.Name,
			Arn:        m.ruleGroupsNamespaceARN(in.WorkspaceID, in.Name),
			Data:       in.Data,
			Status:     driver.StatusActive,
			CreatedAt:  now,
			ModifiedAt: now,
			Tags:       copyTags(in.Tags),
		}
		w.RuleGroups[in.Name] = ns
		result = ns

		return w
	})
	if !ok {
		return nil, notFound("workspace %s not found", in.WorkspaceID)
	}

	if dupErr != nil {
		return nil, dupErr
	}

	out := result
	out.Tags = copyTags(result.Tags)

	return &out, nil
}

// PutRuleGroupsNamespace creates or replaces a rule-groups namespace. On replace
// the arn, createdAt and tags are preserved and the definition blob and
// modifiedAt are updated.
func (m *Mock) PutRuleGroupsNamespace(
	_ context.Context, in *driver.RuleGroupsNamespaceInput,
) (*driver.RuleGroupsNamespace, error) {
	if in.Name == "" {
		return nil, validation("name is required")
	}

	var result driver.RuleGroupsNamespace

	ok := m.workspaces.Update(in.WorkspaceID, func(w driver.Workspace) driver.Workspace {
		if w.RuleGroups == nil {
			w.RuleGroups = map[string]driver.RuleGroupsNamespace{}
		}

		now := m.now()

		ns, exists := w.RuleGroups[in.Name]
		if !exists {
			ns = driver.RuleGroupsNamespace{
				Name:      in.Name,
				Arn:       m.ruleGroupsNamespaceARN(in.WorkspaceID, in.Name),
				CreatedAt: now,
				Tags:      copyTags(in.Tags),
			}
		}

		ns.Data = in.Data
		ns.Status = driver.StatusActive
		ns.ModifiedAt = now
		w.RuleGroups[in.Name] = ns
		result = ns

		return w
	})
	if !ok {
		return nil, notFound("workspace %s not found", in.WorkspaceID)
	}

	out := result
	out.Tags = copyTags(result.Tags)

	return &out, nil
}

// DescribeRuleGroupsNamespace returns a copy of a rule-groups namespace.
func (m *Mock) DescribeRuleGroupsNamespace(
	_ context.Context, workspaceID, name string,
) (*driver.RuleGroupsNamespace, error) {
	w, ok := m.workspaces.Get(workspaceID)
	if !ok {
		return nil, notFound("workspace %s not found", workspaceID)
	}

	ns, ok := w.RuleGroups[name]
	if !ok {
		return nil, notFound("rule groups namespace %s not found in workspace %s", name, workspaceID)
	}

	out := ns
	out.Tags = copyTags(ns.Tags)

	return &out, nil
}

// DeleteRuleGroupsNamespace removes a rule-groups namespace from a workspace.
func (m *Mock) DeleteRuleGroupsNamespace(_ context.Context, workspaceID, name string) error {
	var missing bool

	ok := m.workspaces.Update(workspaceID, func(w driver.Workspace) driver.Workspace {
		if _, exists := w.RuleGroups[name]; !exists {
			missing = true

			return w
		}

		delete(w.RuleGroups, name)

		return w
	})
	if !ok {
		return notFound("workspace %s not found", workspaceID)
	}

	if missing {
		return notFound("rule groups namespace %s not found in workspace %s", name, workspaceID)
	}

	return nil
}

// ListRuleGroupsNamespaces returns a deterministic page of a workspace's
// rule-groups namespaces, optionally filtered by a name prefix.
func (m *Mock) ListRuleGroupsNamespaces(
	_ context.Context, workspaceID, name string, page driver.Page,
) (namespaces []driver.RuleGroupsNamespace, nextToken string, err error) {
	w, ok := m.workspaces.Get(workspaceID)
	if !ok {
		return nil, "", notFound("workspace %s not found", workspaceID)
	}

	names := make([]string, 0, len(w.RuleGroups))
	for k := range w.RuleGroups {
		names = append(names, k)
	}

	sort.Strings(names)

	filtered := make([]driver.RuleGroupsNamespace, 0, len(names))

	for _, k := range names {
		if name != "" && !strings.HasPrefix(k, name) {
			continue
		}

		ns := w.RuleGroups[k]
		ns.Tags = copyTags(ns.Tags)
		filtered = append(filtered, ns)
	}

	start, end, next := paginate(len(filtered), page)

	return filtered[start:end], next, nil
}
