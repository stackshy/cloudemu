package kendra

import (
	"context"
	"errors"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/kendra/driver"
)

// Documented featured results limits.
const (
	maxFeaturedQueryTexts = 49
	maxFeaturedSets       = 50
	maxFeaturedName       = 1000
)

func (m *Mock) featuredChild() *child[driver.FeaturedResultsSet] {
	return &child[driver.FeaturedResultsSet]{
		kind: "featured results set", store: m.featured,
		idOf:    func(f *driver.FeaturedResultsSet) string { return f.ID },
		indexOf: func(f *driver.FeaturedResultsSet) string { return f.IndexID },
		tokenOf: func(f *driver.FeaturedResultsSet) string { return f.ClientToken },
	}
}

func copyFeatured(f *driver.FeaturedResultsSet) driver.FeaturedResultsSet {
	out := *f
	out.QueryTexts = append([]string(nil), f.QueryTexts...)
	out.FeaturedDocuments = append([]string(nil), f.FeaturedDocuments...)
	out.Tags = copyTags(f.Tags)

	return out
}

func validFeaturedName(name string) error {
	if len(name) < 1 || len(name) > maxFeaturedName || !featuredNameRE.MatchString(name) {
		return validation("FeaturedResultsSetName must have length between 1 and %d and match [a-zA-Z0-9][ a-zA-Z0-9_-]*", maxFeaturedName)
	}

	return nil
}

func validFeaturedStatus(s string) error {
	if s != "" && s != driver.FeaturedActive && s != driver.FeaturedInactive {
		return validation("invalid Status: %q", s)
	}

	return nil
}

// checkQueryTexts enforces the per-set limit and that the texts are unique within
// the set.
func checkQueryTexts(texts []string) error {
	if len(texts) > maxFeaturedQueryTexts {
		return validation("QueryTexts must have at most %d items", maxFeaturedQueryTexts)
	}

	seen := map[string]bool{}

	for _, t := range texts {
		k := strings.ToLower(t)
		if seen[k] {
			return validation("QueryTexts contains the duplicate query %q", t)
		}

		seen[k] = true
	}

	return nil
}

// conflictingQueries returns every query text that another set (other than
// skipID) of the index already features, with the set that holds it; query texts
// must be unique per index across all sets.
func (m *Mock) conflictingQueries(indexID, skipID string, texts []string) []driver.ConflictingItem {
	var out []driver.ConflictingItem

	sets := m.featured.SortedValues()

	for i := range sets {
		if sets[i].IndexID != indexID || sets[i].ID == skipID {
			continue
		}

		for _, have := range sets[i].QueryTexts {
			for _, want := range texts {
				if strings.EqualFold(have, want) {
					out = append(out, driver.ConflictingItem{QueryText: want, SetName: sets[i].Name, SetID: sets[i].ID})
				}
			}
		}
	}

	return out
}

// featuredConflict builds the FeaturedResultsConflictException listing every
// query text that another set of the index already uses.
func featuredConflict(items []driver.ConflictingItem) error {
	first := items[0]
	err := conflictErr(driver.ExFeaturedConflict,
		"the query %q is already used by the featured results set %q (%s)", first.QueryText, first.SetName, first.SetID)

	var apiErr *driver.APIError
	if errors.As(err, &apiErr) {
		apiErr.ConflictingItems = items
	}

	return err
}

// CreateFeaturedResultsSet creates a set of featured documents for query texts.
// Query texts must be unique across the index's sets (FeaturedResultsConflictException)
// and an index holds at most 50 sets. Status defaults to ACTIVE. A repeated
// ClientToken returns the first set.
func (m *Mock) CreateFeaturedResultsSet(
	_ context.Context, in *driver.CreateFeaturedResultsSetInput,
) (*driver.FeaturedResultsSet, error) {
	if err := validateCreateFeatured(in); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.requireActiveIndex(in.IndexID); err != nil {
		return nil, err
	}

	if existing, ok := childByToken(m.featuredChild(), in.IndexID, in.ClientToken); ok {
		out := copyFeatured(&existing)

		return &out, nil
	}

	if childCount(m.featuredChild(), in.IndexID) >= maxFeaturedSets {
		return nil, validation("an index can hold at most %d featured results sets", maxFeaturedSets)
	}

	if conflicts := m.conflictingQueries(in.IndexID, "", in.QueryTexts); len(conflicts) > 0 {
		return nil, featuredConflict(conflicts)
	}

	status := in.Status
	if status == "" {
		status = driver.FeaturedActive
	}

	id := newUUID()
	now := m.now()

	f := driver.FeaturedResultsSet{
		ID: id, IndexID: in.IndexID, Name: in.Name, Description: in.Description, Status: status,
		QueryTexts: append([]string(nil), in.QueryTexts...), FeaturedDocuments: append([]string(nil), in.FeaturedDocuments...),
		ClientToken: in.ClientToken, CreatedAt: now, UpdatedAt: now, Tags: copyTags(in.Tags),
	}

	m.featured.Set(childKey(in.IndexID, id), f)

	out := copyFeatured(&f)

	return &out, nil
}

// validateCreateFeatured applies CreateFeaturedResultsSet's input rules.
func validateCreateFeatured(in *driver.CreateFeaturedResultsSetInput) error {
	if err := validateIndexID(in.IndexID); err != nil {
		return err
	}

	if err := validFeaturedName(in.Name); err != nil {
		return err
	}

	if err := validFeaturedStatus(in.Status); err != nil {
		return err
	}

	if err := checkQueryTexts(in.QueryTexts); err != nil {
		return err
	}

	return validateCommon(in.ClientToken, in.Description, in.Tags)
}

// DescribeFeaturedResultsSet returns a set with its featured documents resolved
// against the index: documents the index holds come back with their title and
// URI, the others are reported missing.
func (m *Mock) DescribeFeaturedResultsSet(
	_ context.Context, indexID, id string,
) (*driver.FeaturedResultsSetView, error) {
	f, err := childGet(m, m.featuredChild(), indexID, id)
	if err != nil {
		return nil, err
	}

	view := &driver.FeaturedResultsSetView{
		FeaturedResultsSet:    copyFeatured(&f),
		DocumentsWithMetadata: []driver.FeaturedDocumentRef{},
		DocumentsMissing:      []string{},
	}

	for _, docID := range f.FeaturedDocuments {
		if d, ok := m.documents.Get(documentKey(indexID, docID)); ok {
			view.DocumentsWithMetadata = append(view.DocumentsWithMetadata,
				driver.FeaturedDocumentRef{ID: d.ID, Title: d.Title, URI: d.uri()})
		} else {
			view.DocumentsMissing = append(view.DocumentsMissing, docID)
		}
	}

	return view, nil
}

// UpdateFeaturedResultsSet applies the supplied fields, leaving omitted ones
// unchanged, and returns the updated set.
func (m *Mock) UpdateFeaturedResultsSet(
	_ context.Context, in *driver.UpdateFeaturedResultsSetInput,
) (*driver.FeaturedResultsSet, error) {
	if err := validateUpdateFeatured(in); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, err := childGet(m, m.featuredChild(), in.IndexID, in.ID); err != nil {
		return nil, err
	}

	if in.QueryTextsSet {
		if conflicts := m.conflictingQueries(in.IndexID, in.ID, in.QueryTexts); len(conflicts) > 0 {
			return nil, featuredConflict(conflicts)
		}
	}

	key := childKey(in.IndexID, in.ID)

	m.featured.Update(key, func(f driver.FeaturedResultsSet) driver.FeaturedResultsSet {
		applyFeaturedUpdate(&f, in)
		f.UpdatedAt = m.now()

		return f
	})

	f, _ := m.featured.Get(key)
	out := copyFeatured(&f)

	return &out, nil
}

// validateUpdateFeatured applies UpdateFeaturedResultsSet's input rules; each
// member is checked only when supplied.
func validateUpdateFeatured(in *driver.UpdateFeaturedResultsSetInput) error {
	if err := validateIndexID(in.IndexID); err != nil {
		return err
	}

	if in.Name != nil {
		if err := validFeaturedName(*in.Name); err != nil {
			return err
		}
	}

	if in.Status != nil {
		if err := validFeaturedStatus(*in.Status); err != nil {
			return err
		}
	}

	if in.QueryTextsSet {
		if err := checkQueryTexts(in.QueryTexts); err != nil {
			return err
		}
	}

	if in.Description != nil && len(*in.Description) > maxDescriptionLen {
		return validation("Description must have length between 0 and %d", maxDescriptionLen)
	}

	return nil
}

func applyFeaturedUpdate(f *driver.FeaturedResultsSet, in *driver.UpdateFeaturedResultsSetInput) {
	if in.Name != nil {
		f.Name = *in.Name
	}

	if in.Description != nil {
		f.Description = *in.Description
	}

	if in.Status != nil && *in.Status != "" {
		f.Status = *in.Status
	}

	if in.QueryTextsSet {
		f.QueryTexts = append([]string(nil), in.QueryTexts...)
	}

	if in.DocumentsSet {
		f.FeaturedDocuments = append([]string(nil), in.FeaturedDocuments...)
	}
}

// ListFeaturedResultsSets returns a deterministic page of an index's sets.
func (m *Mock) ListFeaturedResultsSets(
	_ context.Context, indexID string, page driver.Page,
) (sets []driver.FeaturedResultsSet, nextToken string, err error) {
	items, next, err := childList(m, m.featuredChild(), indexID, page, maxPageSize)
	if err != nil {
		return nil, "", err
	}

	out := make([]driver.FeaturedResultsSet, 0, len(items))
	for i := range items {
		out = append(out, copyFeatured(&items[i]))
	}

	return out, next, nil
}

// BatchDeleteFeaturedResultsSet deletes sets by id and reports, per id, the ones
// it could not delete (unknown or malformed ids) instead of failing the batch.
func (m *Mock) BatchDeleteFeaturedResultsSet(
	_ context.Context, indexID string, ids []string,
) ([]driver.BatchDeleteError, error) {
	if err := validateIndexID(indexID); err != nil {
		return nil, err
	}

	if len(ids) < 1 {
		return nil, validation("FeaturedResultsSetIds must have at least 1 item")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.requireActiveIndex(indexID); err != nil {
		return nil, err
	}

	errs := []driver.BatchDeleteError{}

	for _, id := range ids {
		if m.featured.Delete(childKey(indexID, id)) {
			continue
		}

		errs = append(errs, driver.BatchDeleteError{
			ID: id, ErrorCode: driver.ErrCodeInvalidRequest,
			ErrorMessage: "featured results set " + id + " does not exist for index " + indexID,
		})
	}

	return errs, nil
}
