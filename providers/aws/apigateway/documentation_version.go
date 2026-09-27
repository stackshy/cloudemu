package apigateway

import (
	"context"
	"sort"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/apigateway/driver"
)

const (
	msgDocVersionNotFound = "Invalid Documentation version identifier specified"
	msgDocVersionExists   = "Documentation version already exists"
)

// docVersion is a published documentation version plus the parts it froze.
// Its fields are exported so the snapshot can serialize it directly.
type docVersion struct {
	Version driver.DocumentationVersion         `json:"version"`
	Parts   map[string]driver.DocumentationPart `json:"parts,omitempty"`
}

// CreateDocumentationVersion freezes the API's current parts under a new
// version name, optionally associating it with a stage.
func (m *Mock) CreateDocumentationVersion(
	_ context.Context, restAPIID string, in driver.CreateDocumentationVersionInput,
) (*driver.DocumentationVersion, error) {
	if in.Version == "" {
		return nil, nullError("createDocumentationVersionInput.documentationVersion")
	}

	ad, err := m.getAPI(restAPIID)
	if err != nil {
		return nil, err
	}

	ad.mu.Lock()
	defer ad.mu.Unlock()

	if _, exists := ad.docVersions[in.Version]; exists {
		return nil, cerrors.New(cerrors.AlreadyExists, msgDocVersionExists)
	}

	var stage *driver.Stage

	if in.StageName != "" {
		st, ok := ad.stages[in.StageName]
		if !ok {
			return nil, cerrors.Newf(cerrors.NotFound, "Invalid stage identifier specified %s", in.StageName)
		}

		stage = st
	}

	dv := &docVersion{
		Version: driver.DocumentationVersion{Version: in.Version, Description: in.Description, CreatedDate: m.now()},
		Parts:   make(map[string]driver.DocumentationPart, len(ad.docParts)),
	}

	for id, p := range ad.docParts {
		dv.Parts[id] = *p
	}

	ad.docVersions[in.Version] = dv

	if stage != nil {
		stage.DocumentationVersion = in.Version
	}

	out := dv.Version

	return &out, nil
}

// GetDocumentationVersion returns one version.
func (m *Mock) GetDocumentationVersion(_ context.Context, restAPIID, version string) (*driver.DocumentationVersion, error) {
	ad, err := m.getAPI(restAPIID)
	if err != nil {
		return nil, err
	}

	ad.mu.RLock()
	defer ad.mu.RUnlock()

	dv, ok := ad.docVersions[version]
	if !ok {
		return nil, cerrors.New(cerrors.NotFound, msgDocVersionNotFound)
	}

	out := dv.Version

	return &out, nil
}

// GetDocumentationVersions lists versions oldest first, one page at a time.
func (m *Mock) GetDocumentationVersions(
	_ context.Context, restAPIID string, page driver.PageInput,
) (*driver.DocumentationVersionPage, error) {
	ad, err := m.getAPI(restAPIID)
	if err != nil {
		return nil, err
	}

	ad.mu.RLock()

	all := make([]driver.DocumentationVersion, 0, len(ad.docVersions))
	for _, dv := range ad.docVersions {
		all = append(all, dv.Version)
	}

	ad.mu.RUnlock()

	sort.Slice(all, func(i, j int) bool {
		if all[i].CreatedDate != all[j].CreatedDate {
			return all[i].CreatedDate < all[j].CreatedDate
		}

		return all[i].Version < all[j].Version
	})

	items, next, err := pageOf(all, page)
	if err != nil {
		return nil, err
	}

	return &driver.DocumentationVersionPage{Items: items, Position: next}, nil
}

// UpdateDocumentationVersion applies a patch document; only /description can
// change.
func (m *Mock) UpdateDocumentationVersion(
	_ context.Context, restAPIID, version string, ops []driver.PatchOperation,
) (*driver.DocumentationVersion, error) {
	ad, err := m.getAPI(restAPIID)
	if err != nil {
		return nil, err
	}

	ad.mu.Lock()
	defer ad.mu.Unlock()

	dv, ok := ad.docVersions[version]
	if !ok {
		return nil, cerrors.New(cerrors.NotFound, msgDocVersionNotFound)
	}

	desc := dv.Version.Description

	for _, op := range ops {
		if op.Path != pathDescription {
			return nil, invalidPatchPath(op, pathDescription)
		}

		desc = patchRef(op)
	}

	dv.Version.Description = desc
	out := dv.Version

	return &out, nil
}

// DeleteDocumentationVersion removes a version no stage is associated with.
func (m *Mock) DeleteDocumentationVersion(_ context.Context, restAPIID, version string) error {
	ad, err := m.getAPI(restAPIID)
	if err != nil {
		return err
	}

	ad.mu.Lock()
	defer ad.mu.Unlock()

	if _, ok := ad.docVersions[version]; !ok {
		return cerrors.New(cerrors.NotFound, msgDocVersionNotFound)
	}

	for _, st := range ad.stages {
		if st.DocumentationVersion == version {
			return cerrors.New(cerrors.InvalidArgument,
				"Cannot delete documentation version because there are API Stages associated with it.")
		}
	}

	delete(ad.docVersions, version)

	return nil
}
