package athena

import (
	"context"
	"sort"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/athena/driver"
)

// CreateNamedQuery saves a query and returns its generated id. The QueryString
// is stored byte-exact.
//
//nolint:gocritic // hugeParam: taken by value to match the driver interface / copy semantics
func (m *Mock) CreateNamedQuery(_ context.Context, nq driver.NamedQuery) (string, error) {
	if !validWorkGroupName(nq.Name) {
		return "", invalidRequest("named query name %q is invalid", nq.Name)
	}

	if nq.Database == "" {
		return "", invalidRequest("named query requires a Database")
	}

	if nq.WorkGroup == "" {
		nq.WorkGroup = driver.DefaultWorkGroup
	}

	if _, ok := m.workGroups.Get(nq.WorkGroup); !ok {
		return "", notFoundRequest("WorkGroup %s is not found", nq.WorkGroup)
	}

	nq.NamedQueryID = idgen.UUID()
	m.namedQueries.Set(nq.NamedQueryID, nq)

	return nq.NamedQueryID, nil
}

// GetNamedQuery returns a saved query by id.
func (m *Mock) GetNamedQuery(_ context.Context, id string) (*driver.NamedQuery, error) {
	nq, ok := m.namedQueries.Get(id)
	if !ok {
		return nil, notFoundRequest("NamedQuery %s is not found", id)
	}

	out := nq

	return &out, nil
}

// DeleteNamedQuery removes a saved query by id.
func (m *Mock) DeleteNamedQuery(_ context.Context, id string) error {
	if !m.namedQueries.Delete(id) {
		return notFoundRequest("NamedQuery %s is not found", id)
	}

	return nil
}

// ListNamedQueries returns the ids of saved queries in a workgroup (default
// "primary") in deterministic (id-sorted) order.
//
//nolint:gocritic // unnamedResult: (ids, nextToken, err) is idiomatic and self-explanatory
func (m *Mock) ListNamedQueries(_ context.Context, workGroup string, page driver.Pagination) ([]string, string, error) {
	if workGroup == "" {
		workGroup = driver.DefaultWorkGroup
	}

	ids := make([]string, 0)

	for id, nq := range m.namedQueries.All() {
		if nq.WorkGroup == workGroup {
			ids = append(ids, id)
		}
	}

	sort.Strings(ids)

	return paginate(ids, page)
}
