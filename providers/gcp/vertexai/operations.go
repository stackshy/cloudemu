package vertexai

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/vertexai/driver"
)

// GetOperation returns a recorded operation by name.
func (m *Mock) GetOperation(_ context.Context, name string) (*driver.Operation, error) {
	op, ok := m.operations.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "operation %q not found", name)
	}

	out := *op

	return &out, nil
}

// ListOperations returns all operations whose name is scoped under parent
// (projects/{p}/locations/{l}).
func (m *Mock) ListOperations(_ context.Context, parent string) ([]driver.Operation, error) {
	out := make([]driver.Operation, 0)

	for _, op := range m.operations.All() {
		if parent == "" || strings.HasPrefix(op.Name, parent+"/operations/") {
			out = append(out, *op)
		}
	}

	return out, nil
}
