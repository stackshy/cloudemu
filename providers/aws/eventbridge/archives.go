package eventbridge

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/eventmatch"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/eventbus/driver"
)

const (
	archiveStateEnabled = "ENABLED"

	maxArchiveNameLen = 48
	maxDescriptionLen = 512
	maxPatternLen     = 4096

	hoursPerDay = 24
)

// archiveStates is the ArchiveState enum, in the order EventBridge lists it.
func archiveStates() []string {
	return []string{archiveStateEnabled, "DISABLED", stateCreating, stateUpdating, "CREATE_FAILED", "UPDATE_FAILED"}
}

// archivedEvent is one event held by an archive, with the time it was archived
// (retention counts from it) and its envelope size in bytes.
type archivedEvent struct {
	Event    driver.Event `json:"event"`
	Archived time.Time    `json:"archived"`
	Size     int          `json:"size"`
}

// archiveData is an archive and the events it holds. busName is the event bus
// it captures from. mu guards info and events.
type archiveData struct {
	mu      sync.RWMutex
	info    driver.Archive
	busName string
	events  []archivedEvent
}

func errBusNotExist(name string) error {
	return errors.Newf(errors.NotFound, "Event bus %s does not exist.", name)
}

func errArchiveNotExist(name string) error {
	return errors.Newf(errors.NotFound, "Archive %s does not exist.", name)
}

// validatePattern checks an archive event pattern, reporting a failure as
// InvalidEventPatternException.
func validatePattern(pattern string) error {
	if pattern == "" {
		return nil
	}

	if err := eventmatch.ValidatePattern(pattern); err != nil {
		return fmt.Errorf("%w: %w", driver.ErrInvalidEventPattern, errors.New(errors.InvalidArgument, err.Error()))
	}

	return nil
}

func validateArchiveConfig(cfg *driver.ArchiveConfig) error {
	var v violations

	v.name(cfg.Name, "archiveName", maxArchiveNameLen)
	v.eventsARN(cfg.EventSourceARN, "eventSourceArn")
	v.maxLen(cfg.Description, "description", maxDescriptionLen)
	v.maxLen(cfg.EventPattern, "eventPattern", maxPatternLen)

	if cfg.RetentionDays < 0 {
		v.add(fmt.Sprint(cfg.RetentionDays), "retentionDays", "Member must have value greater than or equal to 0")
	}

	if err := v.err(); err != nil {
		return err
	}

	return validatePattern(cfg.EventPattern)
}

// CreateArchive creates an archive that captures the events put on the event
// bus named by cfg.EventSourceARN.
func (m *Mock) CreateArchive(_ context.Context, cfg *driver.ArchiveConfig) (*driver.Archive, error) {
	if err := validateArchiveConfig(cfg); err != nil {
		return nil, err
	}

	busName := busNameFromARN(cfg.EventSourceARN)
	if !m.buses.Has(busName) {
		return nil, errBusNotExist(busName)
	}

	ad := &archiveData{
		busName: busName,
		info: driver.Archive{
			Name:             cfg.Name,
			ARN:              idgen.AWSARN("events", m.opts.Region, m.opts.AccountID, "archive/"+cfg.Name),
			EventSourceARN:   cfg.EventSourceARN,
			Description:      cfg.Description,
			EventPattern:     compactPattern(cfg.EventPattern),
			State:            archiveStateEnabled,
			KMSKeyIdentifier: cfg.KMSKeyIdentifier,
			RetentionDays:    cfg.RetentionDays,
			CreatedAt:        m.opts.Clock.Now().UTC(),
		},
	}

	if !m.archives.SetIfAbsent(cfg.Name, ad) {
		return nil, errors.Newf(errors.AlreadyExists, "Archive %s already exists.", cfg.Name)
	}

	out := ad.info

	return &out, nil
}

// DescribeArchive returns an archive with its current event count and size.
func (m *Mock) DescribeArchive(_ context.Context, name string) (*driver.Archive, error) {
	ad, ok := m.archives.Get(name)
	if !ok {
		return nil, errArchiveNotExist(name)
	}

	out := m.archiveView(ad)

	return &out, nil
}

// archiveView prunes expired events and returns a copy of the archive's info
// with EventCount and SizeBytes filled in.
func (m *Mock) archiveView(ad *archiveData) driver.Archive {
	ad.mu.Lock()
	defer ad.mu.Unlock()

	m.pruneLocked(ad)

	out := ad.info
	out.EventCount = int64(len(ad.events))

	for i := range ad.events {
		out.SizeBytes += int64(ad.events[i].Size)
	}

	return out
}

// pruneLocked drops events older than the archive's retention period. The
// caller holds ad.mu for writing.
func (m *Mock) pruneLocked(ad *archiveData) {
	if ad.info.RetentionDays <= 0 || len(ad.events) == 0 {
		return
	}

	cutoff := m.opts.Clock.Now().Add(-time.Duration(ad.info.RetentionDays) * hoursPerDay * time.Hour)

	kept := ad.events[:0]

	for i := range ad.events {
		if ad.events[i].Archived.After(cutoff) {
			kept = append(kept, ad.events[i])
		}
	}

	ad.events = kept
}

// msgArchiveUpdateEmpty is EventBridge's reply to an UpdateArchive call that
// changes nothing.
const msgArchiveUpdateEmpty = "At least one of EventPattern, RetentionDays or Description must be provided."

func validateArchiveUpdate(upd *driver.ArchiveUpdate) error {
	if upd.Description == nil && upd.EventPattern == nil && upd.RetentionDays == nil && upd.KMSKeyIdentifier == nil {
		return errors.New(errors.InvalidArgument, msgArchiveUpdateEmpty)
	}

	if err := archiveUpdateViolations(upd).err(); err != nil {
		return err
	}

	if upd.EventPattern != nil {
		return validatePattern(*upd.EventPattern)
	}

	return nil
}

func archiveUpdateViolations(upd *driver.ArchiveUpdate) violations {
	var v violations

	if upd.Description != nil {
		v.maxLen(*upd.Description, "description", maxDescriptionLen)
	}

	if upd.EventPattern != nil {
		v.maxLen(*upd.EventPattern, "eventPattern", maxPatternLen)
	}

	if upd.RetentionDays != nil && *upd.RetentionDays < 0 {
		v.add(fmt.Sprint(*upd.RetentionDays), "retentionDays", "Member must have value greater than or equal to 0")
	}

	return v
}

// UpdateArchive changes the description, event pattern, retention period or KMS
// key of an archive. Omitted fields keep their values.
func (m *Mock) UpdateArchive(_ context.Context, upd *driver.ArchiveUpdate) (*driver.Archive, error) {
	if err := validateArchiveUpdate(upd); err != nil {
		return nil, err
	}

	ad, ok := m.archives.Get(upd.Name)
	if !ok {
		return nil, errArchiveNotExist(upd.Name)
	}

	ad.mu.Lock()

	if upd.Description != nil {
		ad.info.Description = *upd.Description
	}

	if upd.EventPattern != nil {
		ad.info.EventPattern = compactPattern(*upd.EventPattern)
	}

	if upd.RetentionDays != nil {
		ad.info.RetentionDays = *upd.RetentionDays
	}

	if upd.KMSKeyIdentifier != nil {
		ad.info.KMSKeyIdentifier = *upd.KMSKeyIdentifier
	}

	ad.mu.Unlock()

	out := m.archiveView(ad)

	return &out, nil
}

// DeleteArchive deletes an archive and the events it holds.
func (m *Mock) DeleteArchive(_ context.Context, name string) error {
	if !m.archives.Delete(name) {
		return errArchiveNotExist(name)
	}

	return nil
}

// ListArchives lists archives sorted by name, narrowed by at most one filter.
func (m *Mock) ListArchives(_ context.Context, filter driver.ArchiveFilter) ([]driver.Archive, error) {
	if err := m.validateListFilter("ListArchives", filter.NamePrefix, filter.EventSourceARN, filter.State, archiveStates()); err != nil {
		return nil, err
	}

	busName := busNameFromARN(filter.EventSourceARN)
	if filter.EventSourceARN != "" && !m.buses.Has(busName) {
		return nil, errBusNotExist(busName)
	}

	out := make([]driver.Archive, 0, m.archives.Len())

	for _, ad := range m.archives.All() {
		a := m.archiveView(ad)

		if !strings.HasPrefix(a.Name, filter.NamePrefix) ||
			(filter.State != "" && a.State != filter.State) ||
			(filter.EventSourceARN != "" && busNameFromARN(a.EventSourceARN) != busName) {
			continue
		}

		out = append(out, a)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out, nil
}

// validateListFilter enforces the ListArchives/ListReplays rule that at most
// one of NamePrefix, EventSourceArn and State is given, and that State is one
// of the resource's states.
func (*Mock) validateListFilter(op, prefix, sourceARN, state string, states []string) error {
	if countSet(prefix, sourceARN, state) > 1 {
		return errors.Newf(errors.InvalidArgument,
			"At most one filter is allowed for %s. Use either : State, EventSourceArn, or NamePrefix.", op)
	}

	if state == "" {
		return nil
	}

	var v violations

	v.enum(state, "state", states)

	return v.err()
}

// captureToArchives adds an event just put on busName to every enabled archive
// on that bus whose pattern it matches. Replayed events never come through here.
// No other lock is held while an archive's lock is taken.
func (m *Mock) captureToArchives(busName string, event *driver.Event) {
	if m.archives.Len() == 0 {
		return
	}

	var size int

	for _, ad := range m.archives.All() {
		if ad.busName != busName {
			continue
		}

		if size == 0 {
			size = len(m.eventEnvelope(event))
		}

		m.captureOne(ad, event, size)
	}
}

func (m *Mock) captureOne(ad *archiveData, event *driver.Event, size int) {
	ad.mu.Lock()
	defer ad.mu.Unlock()

	if ad.info.State != archiveStateEnabled || !matchesPattern(event, ad.info.EventPattern) {
		return
	}

	m.pruneLocked(ad)

	ad.events = append(ad.events, archivedEvent{Event: *event, Archived: m.opts.Clock.Now().UTC(), Size: size})
}

// archiveEventsBetween returns a copy of the archive's events whose time falls
// in [start, end), in archive order.
func (m *Mock) archiveEventsBetween(ad *archiveData, start, end time.Time) []driver.Event {
	ad.mu.Lock()
	defer ad.mu.Unlock()

	m.pruneLocked(ad)

	out := make([]driver.Event, 0, len(ad.events))

	for i := range ad.events {
		t := ad.events[i].Event.Time
		if !t.Before(start) && t.Before(end) {
			out = append(out, ad.events[i].Event)
		}
	}

	return out
}
