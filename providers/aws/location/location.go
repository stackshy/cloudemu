// Package location provides an in-memory mock of the Amazon Location Service
// control plane: maps, place indexes, route calculators, geofence collections
// and trackers, plus their resource tags. Each resource is created immediately
// with a stable ARN, CreateTime and UpdateTime; an update mutates only the
// requested fields and bumps UpdateTime, so repeated reads and IaC plans never
// drift. The Location data plane (geocoding, routing, geofence evaluation,
// device positions, map tiles) is out of scope: this is a control-plane-only
// surface.
package location

import (
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/location/driver"
)

// Compile-time check that Mock implements driver.Location.
var _ driver.Location = (*Mock)(nil)

// defaultMaxResults caps a list page when the caller requests none.
const defaultMaxResults = 100

// ARN resource-type prefixes for the five Location resource types.
const (
	kindMap                = "map"
	kindPlaceIndex         = "place-index"
	kindRouteCalculator    = "route-calculator"
	kindGeofenceCollection = "geofence-collection"
	kindTracker            = "tracker"
)

// arnFields is the colon-delimited field count of an AWS ARN; the sixth field
// holds the "<kind>/<name>" resource part. resourceParts is the "<kind>/<name>"
// split count.
const (
	arnFields     = 6
	resourceParts = 2
)

// Mock is an in-memory implementation of the Amazon Location Service control
// plane.
type Mock struct {
	maps        *memstore.Store[driver.MapInfo]
	indexes     *memstore.Store[driver.PlaceIndexInfo]
	calculators *memstore.Store[driver.RouteCalculatorInfo]
	collections *memstore.Store[driver.GeofenceCollectionInfo]
	trackers    *memstore.Store[driver.TrackerInfo]
	opts        *config.Options
}

// New creates a new Location mock with the given configuration options.
func New(opts *config.Options) *Mock {
	return &Mock{
		maps:        memstore.New[driver.MapInfo](),
		indexes:     memstore.New[driver.PlaceIndexInfo](),
		calculators: memstore.New[driver.RouteCalculatorInfo](),
		collections: memstore.New[driver.GeofenceCollectionInfo](),
		trackers:    memstore.New[driver.TrackerInfo](),
		opts:        opts,
	}
}

func (m *Mock) now() time.Time {
	return m.opts.Clock.Now().UTC()
}

// arn mints the stable ARN for a resource of the given kind and name, e.g.
// arn:aws:geo:<region>:<acct>:map/<name>.
func (m *Mock) arn(kind, name string) string {
	return idgen.AWSARN("geo", m.opts.Region, m.opts.AccountID, kind+"/"+name)
}

// newMeta builds the shared metadata for a freshly created resource: its ARN,
// description, matching create/update timestamps and a copy of the caller tags.
func (m *Mock) newMeta(kind, name, description string, tags map[string]string) driver.Meta {
	now := m.now()

	return driver.Meta{
		Name:        name,
		Arn:         m.arn(kind, name),
		Description: description,
		CreateTime:  now,
		UpdateTime:  now,
		Tags:        copyTags(tags),
	}
}

// parseARN extracts the resource kind and name from a Location ARN of the form
// arn:aws:geo:<region>:<acct>:<kind>/<name>.
func parseARN(arn string) (kind, name string, ok bool) {
	parts := strings.SplitN(arn, ":", arnFields)
	if len(parts) < arnFields {
		return "", "", false
	}

	seg := strings.SplitN(parts[arnFields-1], "/", resourceParts)
	if len(seg) != resourceParts || seg[1] == "" {
		return "", "", false
	}

	switch seg[0] {
	case kindMap, kindPlaceIndex, kindRouteCalculator, kindGeofenceCollection, kindTracker:
		return seg[0], seg[1], true
	default:
		return "", "", false
	}
}

func copyTags(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}

	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}

	return out
}

// cloneMeta returns an alias-free copy of a resource's metadata so a caller
// cannot mutate stored tags through the result.
func cloneMeta(meta *driver.Meta) driver.Meta {
	out := *meta
	out.Tags = copyTags(meta.Tags)

	return out
}

// applyUpdate applies a mutation to the stored resource identified by name,
// bumping UpdateTime, and returns a clone. It reports notFound when the resource
// is absent. getMeta yields the embedded Meta pointer for the concrete type.
func applyUpdate[T any](
	m *Mock,
	store *memstore.Store[T],
	kind, name string,
	getMeta func(*T) *driver.Meta,
	mutate func(*T),
	clone func(*T) T,
) (*T, error) {
	now := m.now()

	ok := store.Update(name, func(v T) T {
		mutate(&v)

		getMeta(&v).UpdateTime = now

		return v
	})
	if !ok {
		return nil, notFound(kind, name)
	}

	got, _ := store.Get(name)
	out := clone(&got)

	return &out, nil
}

// describeEntity returns a clone of the stored resource, or notFound.
func describeEntity[T any](store *memstore.Store[T], kind, name string, clone func(*T) T) (*T, error) {
	v, ok := store.Get(name)
	if !ok {
		return nil, notFound(kind, name)
	}

	out := clone(&v)

	return &out, nil
}

// deleteEntity removes the named resource, or reports notFound.
func deleteEntity[T any](store *memstore.Store[T], kind, name string) error {
	if !store.Delete(name) {
		return notFound(kind, name)
	}

	return nil
}

// listEntities returns a deterministic page of the store's values ordered by
// name, plus the next-page token.
func listEntities[T any](store *memstore.Store[T], page driver.Page, clone func(*T) T) (items []T, next string) {
	vals := store.SortedValues()
	start, end, token := paginate(len(vals), page)

	out := make([]T, 0, end-start)
	for i := start; i < end; i++ {
		out = append(out, clone(&vals[i]))
	}

	return out, token
}

// lookupMeta returns a copy of the stored resource's metadata, or notFound.
func lookupMeta[T any](store *memstore.Store[T], kind, name string, getMeta func(*T) *driver.Meta) (driver.Meta, error) {
	v, ok := store.Get(name)
	if !ok {
		return driver.Meta{}, notFound(kind, name)
	}

	return *getMeta(&v), nil
}

// tagStore folds a resource's tags via fn, returning notFound when absent.
func tagStore[T any](store *memstore.Store[T], name string, getMeta func(*T) *driver.Meta, fn func(map[string]string)) error {
	ok := store.Update(name, func(v T) T {
		meta := getMeta(&v)
		if meta.Tags == nil {
			meta.Tags = map[string]string{}
		}

		fn(meta.Tags)

		return v
	})
	if !ok {
		return notFound("resource", name)
	}

	return nil
}
