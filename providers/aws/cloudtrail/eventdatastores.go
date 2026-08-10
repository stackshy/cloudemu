package cloudtrail

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/cloudtrail/driver"
)

// CreateEventDataStore stores an event data store and returns it in ENABLED
// state (STARTING_INGESTION-then-ENABLED semantics collapsed to ENABLED), or in
// STOPPED_INGESTION when the caller passes StartIngestion=false. The name is
// claimed atomically against existing stores' names.
//
//nolint:gocritic // in is the public input, taken by value to match the driver API.
func (m *Mock) CreateEventDataStore(
	_ context.Context, in driver.CreateEventDataStoreInput,
) (*driver.EventDataStore, error) {
	if in.Name == "" {
		return nil, errInvalidParameter("Name is required")
	}

	retention := int32(defaultRetention)
	if in.RetentionPeriod != nil {
		retention = *in.RetentionPeriod
	}

	if retention < minRetention || retention > maxRetention {
		return nil, errInvalidParameter("RetentionPeriod must be between %d and %d days", minRetention, maxRetention)
	}

	billing := in.BillingMode
	if billing == "" {
		billing = driver.BillingExtendableRetention
	}

	// A store starts ingesting (ENABLED) unless the caller explicitly opts out
	// with StartIngestion=false, which lands it in STOPPED_INGESTION.
	status := driver.EDSStatusEnabled
	if in.StartIngestion != nil && !*in.StartIngestion {
		status = driver.EDSStatusStoppedIngestion
	}

	now := m.now()
	eds := driver.EventDataStore{
		Name:                         in.Name,
		ARN:                          m.edsARN(),
		Status:                       status,
		BillingMode:                  billing,
		RetentionPeriod:              retention,
		MultiRegionEnabled:           derefBool(in.MultiRegionEnabled, true),
		OrganizationEnabled:          derefBool(in.OrganizationEnabled, false),
		TerminationProtectionEnabled: derefBool(in.TerminationProtectionEnabled, true),
		KMSKeyID:                     in.KMSKeyID,
		AdvancedEventSelectors:       copyAdvSelectors(in.AdvancedEventSelectors),
		CreatedAt:                    now,
		UpdatedAt:                    now,
		Tags:                         copyTags(in.Tags),
	}

	ed := &edsData{eds: eds, maxEventSize: maxEventSizeStd, federationStatus: "DISABLED"}

	// Claim the name atomically first so concurrent creates of the same name
	// can't both succeed with distinct ARNs.
	if !m.edsNameIdx.SetIfAbsent(in.Name, eds.ARN) {
		return nil, errEDSExists(in.Name)
	}

	m.eds.Set(eds.ARN, ed)
	m.storeResourceTags(eds.ARN, in.Tags)

	out := copyEDS(&ed.eds)

	return &out, nil
}

// GetEventDataStore returns an event data store by ARN.
func (m *Mock) GetEventDataStore(_ context.Context, arn string) (*driver.EventDataStore, error) {
	ed, err := m.resolveEDS(arn)
	if err != nil {
		return nil, err
	}

	ed.mu.RLock()
	defer ed.mu.RUnlock()

	out := copyEDS(&ed.eds)

	return &out, nil
}

// UpdateEventDataStore applies the non-nil fields of in.
//
//nolint:gocritic,gocyclo // in matches the driver API (by value); one branch per optional field.
func (m *Mock) UpdateEventDataStore(
	_ context.Context, in driver.UpdateEventDataStoreInput,
) (*driver.EventDataStore, error) {
	ed, err := m.resolveEDS(in.ARN)
	if err != nil {
		return nil, err
	}

	ed.mu.Lock()
	defer ed.mu.Unlock()

	e := &ed.eds
	if e.Status != driver.EDSStatusEnabled {
		return nil, errEDSInvalidStatus("event data store %q is %s and cannot be updated", e.ARN, e.Status)
	}

	if in.Name != nil && *in.Name != e.Name {
		if !m.edsNameIdx.SetIfAbsent(*in.Name, e.ARN) {
			return nil, errEDSExists(*in.Name)
		}

		// Free the old name only if this store still owns that claim, so a
		// concurrent recreate under the old name isn't clobbered.
		m.freeEDSName(e.Name, e.ARN)
		e.Name = *in.Name
	}

	if in.BillingMode != nil {
		e.BillingMode = *in.BillingMode
	}

	if in.RetentionPeriod != nil {
		if *in.RetentionPeriod < minRetention || *in.RetentionPeriod > maxRetention {
			return nil, errInvalidParameter("RetentionPeriod must be between %d and %d days", minRetention, maxRetention)
		}

		e.RetentionPeriod = *in.RetentionPeriod
	}

	if in.MultiRegionEnabled != nil {
		e.MultiRegionEnabled = *in.MultiRegionEnabled
	}

	if in.OrganizationEnabled != nil {
		e.OrganizationEnabled = *in.OrganizationEnabled
	}

	if in.TerminationProtectionEnabled != nil {
		e.TerminationProtectionEnabled = *in.TerminationProtectionEnabled
	}

	if in.KMSKeyID != nil {
		e.KMSKeyID = *in.KMSKeyID
	}

	if in.AdvancedEventSelectors != nil {
		e.AdvancedEventSelectors = copyAdvSelectors(in.AdvancedEventSelectors)
	}

	e.UpdatedAt = m.now()
	out := copyEDS(e)

	return &out, nil
}

// DeleteEventDataStore marks an event data store PENDING_DELETION (soft delete,
// matching CloudTrail's 7-day wait) unless termination protection is on.
func (m *Mock) DeleteEventDataStore(_ context.Context, arn string) error {
	ed, err := m.resolveEDS(arn)
	if err != nil {
		return err
	}

	ed.mu.Lock()
	defer ed.mu.Unlock()

	// Already pending deletion: no-op. Re-running delete must not touch the name
	// index, or it could free a claim now owned by a recreated same-name store.
	if ed.eds.Status == driver.EDSStatusPendingDeletion {
		return nil
	}

	if ed.eds.TerminationProtectionEnabled {
		return errEDSTerminationProtected(arn)
	}

	ed.eds.Status = driver.EDSStatusPendingDeletion
	ed.eds.UpdatedAt = m.now()
	// Free the name so a same-name store can be recreated during the deletion
	// window; RestoreEventDataStore re-claims it. Free only if this store still
	// owns the claim (guards against a concurrent recreate under the same name).
	m.freeEDSName(ed.eds.Name, ed.eds.ARN)

	return nil
}

// freeEDSName removes name -> arn from the name index only when the index still
// maps name to arn (this store owns the claim). This prevents a stale/duplicate
// delete from clobbering a claim now held by a different, live store.
func (m *Mock) freeEDSName(name, arn string) {
	if cur, ok := m.edsNameIdx.Get(name); ok && cur == arn {
		m.edsNameIdx.Delete(name)
	}
}

// RestoreEventDataStore returns a PENDING_DELETION store to ENABLED. Real
// CloudTrail only restores a store that is pending deletion; any other status is
// an InvalidEventDataStoreStatusException.
func (m *Mock) RestoreEventDataStore(_ context.Context, arn string) (*driver.EventDataStore, error) {
	ed, err := m.resolveEDS(arn)
	if err != nil {
		return nil, err
	}

	ed.mu.Lock()
	defer ed.mu.Unlock()

	if ed.eds.Status != driver.EDSStatusPendingDeletion {
		return nil, errEDSInvalidStatus(
			"event data store %q is %s and cannot be restored", arn, ed.eds.Status)
	}

	// Re-claim the name freed on delete; if a new store took it meanwhile, the
	// restore conflicts.
	if !m.edsNameIdx.SetIfAbsent(ed.eds.Name, ed.eds.ARN) {
		return nil, errEDSExists(ed.eds.Name)
	}

	ed.eds.Status = driver.EDSStatusEnabled
	ed.eds.UpdatedAt = m.now()
	out := copyEDS(&ed.eds)

	return &out, nil
}

// ListEventDataStores returns all stores ordered by ARN, paginated.
func (m *Mock) ListEventDataStores(
	_ context.Context, nextToken string, maxResults int32,
) ([]driver.EventDataStore, string, error) {
	out, next := paginate(m.eds.All(), nextToken, maxResults,
		func(ed *edsData) driver.EventDataStore {
			ed.mu.RLock()
			defer ed.mu.RUnlock()

			return copyEDS(&ed.eds)
		},
		func(e driver.EventDataStore) string { return e.ARN },
	)

	return out, next, nil
}

// StartEventDataStoreIngestion sets a store's status to ENABLED (ingesting).
func (m *Mock) StartEventDataStoreIngestion(_ context.Context, arn string) error {
	return m.setEDSStatus(arn, driver.EDSStatusEnabled)
}

// StopEventDataStoreIngestion sets a store's status to STOPPED_INGESTION.
func (m *Mock) StopEventDataStoreIngestion(_ context.Context, arn string) error {
	return m.setEDSStatus(arn, driver.EDSStatusStoppedIngestion)
}

func (m *Mock) setEDSStatus(arn, status string) error {
	ed, err := m.resolveEDS(arn)
	if err != nil {
		return err
	}

	ed.mu.Lock()
	defer ed.mu.Unlock()

	// A store pending deletion is inactive: ingestion cannot be started/stopped
	// against it until it is restored.
	if ed.eds.Status == driver.EDSStatusPendingDeletion {
		return errEDSInvalidStatus(
			"event data store %q is %s and does not support ingestion changes", arn, ed.eds.Status)
	}

	ed.eds.Status = status
	ed.eds.UpdatedAt = m.now()

	return nil
}

// EnableFederation enables Lake Formation federation for an event data store.
func (m *Mock) EnableFederation(
	_ context.Context, edsARN, roleARN string,
) (outARN, federationRoleARN, federationStatus string, err error) {
	ed, err := m.resolveEDS(edsARN)
	if err != nil {
		return "", "", "", err
	}

	ed.mu.Lock()
	defer ed.mu.Unlock()

	if ed.eds.Status != driver.EDSStatusEnabled {
		return "", "", "", errEDSInvalidStatus(
			"event data store %q is %s and does not support federation changes", edsARN, ed.eds.Status)
	}

	ed.federationRoleARN = roleARN
	ed.federationStatus = "ENABLED"

	return ed.eds.ARN, ed.federationRoleARN, ed.federationStatus, nil
}

// DisableFederation disables federation for an event data store.
func (m *Mock) DisableFederation(
	_ context.Context, edsARN string,
) (outARN, federationStatus string, err error) {
	ed, err := m.resolveEDS(edsARN)
	if err != nil {
		return "", "", err
	}

	ed.mu.Lock()
	defer ed.mu.Unlock()

	if ed.eds.Status != driver.EDSStatusEnabled {
		return "", "", errEDSInvalidStatus(
			"event data store %q is %s and does not support federation changes", edsARN, ed.eds.Status)
	}

	ed.federationStatus = "DISABLED"
	ed.federationRoleARN = ""

	return ed.eds.ARN, ed.federationStatus, nil
}

func derefBool(p *bool, def bool) bool {
	if p == nil {
		return def
	}

	return *p
}
