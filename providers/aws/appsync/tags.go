package appsync

import "context"

// resolveTaggable resolves the API a tag resource ARN refers to. AppSync tags
// live on the GraphQL API, so a data-source ARN resolves to its owning API too.
func (m *Mock) resolveTaggable(resourceArn string) (*apiData, error) {
	apiID := apiIDFromARN(resourceArn)
	if apiID == "" {
		return nil, badRequest("invalid resource ARN: %q", resourceArn)
	}

	return m.getAPI(apiID)
}

// TagResource adds or overwrites tags on a resource.
func (m *Mock) TagResource(_ context.Context, resourceArn string, tags map[string]string) error {
	ad, err := m.resolveTaggable(resourceArn)
	if err != nil {
		return err
	}

	ad.mu.Lock()
	defer ad.mu.Unlock()

	if ad.api.Tags == nil {
		ad.api.Tags = map[string]string{}
	}

	for k, v := range tags {
		ad.api.Tags[k] = v
	}

	return nil
}

// UntagResource removes tags by key from a resource.
func (m *Mock) UntagResource(_ context.Context, resourceArn string, tagKeys []string) error {
	ad, err := m.resolveTaggable(resourceArn)
	if err != nil {
		return err
	}

	ad.mu.Lock()
	defer ad.mu.Unlock()

	for _, k := range tagKeys {
		delete(ad.api.Tags, k)
	}

	return nil
}

// ListTagsForResource returns a copy of a resource's tags.
func (m *Mock) ListTagsForResource(_ context.Context, resourceArn string) (map[string]string, error) {
	ad, err := m.resolveTaggable(resourceArn)
	if err != nil {
		return nil, err
	}

	ad.mu.RLock()
	defer ad.mu.RUnlock()

	return copyTags(ad.api.Tags), nil
}
