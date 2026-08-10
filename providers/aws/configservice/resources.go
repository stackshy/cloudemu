package configservice

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/configservice/driver"
)

// The resource-configuration query surface is backed by an in-memory store that
// callers populate via PutResourceConfig. Real Config discovers resources
// automatically; the emulator instead records what PutResourceConfig supplies
// and answers queries from that. Read paths deep-copy items. Documented in
// docs/services.md.

func copyItem(i *driver.ConfigurationItem) driver.ConfigurationItem {
	out := *i
	out.Tags = copyTags(i.Tags)
	out.SupplementaryConfig = copyTags(i.SupplementaryConfig)

	return out
}

// PutResourceConfig records a custom resource's configuration item.
//
//nolint:gocritic // item taken by value to match the driver API.
func (m *Mock) PutResourceConfig(_ context.Context, item driver.ConfigurationItem) error {
	if item.ResourceType == "" || item.ResourceID == "" {
		return invalidParameter("ResourceType and ResourceId are required")
	}

	if item.Configuration == "" {
		return invalidParameter("Configuration is required")
	}

	item.Tags = copyTags(item.Tags)
	item.SupplementaryConfig = copyTags(item.SupplementaryConfig)

	if item.CaptureTime.IsZero() {
		item.CaptureTime = m.now()
	}

	if item.AccountID == "" {
		item.AccountID = m.opts.AccountID
	}

	if item.AwsRegion == "" {
		item.AwsRegion = m.opts.Region
	}

	if item.ConfigurationState == "" {
		item.ConfigurationState = "OK"
	}

	m.resources.Set(resourceKey(item.ResourceType, item.ResourceID), &item)

	return nil
}

func (m *Mock) allItems() []driver.ConfigurationItem {
	keys := sortedKeys(m.resources.Keys())
	out := make([]driver.ConfigurationItem, 0, len(keys))

	for _, k := range keys {
		it, ok := m.resources.Get(k)
		if !ok {
			continue
		}

		out = append(out, copyItem(it))
	}

	return out
}

func (m *Mock) matchingResources(resourceType, resourceID string) []driver.ConfigurationItem {
	all := m.allItems()
	out := make([]driver.ConfigurationItem, 0, len(all))

	for i := range all {
		if resourceType != "" && all[i].ResourceType != resourceType {
			continue
		}

		if resourceID != "" && all[i].ResourceID != resourceID {
			continue
		}

		out = append(out, all[i])
	}

	return out
}

// GetResourceConfigHistory returns the recorded item(s) for a resource.
func (m *Mock) GetResourceConfigHistory(
	_ context.Context, resourceType, resourceID string, page driver.Page,
) ([]driver.ConfigurationItem, string, error) {
	if resourceType == "" || resourceID == "" {
		return nil, "", invalidParameter("resourceType and resourceId are required")
	}

	items := m.matchingResources(resourceType, resourceID)
	if len(items) == 0 {
		return nil, "", tagged(driver.ExResourceNotDiscovered, notFoundCode,
			"resource %s/%s has not been discovered", resourceType, resourceID)
	}

	return paginate(items, page)
}

// DeleteResourceConfig removes a recorded resource item.
func (m *Mock) DeleteResourceConfig(_ context.Context, resourceType, resourceID string) error {
	if resourceType == "" || resourceID == "" {
		return invalidParameter("ResourceType and ResourceId are required")
	}

	m.resources.Delete(resourceKey(resourceType, resourceID))

	return nil
}

func (m *Mock) batchGet(
	keys []driver.ResourceKey,
) (found []driver.ConfigurationItem, unprocessed []driver.ResourceKey, err error) {
	for _, k := range keys {
		it, ok := m.resources.Get(resourceKey(k.ResourceType, k.ResourceID))
		if ok {
			found = append(found, copyItem(it))
		} else {
			unprocessed = append(unprocessed, k)
		}
	}

	return found, unprocessed, nil
}

// BatchGetResourceConfig returns recorded items for the requested keys.
func (m *Mock) BatchGetResourceConfig(
	_ context.Context, keys []driver.ResourceKey,
) (found []driver.ConfigurationItem, unprocessed []driver.ResourceKey, err error) {
	if len(keys) == 0 {
		return nil, nil, invalidParameter("resourceKeys must not be empty")
	}

	return m.batchGet(keys)
}

func (m *Mock) listResourceKeys(
	resourceType string, ids []string, page driver.Page,
) (keys []driver.ResourceKey, nextToken string, err error) {
	idSet := map[string]bool{}
	for _, id := range ids {
		idSet[id] = true
	}

	all := m.allItems()
	out := make([]driver.ResourceKey, 0, len(all))

	for i := range all {
		if resourceType != "" && all[i].ResourceType != resourceType {
			continue
		}

		if len(idSet) > 0 && !idSet[all[i].ResourceID] {
			continue
		}

		out = append(out, driver.ResourceKey{ResourceType: all[i].ResourceType, ResourceID: all[i].ResourceID})
	}

	return paginate(out, page)
}

// ListDiscoveredResources lists recorded resource keys of a type.
func (m *Mock) ListDiscoveredResources(
	_ context.Context, resourceType string, ids []string, page driver.Page,
) ([]driver.ResourceKey, string, error) {
	return m.listResourceKeys(resourceType, ids, page)
}

func (m *Mock) discoveredCounts(
	resourceTypes []string, page driver.Page,
) (total int64, counts []driver.ResourceCount, next string, err error) {
	want := map[string]bool{}
	for _, t := range resourceTypes {
		want[t] = true
	}

	byType := map[string]int64{}
	all := m.allItems()

	for i := range all {
		if len(want) > 0 && !want[all[i].ResourceType] {
			continue
		}

		byType[all[i].ResourceType]++
		total++
	}

	keys := make([]string, 0, len(byType))
	for t := range byType {
		keys = append(keys, t)
	}

	rows := make([]driver.ResourceCount, 0, len(keys))
	for _, t := range sortedKeys(keys) {
		rows = append(rows, driver.ResourceCount{ResourceType: t, Count: byType[t]})
	}

	// Counts are not further limited beyond the page window default.
	page.Limit = 0

	paged, next, perr := paginate(rows, page)
	if perr != nil {
		return 0, nil, "", perr
	}

	return total, paged, next, nil
}

// GetDiscoveredResourceCounts returns per-type counts of recorded resources.
func (m *Mock) GetDiscoveredResourceCounts(
	_ context.Context, resourceTypes []string, page driver.Page,
) (total int64, counts []driver.ResourceCount, next string, err error) {
	return m.discoveredCounts(resourceTypes, page)
}

// selectResultsFiltered applies a parsed SELECT query (WHERE resourceType filter
// + projection) to the recorded items and returns the projected rows.
func (m *Mock) selectResultsFiltered(q selectQuery, page driver.Page) (rows []string, nextToken string, err error) {
	items := m.allItems()

	out := make([]string, 0, len(items))

	for i := range items {
		if q.hasResFilter && items[i].ResourceType != q.resourceType {
			continue
		}

		row, perr := q.projectItem(&items[i])
		if perr != nil {
			return nil, "", perr
		}

		out = append(out, row)
	}

	return paginate(out, page)
}

// SelectResourceConfig runs the supported subset of the Config SQL SELECT
// grammar (projection + WHERE resourceType=...) against the recorded items.
// Unsupported syntax is a typed InvalidExpressionException.
func (m *Mock) SelectResourceConfig(
	_ context.Context, expression string, page driver.Page,
) (rows []string, nextToken string, err error) {
	if expression == "" {
		return nil, "", invalidParameter("Expression is required")
	}

	q, perr := parseSelect(expression)
	if perr != nil {
		return nil, "", perr
	}

	return m.selectResultsFiltered(q, page)
}
