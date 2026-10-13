package sagemaker

import (
	"sort"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/internal/pagination"
	"github.com/stackshy/cloudemu/v2/services/sagemaker/driver"
)

// listSpec describes how to filter, sort and copy one resource type for the
// shared List pipeline. Unset accessors disable the matching filter/sort key.
type listSpec[T any] struct {
	name     func(*T) string
	status   func(*T) string
	created  func(*T) string
	modified func(*T) string
	// extra applies operation-specific equality filters (opts.Filters).
	extra func(*T, map[string]string) bool
	// clone returns an independent copy of an item. Nil means a shallow copy.
	clone func(*T) T
	// keys adds operation-specific sort keys (SortBy values).
	keys map[string]func(*T) string
	// defaultOrder is the SortOrder used when the request names none; empty
	// means ascending.
	defaultOrder string
	// defaultSortBy is the SortBy used when the request names none; empty means
	// CreationTime (Name when the resource has no creation time).
	defaultSortBy string
}

// listFrom is the single List implementation every SageMaker List operation
// uses: stable key order, filters, sort, then offset paging (bad token ->
// InvalidArgument, never page 1). MaxResults 0 means "all items".
//
//nolint:gocritic // large value params mirror the driver interface
func listFrom[T any](store *memstore.Store[*T], spec listSpec[T], opts driver.ListOptions) (driver.ListPage[T], error) {
	return listItems(store.SortedValues(), spec, opts)
}

// listItems is listFrom over an already-materialized slice, for resources whose
// observed state (status overlays) differs from what the store holds.
//
//nolint:gocritic,gocyclo // large value params mirror the driver interface; validation branches mirror the service's rules
func listItems[T any](all []*T, spec listSpec[T], opts driver.ListOptions) (driver.ListPage[T], error) {
	if opts.MaxResults < 0 || opts.MaxResults > driver.MaxListResults {
		return driver.ListPage[T]{}, errors.Newf(errors.InvalidArgument,
			"1 validation error detected: Value '%d' at 'maxResults' failed to satisfy constraint: Member must have value between 1 and %d",
			opts.MaxResults, driver.MaxListResults)
	}

	sortBy, desc, err := resolveSort(spec, opts)
	if err != nil {
		return driver.ListPage[T]{}, err
	}

	f, err := newListFilter(opts)
	if err != nil {
		return driver.ListPage[T]{}, err
	}

	if err = checkFilterSupport(&f, spec); err != nil {
		return driver.ListPage[T]{}, err
	}

	matched := make([]*T, 0, len(all))

	for _, item := range all {
		if matchItem(f, spec, item) {
			matched = append(matched, item)
		}
	}

	sortItems(matched, spec, sortBy, desc)

	size := opts.MaxResults
	if size == 0 {
		size = len(matched)
	}

	if size == 0 {
		size = 1
	}

	page, err := pagination.Paginate(matched, opts.NextToken, size)
	if err != nil {
		return driver.ListPage[T]{}, errors.New(errors.InvalidArgument, "Invalid value for NextToken")
	}

	out := make([]T, 0, len(page.Items))
	for _, item := range page.Items {
		out = append(out, copyListItem(spec, item))
	}

	return driver.ListPage[T]{Items: out, NextToken: page.NextPageToken}, nil
}

func copyListItem[T any](spec listSpec[T], item *T) T {
	if spec.clone != nil {
		return spec.clone(item)
	}

	return *item
}

// resolveSort validates SortBy/SortOrder against what the spec supports.
//
//nolint:gocritic,gocyclo // large value params mirror the driver interface; validation branches mirror the service's rules
func resolveSort[T any](spec listSpec[T], opts driver.ListOptions) (string, bool, error) {
	sortBy := opts.SortBy
	if sortBy == "" {
		sortBy = spec.defaultSortBy
	}

	if sortBy == "" {
		sortBy = driver.SortByCreationTime
		if spec.created == nil {
			sortBy = driver.SortByName
		}
	}

	_, custom := spec.keys[sortBy]

	switch {
	case custom:
	case sortBy == driver.SortByName:
		if spec.name == nil {
			return "", false, invalidSort(sortBy)
		}
	case sortBy == driver.SortByCreationTime:
		if spec.created == nil {
			return "", false, invalidSort(sortBy)
		}
	case sortBy == driver.SortByLastModifiedTime:
		if spec.modified == nil {
			return "", false, invalidSort(sortBy)
		}
	case sortBy == driver.SortByStatus:
		if spec.status == nil {
			return "", false, invalidSort(sortBy)
		}
	default:
		return "", false, invalidSort(sortBy)
	}

	switch order := orderOrDefault(spec, opts.SortOrder); order {
	case "", driver.SortAscending:
		return sortBy, false, nil
	case driver.SortDescending:
		return sortBy, true, nil
	default:
		return "", false, errors.Newf(errors.InvalidArgument,
			"Value '%s' at 'sortOrder' failed to satisfy constraint: Member must satisfy enum value set: [Ascending, Descending]",
			opts.SortOrder)
	}
}

func orderOrDefault[T any](spec listSpec[T], order string) string {
	if order == "" {
		return spec.defaultOrder
	}

	return order
}

func invalidSort(sortBy string) error {
	return errors.Newf(errors.InvalidArgument,
		"Value '%s' at 'sortBy' failed to satisfy constraint: not supported by this operation", sortBy)
}

func sortItems[T any](items []*T, spec listSpec[T], sortBy string, desc bool) {
	key := func(t *T) string {
		if f, ok := spec.keys[sortBy]; ok {
			return f(t)
		}

		switch sortBy {
		case driver.SortByName:
			return spec.name(t)
		case driver.SortByLastModifiedTime:
			return spec.modified(t)
		case driver.SortByStatus:
			return spec.status(t)
		default:
			return spec.created(t)
		}
	}

	sort.SliceStable(items, func(i, j int) bool {
		ki, kj := key(items[i]), key(items[j])
		if ki != kj {
			if sortBy == driver.SortByCreationTime || sortBy == driver.SortByLastModifiedTime {
				return timeLess(ki, kj) != desc
			}

			return (ki < kj) != desc
		}

		if spec.name == nil {
			return false
		}

		return spec.name(items[i]) < spec.name(items[j])
	})
}

// timeLess compares two stored RFC3339(Nano) times; unparsable values sort as
// the zero time so ordering stays total.
func timeLess(a, b string) bool {
	return parseTime(a).Before(parseTime(b))
}

func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}

	return t
}

// listFilter is the parsed, validated form of the time/name/status filters.
type listFilter struct {
	opts                          driver.ListOptions
	createdAfter, createdBefore   time.Time
	modifiedAfter, modifiedBefore time.Time
}

//nolint:gocritic // large value params mirror the driver interface
func newListFilter(opts driver.ListOptions) (listFilter, error) {
	f := listFilter{opts: opts}

	for _, p := range []struct {
		src string
		dst *time.Time
	}{
		{opts.CreationTimeAfter, &f.createdAfter},
		{opts.CreationTimeBefore, &f.createdBefore},
		{opts.LastModifiedTimeAfter, &f.modifiedAfter},
		{opts.LastModifiedTimeBefore, &f.modifiedBefore},
	} {
		if p.src == "" {
			continue
		}

		t, err := time.Parse(time.RFC3339Nano, p.src)
		if err != nil {
			return listFilter{}, errors.Newf(errors.InvalidArgument, "invalid time filter %q", p.src)
		}

		*p.dst = t
	}

	return f, nil
}

// checkFilterSupport rejects a filter on a field the operation does not have.
// An unsupported filter must never quietly turn into an empty result.
//
//nolint:gocyclo // one check per filter field
func checkFilterSupport[T any](f *listFilter, spec listSpec[T]) error {
	unsupported := func(field string) error {
		return errors.Newf(errors.InvalidArgument, "filter %s is not supported by this operation", field)
	}

	switch {
	case (!f.createdAfter.IsZero() || !f.createdBefore.IsZero()) && spec.created == nil:
		return unsupported("CreationTime")
	case (!f.modifiedAfter.IsZero() || !f.modifiedBefore.IsZero()) && spec.modified == nil:
		return unsupported("LastModifiedTime")
	case f.opts.StatusEquals != "" && spec.status == nil:
		return unsupported("StatusEquals")
	case (f.opts.NameContains != "" || f.opts.NamePrefix != "") && spec.name == nil:
		return unsupported("NameContains")
	}

	return nil
}

//nolint:gocritic,gocyclo // large value params mirror the driver interface; validation branches mirror the service's rules
func matchItem[T any](f listFilter, spec listSpec[T], item *T) bool {
	if f.opts.NameContains != "" && (spec.name == nil || !strings.Contains(spec.name(item), f.opts.NameContains)) {
		return false
	}

	if f.opts.NamePrefix != "" && (spec.name == nil || !strings.HasPrefix(spec.name(item), f.opts.NamePrefix)) {
		return false
	}

	if f.opts.StatusEquals != "" && (spec.status == nil || spec.status(item) != f.opts.StatusEquals) {
		return false
	}

	if !inRange(spec.created, item, f.createdAfter, f.createdBefore) ||
		!inRange(spec.modified, item, f.modifiedAfter, f.modifiedBefore) {
		return false
	}

	return spec.extra == nil || spec.extra(item, f.opts.Filters)
}

// inRange applies an exclusive (after, before) window; a zero bound is open.
func inRange[T any](get func(*T) string, item *T, after, before time.Time) bool {
	if after.IsZero() && before.IsZero() {
		return true
	}

	if get == nil {
		return false
	}

	t := parseTime(get(item))

	return (after.IsZero() || t.After(after)) && (before.IsZero() || t.Before(before))
}
