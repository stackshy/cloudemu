package apimanagement

import (
	"context"
	"sort"
	"strings"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// softDeleteRetention is how long Azure keeps a soft-deleted service before it
// is purged for good.
const softDeleteRetention = 48 * time.Hour

// DeletedService is a soft-deleted service: the service as it was when deleted
// (with its child resources), when it was deleted and when it will be purged.
type DeletedService struct {
	Service            Service   `json:"service"`
	Children           *Children `json:"children,omitempty"`
	DeletionDate       time.Time `json:"deletionDate"`
	ScheduledPurgeDate time.Time `json:"scheduledPurgeDate"`
}

// ARMID is the deleted service's own resource id,
// /subscriptions/{s}/providers/Microsoft.ApiManagement/locations/{l}/deletedservices/{name}.
func (d *DeletedService) ARMID() string {
	return "/subscriptions/" + d.Service.Subscription + "/providers/" + providerNamespace +
		"/locations/" + normalizeLocation(d.Service.Location) + "/deletedservices/" + d.Service.Name
}

// deletedKey is the case-insensitive store key of a soft-deleted service.
func deletedKey(sub, location, name string) string {
	return strings.ToLower(sub) + "/" + normalizeLocation(location) + "/" + strings.ToLower(name)
}

// softDeleteLocked moves the live service at k (and its children) into the
// soft-deleted store. The caller holds m.mu.
func (m *Mock) softDeleteLocked(k string, s *Service) {
	children, _ := m.children.Get(k)
	now := m.clock.Now().UTC()

	m.deleted.Set(deletedKey(s.Subscription, s.Location, s.Name), &DeletedService{
		Service:            cloneService(s),
		Children:           cloneChildren(children),
		DeletionDate:       now,
		ScheduledPurgeDate: now.Add(softDeleteRetention),
	})

	m.services.Delete(k)
	m.children.Delete(k)
}

// pruneExpiredLocked drops soft-deleted services whose retention has lapsed.
// The caller holds m.mu.
func (m *Mock) pruneExpiredLocked() {
	now := m.clock.Now()

	for k, d := range m.deleted.All() {
		if !now.Before(d.ScheduledPurgeDate) {
			m.deleted.Delete(k)
		}
	}
}

// deletedByNameLocked returns the soft-deleted service holding name in any
// subscription or location, or nil. The caller holds m.mu.
func (m *Mock) deletedByNameLocked(name string) *DeletedService {
	m.pruneExpiredLocked()

	for _, d := range m.deleted.All() {
		if strings.EqualFold(d.Service.Name, name) {
			return d
		}
	}

	return nil
}

// GetDeletedService returns the soft-deleted service name in sub/location, or a
// NotFound error when nothing by that name is soft-deleted there.
func (m *Mock) GetDeletedService(_ context.Context, sub, location, name string) (DeletedService, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.pruneExpiredLocked()

	d, ok := m.deleted.Get(deletedKey(sub, location, name))
	if !ok {
		return DeletedService{}, deletedNotFound(name, location)
	}

	return cloneDeleted(d), nil
}

// ListDeletedServices returns every soft-deleted service in the subscription,
// sorted by name.
func (m *Mock) ListDeletedServices(_ context.Context, sub string) ([]DeletedService, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.pruneExpiredLocked()

	var out []DeletedService

	for _, d := range m.deleted.All() {
		if strings.EqualFold(d.Service.Subscription, sub) {
			out = append(out, cloneDeleted(d))
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Service.Name < out[j].Service.Name })

	return out, nil
}

// PurgeDeletedService permanently removes a soft-deleted service, freeing its
// name. It is a NotFound error when nothing by that name is soft-deleted there.
func (m *Mock) PurgeDeletedService(_ context.Context, sub, location, name string) (DeletedService, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.pruneExpiredLocked()

	k := deletedKey(sub, location, name)

	d, ok := m.deleted.Get(k)
	if !ok {
		return DeletedService{}, deletedNotFound(name, location)
	}

	m.deleted.Delete(k)

	return cloneDeleted(d), nil
}

// restoreService recovers a soft-deleted service (a PUT whose properties set
// restore = true). As in Azure, every other property of the request is ignored:
// the service comes back as it was, with its child resources, under the
// request's resource id. It is a NotFound error when no service by that name is
// soft-deleted in sub/location.
func (m *Mock) restoreService(sub, rg, name, location, ifMatch string) (Service, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.pruneExpiredLocked()

	dk := deletedKey(sub, location, name)

	d, ok := m.deleted.Get(dk)
	if !ok {
		return Service{}, false, deletedNotFound(name, location)
	}

	if err := checkIfMatch(false, nil, ifMatch, name); err != nil {
		return Service{}, false, err
	}

	k := serviceKey(sub, rg, name)
	s := cloneService(&d.Service)
	s.Subscription, s.ResourceGroup, s.Name = sub, rg, name
	s.Identity = resolveIdentity(s.Identity, sub, rg, name)
	m.commitLocked(k, &s)

	children := d.Children
	if children == nil {
		children = seedChildren(&s)
	}

	m.children.Set(k, children)
	m.deleted.Delete(dk)

	return cloneService(&s), true, nil
}

// deletedNotFound is the NotFound error for a missing soft-deleted service.
func deletedNotFound(name, location string) error {
	return cerrors.Newf(cerrors.NotFound,
		"no soft-deleted API Management service %q in location %q", name, location)
}

// cloneDeleted deep-copies a soft-deleted record for a caller.
func cloneDeleted(d *DeletedService) DeletedService {
	out := *d
	out.Service = cloneService(&d.Service)
	out.Children = nil

	return out
}
