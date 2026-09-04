package blobstorage

import (
	"context"
	"maps"
	"sort"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// Compile-time check that Mock satisfies the optional AzureFindBlobsByTags
// capability the blob wire handler reaches by type assertion.
var _ driver.AzureFindBlobsByTags = (*Mock)(nil)

// FindBlobsByTags returns every live blob whose index tags satisfy all of the
// equality conditions in match. A blank container searches the whole account;
// otherwise the search is scoped to that one container. Results are ordered by
// container then blob name so a listing is deterministic.
func (m *Mock) FindBlobsByTags(_ context.Context, container string, match map[string]string) ([]driver.TaggedBlob, error) {
	var names []string

	if container == "" {
		names = m.containers.Keys()
	} else {
		if _, ok := m.containers.Get(container); !ok {
			return nil, cerrors.Newf(cerrors.NotFound, "container %q not found", container)
		}

		names = []string{container}
	}

	sort.Strings(names)

	var out []driver.TaggedBlob

	for _, name := range names {
		ctr, ok := m.containers.Get(name)
		if !ok {
			continue
		}

		out = append(out, matchingBlobs(name, ctr, match)...)
	}

	return out, nil
}

// matchingBlobs collects the live blobs in one container whose tags satisfy
// every condition in match, ordered by blob name.
func matchingBlobs(container string, ctr *containerMeta, match map[string]string) []driver.TaggedBlob {
	keys := ctr.objects.Keys()
	sort.Strings(keys)

	var out []driver.TaggedBlob

	for _, key := range keys {
		obj, ok := ctr.objects.Get(key)
		if !ok || !tagsSatisfy(obj.Tags, match) {
			continue
		}

		out = append(out, driver.TaggedBlob{
			Container: container, Name: key, Tags: maps.Clone(obj.Tags),
		})
	}

	return out
}

// tagsSatisfy reports whether tags contains every key/value pair in match. An
// empty match is satisfied by any blob that carries at least one tag (Azure's
// Find Blobs by Tags only ever returns tagged blobs).
func tagsSatisfy(tags, match map[string]string) bool {
	if len(tags) == 0 {
		return false
	}

	for k, v := range match {
		if tags[k] != v {
			return false
		}
	}

	return true
}
