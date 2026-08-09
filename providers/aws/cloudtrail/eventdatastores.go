package cloudtrail

import (
	"context"
	"sort"

	"github.com/stackshy/cloudemu/v2/services/cloudtrail/driver"
)

// CreateEventDataStore stores an event data store and returns it in ENABLED
// state (or STARTING_INGESTION-then-ENABLED semantics collapsed to ENABLED).
// The name is claimed atomically against existing stores' names.
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

	now := m.now()
	eds := driver.EventDataStore{
		Name:                         in.Name,
		ARN:                          m.edsARN(),
		Status:                       driver.EDSStatusEnabled,
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
	if in.Name != nil && *in.Name != e.Name {
		if !m.edsNameIdx.SetIfAbsent(*in.Name, e.ARN) {
			return nil, errEDSExists(*in.Name)
		}

		m.edsNameIdx.Delete(e.Name)
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

	if ed.eds.TerminationProtectionEnabled {
		return errEDSTerminationProtected(arn)
	}

	ed.eds.Status = driver.EDSStatusPendingDeletion
	ed.eds.UpdatedAt = m.now()
	// Free the name so a same-name store can be recreated during the deletion
	// window; RestoreEventDataStore re-claims it.
	m.edsNameIdx.Delete(ed.eds.Name)

	return nil
}

// RestoreEventDataStore returns a PENDING_DELETION store to ENABLED. Real
// CloudTrail only restores a store that is pending deletion; any other status is
// an InactiveEventDataStoreException.
func (m *Mock) RestoreEventDataStore(_ context.Context, arn string) (*driver.EventDataStore, error) {
	ed, err := m.resolveEDS(arn)
	if err != nil {
		return nil, err
	}

	ed.mu.Lock()
	defer ed.mu.Unlock()

	if ed.eds.Status != driver.EDSStatusPendingDeletion {
		return nil, errEDSInactive(arn)
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
	all := m.eds.All()

	arns := make([]string, 0, len(all))
	for arn := range all {
		arns = append(arns, arn)
	}

	sort.Strings(arns)

	limit := int(maxResults)
	if limit <= 0 {
		limit = defaultMaxResults
	}

	out := make([]driver.EventDataStore, 0, len(arns))
	started := nextToken == ""

	for _, arn := range arns {
		if !started {
			if arn == nextToken {
				started = true
			}

			continue
		}

		if len(out) == limit {
			return out, out[len(out)-1].ARN, nil
		}

		ed := all[arn]
		ed.mu.RLock()
		out = append(out, copyEDS(&ed.eds))
		ed.mu.RUnlock()
	}

	return out, "", nil
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
