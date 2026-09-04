package elbv2

import (
	"context"

	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/loadbalancer/driver"
)

// AddResourceTags adds or overwrites tags on a load balancer, target group,
// listener, or rule identified by ARN (ELBv2 AddTags). Unknown ARNs are
// ignored, matching AWS's tolerance for a mixed multi-resource AddTags call.
func (m *Mock) AddResourceTags(_ context.Context, arn string, tags map[string]string) error {
	switch {
	case addTagsToStore(m.lbs, arn, tags, lbTagsGet, lbTagsSet):
	case addTagsToStore(m.tgs, arn, tags, tgTagsGet, tgTagsSet):
	case addTagsToStore(m.listeners, arn, tags, listenerTagsGet, listenerTagsSet):
	case addTagsToStore(m.rules, arn, tags, ruleTagsGet, ruleTagsSet):
	}

	return nil
}

// RemoveResourceTags removes tags by key from a load balancer, target group,
// listener, or rule identified by ARN (ELBv2 RemoveTags).
func (m *Mock) RemoveResourceTags(_ context.Context, arn string, keys []string) error {
	switch {
	case removeTagsFromStore(m.lbs, arn, keys, lbTagsGet):
	case removeTagsFromStore(m.tgs, arn, keys, tgTagsGet):
	case removeTagsFromStore(m.listeners, arn, keys, listenerTagsGet):
	case removeTagsFromStore(m.rules, arn, keys, ruleTagsGet):
	}

	return nil
}

// The four ELBv2 resource kinds AddTags/RemoveTags accept all carry their tags
// on the same field shape (map[string]string); these get/set pairs let
// addTagsToStore/removeTagsFromStore below operate generically over whichever
// store the ARN resolves against. Both sides take a pointer so a heavy
// resource struct (LBInfo, TargetGroupInfo) is never copied by value into the
// getter.
func lbTagsGet(v *driver.LBInfo) map[string]string          { return v.Tags }
func lbTagsSet(v *driver.LBInfo, tags map[string]string)    { v.Tags = tags }
func tgTagsGet(v *driver.TargetGroupInfo) map[string]string { return v.Tags }
func tgTagsSet(v *driver.TargetGroupInfo, tags map[string]string) {
	v.Tags = tags
}
func listenerTagsGet(v *driver.ListenerInfo) map[string]string { return v.Tags }
func listenerTagsSet(v *driver.ListenerInfo, tags map[string]string) {
	v.Tags = tags
}
func ruleTagsGet(v *driver.RuleInfo) map[string]string       { return v.Tags }
func ruleTagsSet(v *driver.RuleInfo, tags map[string]string) { v.Tags = tags }

// addTagsToStore merges tags into the tag map of the value stored at arn in
// store (initializing a nil map first), reporting false (a no-op) when arn
// does not resolve in this store — letting AddResourceTags try the next
// resource kind, matching AWS's tolerance for a mixed multi-resource-kind
// AddTags call.
func addTagsToStore[T any](
	store *memstore.Store[T], arn string, tags map[string]string,
	get func(*T) map[string]string, set func(*T, map[string]string),
) bool {
	v, ok := store.Get(arn)
	if !ok {
		return false
	}

	existing := get(&v)
	if existing == nil {
		existing = map[string]string{}
		set(&v, existing)
	}

	for k, val := range tags {
		existing[k] = val
	}

	store.Set(arn, v)

	return true
}

// removeTagsFromStore deletes keys from the tag map of the value stored at arn
// in store, reporting false (a no-op) when arn does not resolve in this store.
func removeTagsFromStore[T any](
	store *memstore.Store[T], arn string, keys []string, get func(*T) map[string]string,
) bool {
	v, ok := store.Get(arn)
	if !ok {
		return false
	}

	tags := get(&v)
	for _, k := range keys {
		delete(tags, k)
	}

	store.Set(arn, v)

	return true
}
