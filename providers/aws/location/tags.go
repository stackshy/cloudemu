package location

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/location/driver"
)

// TagResource merges tags onto the resource identified by its ARN.
func (m *Mock) TagResource(_ context.Context, resourceARN string, tags map[string]string) error {
	kind, name, ok := parseARN(resourceARN)
	if !ok {
		return validation("invalid resource ARN: %s", resourceARN)
	}

	apply := func(existing map[string]string) {
		for k, v := range tags {
			existing[k] = v
		}
	}

	return m.mutateTags(kind, name, apply)
}

// UntagResource removes the given tag keys from the resource.
func (m *Mock) UntagResource(_ context.Context, resourceARN string, tagKeys []string) error {
	kind, name, ok := parseARN(resourceARN)
	if !ok {
		return validation("invalid resource ARN: %s", resourceARN)
	}

	apply := func(existing map[string]string) {
		for _, k := range tagKeys {
			delete(existing, k)
		}
	}

	return m.mutateTags(kind, name, apply)
}

// ListTagsForResource returns a copy of the resource's tags.
func (m *Mock) ListTagsForResource(_ context.Context, resourceARN string) (map[string]string, error) {
	kind, name, ok := parseARN(resourceARN)
	if !ok {
		return nil, validation("invalid resource ARN: %s", resourceARN)
	}

	meta, err := m.metaFor(kind, name)
	if err != nil {
		return nil, err
	}

	return copyTags(meta.Tags), nil
}

// mutateTags routes a tag mutation to the store holding the resource kind.
func (m *Mock) mutateTags(kind, name string, fn func(map[string]string)) error {
	switch kind {
	case kindMap:
		return tagStore(m.maps, name, mapMeta, fn)
	case kindPlaceIndex:
		return tagStore(m.indexes, name, placeIndexMeta, fn)
	case kindRouteCalculator:
		return tagStore(m.calculators, name, routeCalculatorMeta, fn)
	case kindGeofenceCollection:
		return tagStore(m.collections, name, geofenceCollectionMeta, fn)
	case kindTracker:
		return tagStore(m.trackers, name, trackerMeta, fn)
	default:
		return notFound("resource", name)
	}
}

// metaFor returns the stored metadata of the resource identified by kind+name.
func (m *Mock) metaFor(kind, name string) (driver.Meta, error) {
	switch kind {
	case kindMap:
		return lookupMeta(m.maps, kind, name, mapMeta)
	case kindPlaceIndex:
		return lookupMeta(m.indexes, kind, name, placeIndexMeta)
	case kindRouteCalculator:
		return lookupMeta(m.calculators, kind, name, routeCalculatorMeta)
	case kindGeofenceCollection:
		return lookupMeta(m.collections, kind, name, geofenceCollectionMeta)
	case kindTracker:
		return lookupMeta(m.trackers, kind, name, trackerMeta)
	default:
		return driver.Meta{}, notFound("resource", name)
	}
}
