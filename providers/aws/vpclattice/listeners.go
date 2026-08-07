package vpclattice

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/vpclattice/driver"
)

func listenerNotFound(id string) error {
	return errors.Newf(errors.NotFound, "listener %q not found", id)
}

func cloneListener(l *driver.Listener) driver.Listener {
	out := *l
	out.DefaultAction = append([]byte(nil), l.DefaultAction...)

	return out
}

// serviceARNFor resolves a service identifier to its ARN, erroring if unknown.
// Caller holds m.mu.
func (m *Mock) serviceARNFor(serviceIdentifier string) (string, error) {
	sid := idFromIdentifier(serviceIdentifier)

	svc, ok := m.services.Get(sid)
	if !ok {
		return "", serviceNotFound(sid)
	}

	return svc.ARN, nil
}

func (m *Mock) CreateListener(_ context.Context, in *driver.CreateListenerInput) (*driver.Listener, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	serviceARN, err := m.serviceARNFor(in.ServiceID)
	if err != nil {
		return nil, err
	}

	sid := idFromIdentifier(in.ServiceID)
	id := idgen.GenerateID("listener-")
	l := &driver.Listener{
		ID:            id,
		ARN:           m.arn("service/" + sid + "/listener/" + id),
		Name:          in.Name,
		ServiceID:     sid,
		ServiceARN:    serviceARN,
		Protocol:      in.Protocol,
		Port:          in.Port,
		DefaultAction: append([]byte(nil), in.DefaultAction...),
		CreatedAt:     m.now(),
		LastUpdatedAt: m.now(),
	}
	m.listeners.Set(id, l)
	m.writeTags(l.ARN, in.Tags)

	out := cloneListener(l)

	return &out, nil
}

// getListenerLocked resolves a listener scoped to a service. Caller holds m.mu.
func (m *Mock) getListenerLocked(serviceIdentifier, listenerIdentifier string) (*driver.Listener, error) {
	sid := idFromIdentifier(serviceIdentifier)
	lid := idFromIdentifier(listenerIdentifier)

	l, ok := m.listeners.Get(lid)
	if !ok || l.ServiceID != sid {
		return nil, listenerNotFound(lid)
	}

	return l, nil
}

func (m *Mock) GetListener(_ context.Context, serviceID, listenerID string) (*driver.Listener, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	l, err := m.getListenerLocked(serviceID, listenerID)
	if err != nil {
		return nil, err
	}

	out := cloneListener(l)

	return &out, nil
}

func (m *Mock) UpdateListener(
	_ context.Context, serviceID, listenerID string, defaultAction []byte,
) (*driver.Listener, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	l, err := m.getListenerLocked(serviceID, listenerID)
	if err != nil {
		return nil, err
	}

	if len(defaultAction) > 0 {
		l.DefaultAction = append([]byte(nil), defaultAction...)
	}

	l.LastUpdatedAt = m.now()

	out := cloneListener(l)

	return &out, nil
}

func (m *Mock) DeleteListener(_ context.Context, serviceID, listenerID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	l, err := m.getListenerLocked(serviceID, listenerID)
	if err != nil {
		return err
	}

	// Cascade the listener's contained rules.
	for rid, r := range m.rules.All() {
		if r.ListenerID == l.ID {
			m.rules.Delete(rid)
		}
	}

	m.listeners.Delete(l.ID)

	return nil
}

func (m *Mock) ListListeners(_ context.Context, serviceID string) ([]driver.Listener, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	sid := idFromIdentifier(serviceID)

	all := sortedValues(m.listeners.All(), cloneListener)

	out := make([]driver.Listener, 0, len(all))

	for i := range all {
		if all[i].ServiceID == sid {
			out = append(out, all[i])
		}
	}

	return out, nil
}
