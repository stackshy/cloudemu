package healthlake

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/healthlake/driver"
)

// CreateFHIRDatastore provisions a FHIR data store with stable computed fields
// (id, arn, endpoint, createdAt) and reports it ACTIVE at once, so an IaC waiter
// completes without a provisioning wait. When no SSE config is supplied the data
// store reports an AWS-owned KMS key, mirroring real HealthLake. The SSE,
// preload and identity-provider blocks round-trip verbatim.
func (m *Mock) CreateFHIRDatastore(_ context.Context, in *driver.CreateFHIRDatastoreInput) (*driver.Datastore, error) {
	version := in.DatastoreTypeVersion
	if version == "" {
		return nil, validation("DatastoreTypeVersion is required")
	}

	if version != driver.FHIRVersionR4 {
		return nil, validation("unsupported DatastoreTypeVersion %q; only R4 is supported", version)
	}

	id := newDatastoreID()

	sse := copySSE(in.SseConfiguration)
	if sse == nil {
		sse = &driver.SseConfiguration{
			KmsEncryptionConfig: &driver.KmsEncryptionConfig{CmkType: driver.CmkTypeAWSOwned},
		}
	}

	ds := driver.Datastore{
		DatastoreID:                   id,
		DatastoreArn:                  m.datastoreARN(id),
		DatastoreEndpoint:             m.datastoreEndpoint(id),
		DatastoreName:                 in.DatastoreName,
		DatastoreStatus:               driver.StatusActive,
		DatastoreTypeVersion:          version,
		CreatedAt:                     m.opts.Clock.Now().UTC(),
		SseConfiguration:              sse,
		PreloadDataConfig:             copyPreload(in.PreloadDataConfig),
		IdentityProviderConfiguration: copyIdentityProvider(in.IdentityProviderConfiguration),
		Tags:                          copyTags(in.Tags),
	}

	m.datastores.Set(id, ds)

	out := copyDatastore(&ds)

	return &out, nil
}

// DescribeFHIRDatastore returns the data store by id, or a
// ResourceNotFoundException.
func (m *Mock) DescribeFHIRDatastore(_ context.Context, datastoreID string) (*driver.Datastore, error) {
	ds, ok := m.datastores.Get(datastoreID)
	if !ok {
		return nil, notFound("data store %q does not exist", datastoreID)
	}

	out := copyDatastore(&ds)

	return &out, nil
}

// DeleteFHIRDatastore removes a data store and returns its identity with a
// DELETED status. The store is removed immediately so a subsequent describe
// returns ResourceNotFoundException, letting an IaC delete-waiter complete.
func (m *Mock) DeleteFHIRDatastore(_ context.Context, datastoreID string) (*driver.Datastore, error) {
	ds, ok := m.datastores.Get(datastoreID)
	if !ok {
		return nil, notFound("data store %q does not exist", datastoreID)
	}

	m.datastores.Delete(datastoreID)

	out := copyDatastore(&ds)
	out.DatastoreStatus = driver.StatusDeleted

	return &out, nil
}

// ListFHIRDatastores returns a deterministic page of data stores ordered by id,
// narrowed by the supplied filter (name and/or status).
func (m *Mock) ListFHIRDatastores(
	_ context.Context, filter driver.ListFilter, page driver.Page,
) (datastores []driver.Datastore, nextToken string, err error) {
	stored := m.datastores.SortedValues()

	matched := make([]driver.Datastore, 0, len(stored))

	for i := range stored {
		if matchesFilter(&stored[i], filter) {
			matched = append(matched, stored[i])
		}
	}

	start, end, next := paginate(len(matched), page)

	out := make([]driver.Datastore, 0, end-start)
	for i := start; i < end; i++ {
		out = append(out, copyDatastore(&matched[i]))
	}

	return out, next, nil
}

// matchesFilter reports whether a data store satisfies the list filter. A zero
// filter field is ignored.
func matchesFilter(ds *driver.Datastore, filter driver.ListFilter) bool {
	if filter.DatastoreName != "" && ds.DatastoreName != filter.DatastoreName {
		return false
	}

	if filter.DatastoreStatus != "" && ds.DatastoreStatus != filter.DatastoreStatus {
		return false
	}

	return true
}
