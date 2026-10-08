package kendra

import (
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/kendra/driver"
)

// child describes one family of index child resources (FAQs, thesauri, block
// lists, experiences, access control configurations, featured results sets) so
// get/list/token lookup are written once. Every child is stored under
// "indexId/childId".
type child[T any] struct {
	kind     string
	store    *memstore.Store[T]
	idOf     func(*T) string
	indexOf  func(*T) string
	tokenOf  func(*T) string
	statusOf func(*T) string // empty when the family has no status
}

func childKey(indexID, id string) string { return indexID + "/" + id }

// childGet looks a child up by index and id.
func childGet[T any](m *Mock, c *child[T], indexID, id string) (T, error) {
	var zero T

	if err := validateIndexID(indexID); err != nil {
		return zero, err
	}

	if err := validateChildID("id", id, false); err != nil {
		return zero, err
	}

	if _, ok := m.indexes.Get(indexID); !ok {
		return zero, notFound("index with id %q does not exist", indexID)
	}

	v, ok := c.store.Get(childKey(indexID, id))
	if !ok {
		return zero, notFound("%s %q does not exist for index %q", c.kind, id, indexID)
	}

	return v, nil
}

// childRequireActive is childGet that also rejects a child that is still
// settling with a ConflictException.
func childRequireActive[T any](m *Mock, c *child[T], indexID, id string) (T, error) {
	v, err := childGet(m, c, indexID, id)
	if err != nil {
		return v, err
	}

	if c.statusOf != nil {
		if status := m.settleStatus(childKey(indexID, id), c.statusOf(&v)); status != driver.ChildStatusActive {
			return v, conflict("%s %q is %s; try again when it is ACTIVE", c.kind, id, status)
		}
	}

	return v, nil
}

// childByToken returns the child a previous create under the index made with the
// same client token; an empty token never matches.
func childByToken[T any](c *child[T], indexID, token string) (found T, ok bool) {
	if token == "" {
		return found, false
	}

	all := c.store.SortedValues()
	for i := range all {
		if c.indexOf(&all[i]) == indexID && c.tokenOf(&all[i]) == token {
			return all[i], true
		}
	}

	return found, false
}

// childList returns the children of an index ordered by id, paged.
func childList[T any](m *Mock, c *child[T], indexID string, page driver.Page, maxAllowed int32) (items []T, next string, err error) {
	if _, ierr := m.getIndex(indexID); ierr != nil {
		return nil, "", ierr
	}

	all := c.store.SortedValues()
	matched := make([]T, 0, len(all))

	for i := range all {
		if c.indexOf(&all[i]) == indexID {
			matched = append(matched, all[i])
		}
	}

	start, end, next, err := m.paginate(c.kind+"/"+indexID, len(matched), page, maxAllowed)
	if err != nil {
		return nil, "", err
	}

	return matched[start:end], next, nil
}

// childCount returns how many children an index has.
func childCount[T any](c *child[T], indexID string) int {
	n := 0

	all := c.store.SortedValues()
	for i := range all {
		if c.indexOf(&all[i]) == indexID {
			n++
		}
	}

	return n
}

// checkS3Path validates an S3 location the way the API does (bucket 3-63
// characters, key 1-1024 characters).
func checkS3Path(field string, p driver.S3Path) error {
	if len(p.Bucket) < 3 || len(p.Bucket) > 63 {
		return validation("%s Bucket must have length between 3 and 63", field)
	}

	if len(p.Key) < 1 || len(p.Key) > 1024 {
		return validation("%s Key must have length between 1 and 1024", field)
	}

	return nil
}
