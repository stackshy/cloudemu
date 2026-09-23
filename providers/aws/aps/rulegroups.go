package aps

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/internal/idempotency"
	"github.com/stackshy/cloudemu/v2/services/aps/driver"
)

// CreateRuleGroupsNamespace creates a rule-groups namespace under a workspace,
// directly in the ACTIVE state. The definition blob is stored verbatim. A
// repeated ClientToken within the dedup window returns the live namespace
// already created for it instead of hitting the name-conflict check. A retried
// create resends the same Name and would otherwise conflict with itself. The
// token is scoped to the workspace+name it was sent for and only replays while
// that namespace still exists. A name already in use under a different (or no)
// token yields a ConflictException.
func (m *Mock) CreateRuleGroupsNamespace(
	ctx context.Context, in *driver.RuleGroupsNamespaceInput,
) (*driver.RuleGroupsNamespace, error) {
	if in.Name == "" {
		return nil, validation("name is required")
	}

	now := m.now()

	return idempotency.Do(ctx, m.rgTokens, idempotency.Scoped(in.ClientToken, in.WorkspaceID, in.Name), now,
		func(ctx context.Context, _ string) (*driver.RuleGroupsNamespace, error) {
			return m.DescribeRuleGroupsNamespace(ctx, in.WorkspaceID, in.Name)
		},
		func() (*driver.RuleGroupsNamespace, error) { return m.createRuleGroupsNamespace(in, now) },
		func(ns *driver.RuleGroupsNamespace) string { return ns.Arn })
}

// createRuleGroupsNamespace inserts one new namespace into its workspace.
func (m *Mock) createRuleGroupsNamespace(
	in *driver.RuleGroupsNamespaceInput, now time.Time,
) (*driver.RuleGroupsNamespace, error) {
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

	// The token's id is the name-derived ARN, so drop it: a same-name namespace
	// created later by another request must not replay to this token.
	m.rgTokens.Forget(m.ruleGroupsNamespaceARN(workspaceID, name))

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
