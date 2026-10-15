package kendra

import (
	"context"

	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/kendra/driver"
)

// sourceKind binds one family of S3-backed index children (thesauri, query
// suggestions block lists) to its store and wraps its embedded SourceFile, so the
// create / describe / update / list / delete flow is written once.
type sourceKind[T any] struct {
	m     *Mock
	child *child[T]
	store *memstore.Store[T]
	file  func(*T) *driver.SourceFile
	wrap  func(driver.SourceFile) T
}

// sourceCreate is the common input of the create calls.
type sourceCreate struct {
	IndexID     string
	Name        string
	Description string
	RoleArn     string
	S3Path      driver.S3Path
	ClientToken string
	Tags        []driver.Tag
}

// sourceUpdate is the common input of the update calls; a nil member is left
// unchanged.
type sourceUpdate struct {
	IndexID     string
	ID          string
	Name        *string
	Description *string
	RoleArn     *string
	S3Path      *driver.S3Path
}

func newSourceKind[T any](
	m *Mock, kind string, store *memstore.Store[T], file func(*T) *driver.SourceFile, wrap func(driver.SourceFile) T,
) *sourceKind[T] {
	return &sourceKind[T]{
		m: m, store: store, file: file, wrap: wrap,
		child: &child[T]{
			kind: kind, store: store,
			idOf:     func(t *T) string { return file(t).ID },
			indexOf:  func(t *T) string { return file(t).IndexID },
			tokenOf:  func(t *T) string { return file(t).ClientToken },
			statusOf: func(t *T) string { return file(t).Status },
		},
	}
}

func (k *sourceKind[T]) view(t T) T {
	f := k.file(&t)
	f.Tags = copyTags(f.Tags)
	f.Status = k.m.settleStatus(childKey(f.IndexID, f.ID), f.Status)

	return t
}

func (k *sourceKind[T]) create(in *sourceCreate) (T, error) {
	var zero T

	if err := validateIndexID(in.IndexID); err != nil {
		return zero, err
	}

	if err := validateName(in.Name, maxChildName); err != nil {
		return zero, err
	}

	if err := validateRoleArn(in.RoleArn); err != nil {
		return zero, err
	}

	if err := checkS3Path("SourceS3Path", in.S3Path); err != nil {
		return zero, err
	}

	if err := validateCommon(in.ClientToken, in.Description, in.Tags); err != nil {
		return zero, err
	}

	m := k.m
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.requireActiveIndex(in.IndexID); err != nil {
		return zero, err
	}

	if existing, ok := childByToken(k.child, in.IndexID, in.ClientToken); ok {
		return k.view(existing), nil
	}

	id := newUUID()
	now := m.now()
	key := childKey(in.IndexID, id)

	t := k.wrap(driver.SourceFile{
		ID: id, IndexID: in.IndexID, Name: in.Name, Description: in.Description, RoleArn: in.RoleArn,
		SourceS3Path: in.S3Path, Status: driver.ChildStatusActive, ClientToken: in.ClientToken,
		CreatedAt: now, UpdatedAt: now, Tags: copyTags(in.Tags),
	})

	k.store.Set(key, t)
	m.beginSettle(key, driver.ChildStatusCreating)

	return k.view(t), nil
}

func (k *sourceKind[T]) describe(indexID, id string) (T, error) {
	t, err := childGet(k.m, k.child, indexID, id)
	if err != nil {
		return t, err
	}

	return k.view(t), nil
}

func (k *sourceKind[T]) update(in *sourceUpdate) error {
	if err := validateSourceUpdate(in); err != nil {
		return err
	}

	m := k.m
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, err := childRequireActive(m, k.child, in.IndexID, in.ID); err != nil {
		return err
	}

	key := childKey(in.IndexID, in.ID)

	k.store.Update(key, func(t T) T {
		f := k.file(&t)
		applySourceUpdate(f, in)
		f.UpdatedAt = m.now()

		return t
	})
	m.beginSettle(key, driver.ChildStatusUpdating)

	return nil
}

// validateSourceUpdate checks the supplied members of an update.
func validateSourceUpdate(in *sourceUpdate) error {
	if err := validateOptionalChild(in.Name, in.RoleArn, in.Description); err != nil {
		return err
	}

	if in.Name != nil && len(*in.Name) > maxChildName {
		return validation("name must have length between 1 and %d", maxChildName)
	}

	if in.S3Path != nil {
		return checkS3Path("SourceS3Path", *in.S3Path)
	}

	return nil
}

func applySourceUpdate(f *driver.SourceFile, in *sourceUpdate) {
	if in.Name != nil {
		f.Name = *in.Name
	}

	if in.Description != nil {
		f.Description = *in.Description
	}

	if in.RoleArn != nil {
		f.RoleArn = *in.RoleArn
	}

	if in.S3Path != nil {
		f.SourceS3Path = *in.S3Path
	}
}

func (k *sourceKind[T]) list(indexID string, page driver.Page) (items []T, next string, err error) {
	stored, next, err := childList(k.m, k.child, indexID, page, maxPageSize)
	if err != nil {
		return nil, "", err
	}

	out := make([]T, len(stored))
	for i := range stored {
		out[i] = k.view(stored[i])
	}

	return out, next, nil
}

func (k *sourceKind[T]) remove(indexID, id string) error {
	m := k.m
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, err := childRequireActive(m, k.child, indexID, id); err != nil {
		return err
	}

	k.store.Delete(childKey(indexID, id))
	m.settling.Clear(childKey(indexID, id))

	return nil
}

func (m *Mock) thesauriKind() *sourceKind[driver.Thesaurus] {
	return newSourceKind(m, "thesaurus", m.thesauri,
		func(t *driver.Thesaurus) *driver.SourceFile { return &t.SourceFile },
		func(f driver.SourceFile) driver.Thesaurus { return driver.Thesaurus{SourceFile: f} })
}

func (m *Mock) blockListKind() *sourceKind[driver.BlockList] {
	return newSourceKind(m, "query suggestions block list", m.blockLists,
		func(b *driver.BlockList) *driver.SourceFile { return &b.SourceFile },
		func(f driver.SourceFile) driver.BlockList { return driver.BlockList{SourceFile: f} })
}

// CreateThesaurus registers a synonym file with an index. The S3 object is not
// read, so FileSizeBytes, TermCount and SynonymRuleCount are 0. A repeated
// ClientToken returns the first thesaurus.
func (m *Mock) CreateThesaurus(_ context.Context, in *driver.CreateThesaurusInput) (*driver.Thesaurus, error) {
	t, err := m.thesauriKind().create(&sourceCreate{
		IndexID: in.IndexID, Name: in.Name, Description: in.Description, RoleArn: in.RoleArn,
		S3Path: in.SourceS3Path, ClientToken: in.ClientToken, Tags: in.Tags,
	})
	if err != nil {
		return nil, err
	}

	return &t, nil
}

// DescribeThesaurus returns a thesaurus by index and id.
func (m *Mock) DescribeThesaurus(_ context.Context, indexID, id string) (*driver.Thesaurus, error) {
	t, err := m.thesauriKind().describe(indexID, id)
	if err != nil {
		return nil, err
	}

	return &t, nil
}

// UpdateThesaurus applies the supplied fields, leaving omitted ones unchanged.
func (m *Mock) UpdateThesaurus(_ context.Context, in *driver.UpdateThesaurusInput) error {
	return m.thesauriKind().update(&sourceUpdate{
		IndexID: in.IndexID, ID: in.ID, Name: in.Name, Description: in.Description, RoleArn: in.RoleArn, S3Path: in.SourceS3Path,
	})
}

// ListThesauri returns a deterministic page of an index's thesauri ordered by id.
func (m *Mock) ListThesauri(_ context.Context, indexID string, page driver.Page) ([]driver.Thesaurus, string, error) {
	return m.thesauriKind().list(indexID, page)
}

// DeleteThesaurus removes a thesaurus.
func (m *Mock) DeleteThesaurus(_ context.Context, indexID, id string) error {
	return m.thesauriKind().remove(indexID, id)
}

// CreateQuerySuggestionsBlockList registers a block list file with an index. The
// S3 object is not read, so FileSizeBytes and ItemCount are 0. A repeated
// ClientToken returns the first block list.
func (m *Mock) CreateQuerySuggestionsBlockList(_ context.Context, in *driver.CreateBlockListInput) (*driver.BlockList, error) {
	b, err := m.blockListKind().create(&sourceCreate{
		IndexID: in.IndexID, Name: in.Name, Description: in.Description, RoleArn: in.RoleArn,
		S3Path: in.SourceS3Path, ClientToken: in.ClientToken, Tags: in.Tags,
	})
	if err != nil {
		return nil, err
	}

	return &b, nil
}

// DescribeQuerySuggestionsBlockList returns a block list by index and id.
func (m *Mock) DescribeQuerySuggestionsBlockList(_ context.Context, indexID, id string) (*driver.BlockList, error) {
	b, err := m.blockListKind().describe(indexID, id)
	if err != nil {
		return nil, err
	}

	return &b, nil
}

// UpdateQuerySuggestionsBlockList applies the supplied fields, leaving omitted
// ones unchanged.
func (m *Mock) UpdateQuerySuggestionsBlockList(_ context.Context, in *driver.UpdateBlockListInput) error {
	return m.blockListKind().update(&sourceUpdate{
		IndexID: in.IndexID, ID: in.ID, Name: in.Name, Description: in.Description, RoleArn: in.RoleArn, S3Path: in.SourceS3Path,
	})
}

// ListQuerySuggestionsBlockLists returns a deterministic page of an index's block
// lists ordered by id.
func (m *Mock) ListQuerySuggestionsBlockLists(_ context.Context, indexID string, page driver.Page) ([]driver.BlockList, string, error) {
	return m.blockListKind().list(indexID, page)
}

// DeleteQuerySuggestionsBlockList removes a block list.
func (m *Mock) DeleteQuerySuggestionsBlockList(_ context.Context, indexID, id string) error {
	return m.blockListKind().remove(indexID, id)
}
