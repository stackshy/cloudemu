package vpclattice

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/vpclattice/driver"
)

func accessLogSubNotFound(id string) error {
	return errors.Newf(errors.NotFound, "access log subscription %q not found", id)
}

func cloneAccessLogSub(a *driver.AccessLogSubscription) driver.AccessLogSubscription { return *a }

func (m *Mock) CreateAccessLogSubscription(
	_ context.Context, resourceIdentifier, destinationARN, logType string, _ map[string]string,
) (*driver.AccessLogSubscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	resourceARN := resourceIdentifier
	if !strings.Contains(resourceARN, ":") {
		resourceARN = ""
	}

	id := idgen.GenerateID("als-")
	a := &driver.AccessLogSubscription{
		ID:                    id,
		ARN:                   m.arn("accesslogsubscription/" + id),
		DestinationARN:        destinationARN,
		ResourceID:            idFromIdentifier(resourceIdentifier),
		ResourceARN:           resourceARN,
		ServiceNetworkLogType: logType,
		CreatedAt:             m.now(),
		LastUpdatedAt:         m.now(),
	}
	m.accessLogSubs.Set(id, a)

	out := cloneAccessLogSub(a)

	return &out, nil
}

func (m *Mock) GetAccessLogSubscription(_ context.Context, identifier string) (*driver.AccessLogSubscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := idFromIdentifier(identifier)

	a, ok := m.accessLogSubs.Get(id)
	if !ok {
		return nil, accessLogSubNotFound(id)
	}

	out := cloneAccessLogSub(a)

	return &out, nil
}

func (m *Mock) UpdateAccessLogSubscription(
	_ context.Context, identifier, destinationARN string,
) (*driver.AccessLogSubscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := idFromIdentifier(identifier)

	a, ok := m.accessLogSubs.Get(id)
	if !ok {
		return nil, accessLogSubNotFound(id)
	}

	if destinationARN != "" {
		a.DestinationARN = destinationARN
	}

	a.LastUpdatedAt = m.now()

	out := cloneAccessLogSub(a)

	return &out, nil
}

func (m *Mock) DeleteAccessLogSubscription(_ context.Context, identifier string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := idFromIdentifier(identifier)

	if !m.accessLogSubs.Has(id) {
		return accessLogSubNotFound(id)
	}

	m.accessLogSubs.Delete(id)

	return nil
}

func (m *Mock) ListAccessLogSubscriptions(_ context.Context) ([]driver.AccessLogSubscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return sortedValues(m.accessLogSubs.All(), cloneAccessLogSub), nil
}
