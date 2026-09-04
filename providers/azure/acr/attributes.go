package acr

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/containerregistry/driver"
)

// Compile-time check that Mock implements the full Azure data-plane surface,
// including the changeableAttributes lock.
var _ driver.AzureRepositoryWriter = (*Mock)(nil)

// changeableAttrs is the resolved (never-nil) form of a repository's, tag's, or
// manifest's changeableAttributes. A resource that has never been PATCHed is
// fully enabled, matching real ACR.
type changeableAttrs struct {
	deleteEnabled bool
	writeEnabled  bool
	listEnabled   bool
	readEnabled   bool
}

func defaultChangeableAttrs() changeableAttrs {
	return changeableAttrs{deleteEnabled: true, writeEnabled: true, listEnabled: true, readEnabled: true}
}

// merge applies the non-nil fields of upd onto a, leaving the rest unchanged —
// the semantics of ACR's partial-update PATCH body.
func (a changeableAttrs) merge(upd driver.AzureChangeableAttributes) changeableAttrs {
	if upd.DeleteEnabled != nil {
		a.deleteEnabled = *upd.DeleteEnabled
	}

	if upd.WriteEnabled != nil {
		a.writeEnabled = *upd.WriteEnabled
	}

	if upd.ListEnabled != nil {
		a.listEnabled = *upd.ListEnabled
	}

	if upd.ReadEnabled != nil {
		a.readEnabled = *upd.ReadEnabled
	}

	return a
}

// toDriver resolves a into a fully-populated driver.AzureChangeableAttributes
// (every pointer non-nil), the shape a Get call always returns.
func (a changeableAttrs) toDriver() driver.AzureChangeableAttributes {
	del, wr, ls, rd := a.deleteEnabled, a.writeEnabled, a.listEnabled, a.readEnabled

	return driver.AzureChangeableAttributes{
		DeleteEnabled: &del,
		WriteEnabled:  &wr,
		ListEnabled:   &ls,
		ReadEnabled:   &rd,
	}
}

// tagExists reports whether tag currently points at some image in rd.
func tagExists(rd *repoData, tag string) bool {
	for _, img := range rd.images.All() {
		if hasTag(img.detail.Tags, tag) {
			return true
		}
	}

	return false
}

// tagAttrsOf returns tag's changeableAttributes, defaulting to fully-enabled
// for a tag that has never been PATCHed. ok is false when tag does not
// currently point at any image.
func tagAttrsOf(rd *repoData, tag string) (attrs changeableAttrs, ok bool) {
	if !tagExists(rd, tag) {
		return changeableAttrs{}, false
	}

	if a, found := rd.tagAttrs[tag]; found {
		return a, true
	}

	return defaultChangeableAttrs(), true
}

// checkRepoDeletable returns FailedPrecondition when the repository's
// deleteEnabled attribute is locked.
func checkRepoDeletable(rd *repoData, name string) error {
	if !rd.attrs.deleteEnabled {
		return errors.Newf(errors.FailedPrecondition, "repository %q is delete-locked (deleteEnabled=false)", name)
	}

	return nil
}

// checkRepoWritable returns FailedPrecondition when the repository's
// writeEnabled attribute is locked.
func checkRepoWritable(rd *repoData, name string) error {
	if !rd.attrs.writeEnabled {
		return errors.Newf(errors.FailedPrecondition, "repository %q is write-locked (writeEnabled=false)", name)
	}

	return nil
}

// checkTagWritable returns FailedPrecondition when tag already exists and is
// write-locked. A tag that does not yet exist is always writable (creating a
// new tag is never an "overwrite").
func checkTagWritable(rd *repoData, tag string) error {
	attrs, ok := tagAttrsOf(rd, tag)
	if !ok || attrs.writeEnabled {
		return nil
	}

	return errors.Newf(errors.FailedPrecondition, "tag %q is write-locked (writeEnabled=false)", tag)
}

// checkManifestDeletable returns FailedPrecondition when img's deleteEnabled
// attribute is locked.
func checkManifestDeletable(img *imageData) error {
	if !img.attrs.deleteEnabled {
		return errors.Newf(errors.FailedPrecondition, "manifest %q is delete-locked (deleteEnabled=false)", img.detail.Digest)
	}

	return nil
}

// forgetTag drops tag's per-tag changeableAttributes once it no longer points
// at any image, so a future tag pushed under the same name starts unlocked.
func forgetTag(rd *repoData, tag string) {
	delete(rd.tagAttrs, tag)
}

// GetRepositoryAttributes returns the repository-level changeableAttributes.
func (m *Mock) GetRepositoryAttributes(_ context.Context, repository string) (driver.AzureChangeableAttributes, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	rd, ok := m.repos.Get(repository)
	if !ok {
		return driver.AzureChangeableAttributes{}, errors.Newf(errors.NotFound, "repository %q not found", repository)
	}

	return rd.attrs.toDriver(), nil
}

// UpdateRepositoryAttributes merges attrs onto the repository's
// changeableAttributes (PATCH /acr/v1/{name}).
func (m *Mock) UpdateRepositoryAttributes(
	_ context.Context, repository string, attrs driver.AzureChangeableAttributes,
) (driver.AzureChangeableAttributes, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	rd, ok := m.repos.Get(repository)
	if !ok {
		return driver.AzureChangeableAttributes{}, errors.Newf(errors.NotFound, "repository %q not found", repository)
	}

	rd.attrs = rd.attrs.merge(attrs)

	return rd.attrs.toDriver(), nil
}

// GetTagAttributes returns tag's changeableAttributes.
func (m *Mock) GetTagAttributes(_ context.Context, repository, tag string) (driver.AzureChangeableAttributes, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	rd, ok := m.repos.Get(repository)
	if !ok {
		return driver.AzureChangeableAttributes{}, errors.Newf(errors.NotFound, "repository %q not found", repository)
	}

	attrs, ok := tagAttrsOf(rd, tag)
	if !ok {
		return driver.AzureChangeableAttributes{}, errors.Newf(errors.NotFound, "tag %q not found in repository %q", tag, repository)
	}

	return attrs.toDriver(), nil
}

// UpdateTagAttributes merges attrs onto tag's changeableAttributes (PATCH
// /acr/v1/{name}/_tags/{tag}).
func (m *Mock) UpdateTagAttributes(
	_ context.Context, repository, tag string, attrs driver.AzureChangeableAttributes,
) (driver.AzureChangeableAttributes, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	rd, ok := m.repos.Get(repository)
	if !ok {
		return driver.AzureChangeableAttributes{}, errors.Newf(errors.NotFound, "repository %q not found", repository)
	}

	current, ok := tagAttrsOf(rd, tag)
	if !ok {
		return driver.AzureChangeableAttributes{}, errors.Newf(errors.NotFound, "tag %q not found in repository %q", tag, repository)
	}

	if rd.tagAttrs == nil {
		rd.tagAttrs = make(map[string]changeableAttrs)
	}

	merged := current.merge(attrs)
	rd.tagAttrs[tag] = merged

	return merged.toDriver(), nil
}

// GetManifestAttributes returns the manifest's changeableAttributes.
func (m *Mock) GetManifestAttributes(
	_ context.Context, repository, digest string,
) (driver.AzureChangeableAttributes, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	rd, ok := m.repos.Get(repository)
	if !ok {
		return driver.AzureChangeableAttributes{}, errors.Newf(errors.NotFound, "repository %q not found", repository)
	}

	img := findImage(rd, digest)
	if img == nil {
		return driver.AzureChangeableAttributes{}, errors.Newf(errors.NotFound, "image %q not found in repository %q", digest, repository)
	}

	return img.attrs.toDriver(), nil
}

// UpdateManifestAttributes merges attrs onto the manifest's
// changeableAttributes (PATCH /acr/v1/{name}/_manifests/{digest}).
func (m *Mock) UpdateManifestAttributes(
	_ context.Context, repository, digest string, attrs driver.AzureChangeableAttributes,
) (driver.AzureChangeableAttributes, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	rd, ok := m.repos.Get(repository)
	if !ok {
		return driver.AzureChangeableAttributes{}, errors.Newf(errors.NotFound, "repository %q not found", repository)
	}

	img := findImage(rd, digest)
	if img == nil {
		return driver.AzureChangeableAttributes{}, errors.Newf(errors.NotFound, "image %q not found in repository %q", digest, repository)
	}

	img.attrs = img.attrs.merge(attrs)
	rd.images.Set(img.detail.Digest, img)

	return img.attrs.toDriver(), nil
}
