package opensearch

import (
	"context"
	"strings"
)

// domainNameFromARN extracts the domain name from an es domain ARN, or returns
// the input unchanged when it is not a domain ARN (tags on other resources).
func domainNameFromARN(arn string) string {
	const marker = ":domain/"

	if i := strings.Index(arn, marker); i >= 0 {
		return arn[i+len(marker):]
	}

	return arn
}

// AddTags adds tags to a domain identified by its ARN.
func (m *Mock) AddTags(_ context.Context, arn string, tags map[string]string) error {
	dd, err := m.getDomain(domainNameFromARN(arn))
	if err != nil {
		return err
	}

	dd.mu.Lock()
	defer dd.mu.Unlock()

	// Compute the resulting tag count before mutating so an over-limit batch is
	// rejected atomically, leaving the stored tags untouched.
	merged := len(dd.tags)

	for k := range tags {
		if _, exists := dd.tags[k]; !exists {
			merged++
		}
	}

	if merged > maxTags {
		return limitExceeded("A domain may have at most %d tags", maxTags)
	}

	if dd.tags == nil {
		dd.tags = map[string]string{}
	}

	for k, v := range tags {
		dd.tags[k] = v
	}

	return nil
}

// RemoveTags removes the named tag keys from a domain.
func (m *Mock) RemoveTags(_ context.Context, arn string, keys []string) error {
	dd, err := m.getDomain(domainNameFromARN(arn))
	if err != nil {
		return err
	}

	dd.mu.Lock()
	defer dd.mu.Unlock()

	for _, k := range keys {
		delete(dd.tags, k)
	}

	return nil
}

// ListTags returns a deep copy of a domain's tags.
func (m *Mock) ListTags(_ context.Context, arn string) (map[string]string, error) {
	dd, err := m.getDomain(domainNameFromARN(arn))
	if err != nil {
		return nil, err
	}

	dd.mu.RLock()
	defer dd.mu.RUnlock()

	return copyTags(dd.tags), nil
}
