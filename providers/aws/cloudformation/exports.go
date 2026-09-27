package cloudformation

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/pagination"
	cfn "github.com/stackshy/cloudemu/v2/services/cloudformation"
)

// Error texts of the export guards and the export list operations.
const (
	msgExportTaken    = "Export with name %s is already exported by stack %s."
	msgExportUpdated  = "Export %s cannot be updated as it is in use by %s"
	msgExportRemoved  = "Export %s cannot be deleted as it is in use by %s"
	msgExportDeleted  = "Cannot delete export %s as it is in use by %s"
	msgNotImported    = "Export '%s' is not imported by any stack."
	msgExportNameNeed = "ExportName is required"
)

// exportsPageSize is the number of entries a ListExports or ListImports page
// holds.
const exportsPageSize = 100

// exportEntry is one export and the stack that owns it.
type exportEntry struct {
	cfn.Export
	stackName string
}

// allExports lists every export in the region, sorted by name. A stack that
// is deleted, or being deleted, exports nothing.
func (m *Mock) allExports() []exportEntry {
	var out []exportEntry

	for _, sd := range m.sortedStacks() {
		sd.mu.RLock()

		if st := sd.stack.Status; st != cfn.StatusDeleteComplete && st != cfn.StatusDeleteInProgress {
			for _, o := range sd.stack.Outputs {
				if o.ExportName != "" {
					out = append(out, exportEntry{
						Export:    cfn.Export{ExportingStackID: sd.stack.ID, Name: o.ExportName, Value: o.Value},
						stackName: sd.stack.Name,
					})
				}
			}
		}

		sd.mu.RUnlock()
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out
}

// exportValues maps the export names a stack can import to their values.
// A stack cannot import its own exports.
func (m *Mock) exportValues(stackID string) map[string]string {
	out := map[string]string{}

	for _, e := range m.allExports() {
		if e.ExportingStackID != stackID {
			out[e.Name] = e.Value
		}
	}

	return out
}

// importers returns, sorted, the names of the stacks other than stackID
// that import the export name.
func (m *Mock) importers(name, stackID string) []string {
	var out []string

	for _, sd := range m.sortedStacks() {
		sd.mu.RLock()

		if sd.stack.ID != stackID && sd.stack.Status != cfn.StatusDeleteComplete && slices.Contains(sd.imports, name) {
			out = append(out, sd.stack.Name)
		}

		sd.mu.RUnlock()
	}

	return out
}

// bindImports resolves the exports t imports, checks that each exists and
// records them on the stack together with keep, the imports it already
// holds. It returns the names t imports, or the failure of a missing
// export. It refreshes res.Exports, so the operation reads the values
// current when it starts.
func (m *Mock) bindImports(sd *stackData, t *cfn.Template, res *cfn.Resolver, keep []string) ([]string, *applyFailure) {
	m.exportMu.Lock()
	defer m.exportMu.Unlock()

	res.Exports = m.exportValues(res.StackID)

	names, err := res.ImportNames(t)
	if err != nil {
		return nil, &applyFailure{verb: verbCreate, err: err}
	}

	for _, name := range names {
		if _, ok := res.Exports[name]; !ok {
			return nil, &applyFailure{verb: verbCreate, err: cfn.NoExportError(name)}
		}
	}

	merged := append(slices.Clone(keep), names...)
	slices.Sort(merged)
	sd.setImports(slices.Compact(merged))

	return names, nil
}

func (sd *stackData) setImports(names []string) {
	sd.mu.Lock()
	defer sd.mu.Unlock()

	sd.imports = names
}

func (sd *stackData) importList() []string {
	sd.mu.RLock()
	defer sd.mu.RUnlock()

	return slices.Clone(sd.imports)
}

// checkExports checks the outputs a stack is about to publish: a new export
// name must be free, and an export another stack imports must keep its
// value. The caller holds exportMu.
func (m *Mock) checkExports(sd *stackData, outputs []cfn.Output) error {
	_, stackID := sd.identity()

	owners := map[string]string{}

	for _, e := range m.allExports() {
		if e.ExportingStackID != stackID {
			owners[e.Name] = e.stackName
		}
	}

	next := map[string]string{}

	for _, o := range outputs {
		if o.ExportName == "" {
			continue
		}

		if owner, taken := owners[o.ExportName]; taken {
			return cerrors.Newf(cerrors.InvalidArgument, msgExportTaken, o.ExportName, owner)
		}

		next[o.ExportName] = o.Value
	}

	return m.checkExportsInUse(sd, stackID, next)
}

// checkExportsInUse refuses to drop or change an export another stack
// imports. next maps the export names the stack is about to publish to their
// values.
func (m *Mock) checkExportsInUse(sd *stackData, stackID string, next map[string]string) error {
	sd.mu.RLock()
	current := slices.Clone(sd.stack.Outputs)
	sd.mu.RUnlock()

	for _, o := range current {
		if o.ExportName == "" {
			continue
		}

		users := m.importers(o.ExportName, stackID)
		if len(users) == 0 {
			continue
		}

		value, kept := next[o.ExportName]

		switch {
		case !kept:
			return cerrors.Newf(cerrors.InvalidArgument, msgExportRemoved, o.ExportName, strings.Join(users, ", "))
		case value != o.Value:
			return cerrors.Newf(cerrors.InvalidArgument, msgExportUpdated, o.ExportName, strings.Join(users, ", "))
		}
	}

	return nil
}

// exportInUse returns the reason a stack cannot be deleted because another
// stack imports one of its exports, or "". The caller holds exportMu.
func (m *Mock) exportInUse(sd *stackData) string {
	sd.mu.RLock()
	outputs := slices.Clone(sd.stack.Outputs)
	stackID := sd.stack.ID
	sd.mu.RUnlock()

	for _, o := range outputs {
		if o.ExportName == "" {
			continue
		}

		if users := m.importers(o.ExportName, stackID); len(users) > 0 {
			return fmt.Sprintf(msgExportDeleted, o.ExportName, strings.Join(users, ", "))
		}
	}

	return ""
}

// ListExports returns one page of the region's exports, sorted by name.
func (m *Mock) ListExports(_ context.Context, nextToken string) (*cfn.ExportList, error) {
	entries := m.allExports()

	all := make([]cfn.Export, len(entries))
	for i := range entries {
		all[i] = entries[i].Export
	}

	page, err := pagination.Paginate(all, nextToken, exportsPageSize)
	if err != nil {
		return nil, cerrors.New(cerrors.InvalidArgument, msgInvalidNextToken)
	}

	return &cfn.ExportList{Exports: page.Items, NextToken: page.NextPageToken}, nil
}

// ListImports returns one page of the names of the stacks that import an
// export. An export nothing imports, or that does not exist, is a
// ValidationError.
func (m *Mock) ListImports(_ context.Context, in *cfn.ListImportsInput) (*cfn.ImportList, error) {
	if in.ExportName == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, msgExportNameNeed)
	}

	users := m.importers(in.ExportName, "")
	if len(users) == 0 {
		return nil, cerrors.Newf(cerrors.InvalidArgument, msgNotImported, in.ExportName)
	}

	page, err := pagination.Paginate(users, in.NextToken, exportsPageSize)
	if err != nil {
		return nil, cerrors.New(cerrors.InvalidArgument, msgInvalidNextToken)
	}

	return &cfn.ImportList{Imports: page.Items, NextToken: page.NextPageToken}, nil
}
