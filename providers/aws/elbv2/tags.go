package elbv2

import "context"

// AddResourceTags adds or overwrites tags on a load balancer or target group
// identified by ARN (ELBv2 AddTags). Unknown ARNs are ignored, matching AWS's
// tolerance for a mixed multi-resource AddTags call.
func (m *Mock) AddResourceTags(_ context.Context, arn string, tags map[string]string) error {
	if lb, ok := m.lbs.Get(arn); ok {
		if lb.Tags == nil {
			lb.Tags = map[string]string{}
		}

		for k, v := range tags {
			lb.Tags[k] = v
		}

		m.lbs.Set(arn, lb)

		return nil
	}

	if tg, ok := m.tgs.Get(arn); ok {
		if tg.Tags == nil {
			tg.Tags = map[string]string{}
		}

		for k, v := range tags {
			tg.Tags[k] = v
		}

		m.tgs.Set(arn, tg)
	}

	return nil
}

// RemoveResourceTags removes tags by key from a load balancer or target group
// identified by ARN (ELBv2 RemoveTags).
func (m *Mock) RemoveResourceTags(_ context.Context, arn string, keys []string) error {
	if lb, ok := m.lbs.Get(arn); ok {
		for _, k := range keys {
			delete(lb.Tags, k)
		}

		m.lbs.Set(arn, lb)

		return nil
	}

	if tg, ok := m.tgs.Get(arn); ok {
		for _, k := range keys {
			delete(tg.Tags, k)
		}

		m.tgs.Set(arn, tg)
	}

	return nil
}
