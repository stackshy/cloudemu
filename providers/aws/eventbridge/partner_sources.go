package eventbridge

import (
	"context"
	"fmt"
	"maps"
	"regexp"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/pagination"
	"github.com/stackshy/cloudemu/v2/services/eventbus/driver"
)

// Partner event source states.
const (
	sourceStatePending = "PENDING"
	sourceStateActive  = "ACTIVE"
	sourceStateDeleted = "DELETED"
)

const (
	maxEventSourceNameLen = 256
	maxSourcePageSize     = 100
	maxPartnerEntries     = 20
	maxEventResourceLen   = 2048

	// sourcePendingTTL is how long a source may stay PENDING before AWS deletes
	// it. It sets ExpirationTime; the expiry itself is not enforced.
	sourcePendingTTL = 14 * 24 * time.Hour

	partnerNamePrefix = "aws.partner/"
)

// Patterns of the partner event source members, as the EventBridge API model
// spells them.
const (
	eventSourceNamePattern    = `aws\.partner(/[\.\-_A-Za-z0-9]+){2,}`
	accountIDPattern          = `\d{12}`
	partnerSourcePrefixPatt   = `aws\.partner/[\.\-_A-Za-z0-9]+/[/\.\-_A-Za-z0-9]*`
	eventSourcePrefixPattern  = `[/\.\-_A-Za-z0-9]+`
	accountIDLen              = 12
	partnerSourceField        = "name"
	partnerSourceAccountField = "account"
)

var (
	eventSourceNameRE     = regexp.MustCompile(`^aws\.partner(/[.\-_A-Za-z0-9]+){2,}$`)
	accountIDRE           = regexp.MustCompile(`^\d{12}$`)
	partnerSourcePrefixRE = regexp.MustCompile(`^aws\.partner/[.\-_A-Za-z0-9]+/[/.\-_A-Za-z0-9]*$`)
	eventSourcePrefixRE   = regexp.MustCompile(`^[/.\-_A-Za-z0-9]+$`)
)

// partnerAccount is a partner event source's state in one customer account.
type partnerAccount struct {
	State     string    `json:"state"`
	CreatedAt time.Time `json:"createdAt"`
	// PendingSince is when the source last entered PENDING; zero otherwise.
	PendingSince time.Time `json:"pendingSince,omitzero"`
}

// partnerSource is a partner event source and the customer accounts it was
// created for. Accounts is never mutated in place: writers store a copy, so a
// value read from the store stays safe to use.
type partnerSource struct {
	Name     string                    `json:"name"`
	Accounts map[string]partnerAccount `json:"accounts"`
}

func errEventSourceNotExist(name string) error {
	return errors.Newf(errors.NotFound, "Event source %s does not exist.", name)
}

func errEventSourceState(name, state string) error {
	return errors.Newf(errors.FailedPrecondition, "Event source %s is in state %s.", name, state)
}

// eventSourceARN is a partner event source's ARN. It has no account segment.
func (m *Mock) eventSourceARN(name string) string {
	return "arn:aws:events:" + m.opts.Region + "::event-source/" + name
}

// partnerOf returns the partner name segment of a source name, as in "acme.com"
// for "aws.partner/acme.com/app/events".
func partnerOf(name string) string {
	rest := strings.TrimPrefix(name, partnerNamePrefix)
	if i := strings.Index(rest, "/"); i != -1 {
		return rest[:i]
	}

	return rest
}

func (v *violations) eventSourceName(value, field string) {
	if v.present(value, field) {
		v.lengthBetween(value, field, 1, maxEventSourceNameLen)
		v.pattern(value, field, eventSourceNameRE, eventSourceNamePattern)
	}
}

func (v *violations) accountID(value, field string) {
	if v.present(value, field) {
		v.lengthBetween(value, field, accountIDLen, accountIDLen)
		v.pattern(value, field, accountIDRE, accountIDPattern)
	}
}

func (v *violations) pageLimit(limit int) {
	if limit < 0 || limit > maxSourcePageSize {
		v.add(fmt.Sprint(limit), "limit",
			fmt.Sprintf("Member must have value less than or equal to %d and greater than or equal to 1", maxSourcePageSize))
	}
}

func validateSourceName(name string) error {
	var v violations

	v.eventSourceName(name, partnerSourceField)

	return v.err()
}

func validateSourceAndAccount(name, account string) error {
	var v violations

	v.eventSourceName(name, partnerSourceField)
	v.accountID(account, partnerSourceAccountField)

	return v.err()
}

// expiresAt is the ExpirationTime of a source in state a: two weeks after it
// entered PENDING, and unset in any other state.
func expiresAt(a *partnerAccount) time.Time {
	if a.State != sourceStatePending || a.PendingSince.IsZero() {
		return time.Time{}
	}

	return a.PendingSince.Add(sourcePendingTTL)
}

// CreatePartnerEventSource creates a partner event source for a customer
// account, in PENDING. The account may be this emulator's own. A source deleted
// for that account can be created again.
func (m *Mock) CreatePartnerEventSource(_ context.Context, name, account string) (string, error) {
	if err := validateSourceAndAccount(name, account); err != nil {
		return "", err
	}

	now := m.opts.Clock.Now().UTC()
	entry := partnerAccount{State: sourceStatePending, CreatedAt: now, PendingSince: now}

	if m.partnerSources.SetIfAbsent(name, partnerSource{Name: name, Accounts: map[string]partnerAccount{account: entry}}) {
		return m.eventSourceARN(name), nil
	}

	var exists bool

	m.partnerSources.Update(name, func(s partnerSource) partnerSource {
		if a, ok := s.Accounts[account]; ok && a.State != sourceStateDeleted {
			exists = true

			return s
		}

		s.Accounts = maps.Clone(s.Accounts)
		s.Accounts[account] = entry

		return s
	})

	if exists {
		return "", errors.Newf(errors.AlreadyExists, "Event source %s already exists.", name)
	}

	return m.eventSourceARN(name), nil
}

// DeletePartnerEventSource moves a partner event source to DELETED for one
// customer account. The partner event bus stays, but receives no more events.
// Deleting a source that does not exist for that account succeeds.
func (m *Mock) DeletePartnerEventSource(_ context.Context, name, account string) error {
	if err := validateSourceAndAccount(name, account); err != nil {
		return err
	}

	m.partnerSources.Update(name, func(s partnerSource) partnerSource {
		a, ok := s.Accounts[account]
		if !ok {
			return s
		}

		a.State, a.PendingSince = sourceStateDeleted, time.Time{}
		s.Accounts = maps.Clone(s.Accounts)
		s.Accounts[account] = a

		return s
	})

	return nil
}

// DescribePartnerEventSource returns a partner event source as its partner sees
// it.
func (m *Mock) DescribePartnerEventSource(_ context.Context, name string) (*driver.PartnerEventSource, error) {
	if err := validateSourceName(name); err != nil {
		return nil, err
	}

	if !m.partnerSources.Has(name) {
		return nil, errEventSourceNotExist(name)
	}

	return &driver.PartnerEventSource{Name: name, ARN: m.eventSourceARN(name)}, nil
}

// ListPartnerEventSources lists the partner event sources whose names start
// with the required NamePrefix, sorted by name, one page at a time.
func (m *Mock) ListPartnerEventSources(
	_ context.Context, f driver.EventSourceFilter,
) ([]driver.PartnerEventSource, string, error) {
	var v violations

	if v.present(f.NamePrefix, "namePrefix") {
		v.lengthBetween(f.NamePrefix, "namePrefix", 1, maxEventSourceNameLen)
		v.pattern(f.NamePrefix, "namePrefix", partnerSourcePrefixRE, partnerSourcePrefixPatt)
	}

	v.pageLimit(f.Limit)

	if err := v.err(); err != nil {
		return nil, "", err
	}

	var out []driver.PartnerEventSource

	for _, name := range m.partnerSources.Keys() {
		if strings.HasPrefix(name, f.NamePrefix) {
			out = append(out, driver.PartnerEventSource{Name: name, ARN: m.eventSourceARN(name)})
		}
	}

	return pageOf(out, func(a, b driver.PartnerEventSource) bool { return a.Name < b.Name }, f)
}

// ListPartnerEventSourceAccounts lists the customer accounts a partner event
// source was created for, sorted by account, one page at a time.
func (m *Mock) ListPartnerEventSourceAccounts(
	_ context.Context, name string, limit int, nextToken string,
) ([]driver.PartnerEventSourceAccount, string, error) {
	var v violations

	v.eventSourceName(name, "eventSourceName")
	v.pageLimit(limit)

	if err := v.err(); err != nil {
		return nil, "", err
	}

	src, ok := m.partnerSources.Get(name)
	if !ok {
		return nil, "", errEventSourceNotExist(name)
	}

	out := make([]driver.PartnerEventSourceAccount, 0, len(src.Accounts))

	for account, a := range src.Accounts {
		out = append(out, driver.PartnerEventSourceAccount{
			Account: account, State: a.State, CreatedAt: a.CreatedAt, ExpiresAt: expiresAt(&a),
		})
	}

	return pageOf(out, func(a, b driver.PartnerEventSourceAccount) bool { return a.Account < b.Account },
		driver.EventSourceFilter{Limit: limit, NextToken: nextToken})
}

// pageOf sorts items with less and returns the page f asks for.
func pageOf[T any](items []T, less func(a, b T) bool, f driver.EventSourceFilter) (page []T, next string, err error) {
	p, perr := pagination.PaginateSorted(items, less, f.NextToken, f.Limit)
	if perr != nil {
		return nil, "", errors.New(errors.InvalidArgument, msgBadNextPage)
	}

	return p.Items, p.NextPageToken, nil
}

// ownSource returns this account's view of a partner event source.
func (m *Mock) ownSource(name string) (driver.EventSource, bool) {
	src, ok := m.partnerSources.Get(name)
	if !ok {
		return driver.EventSource{}, false
	}

	a, ok := src.Accounts[m.opts.AccountID]
	if !ok {
		return driver.EventSource{}, false
	}

	return driver.EventSource{
		Name: name, ARN: m.eventSourceARN(name), CreatedBy: partnerOf(name),
		State: a.State, CreatedAt: a.CreatedAt, ExpiresAt: expiresAt(&a),
	}, true
}

// DescribeEventSource returns a partner event source shared with this account.
func (m *Mock) DescribeEventSource(_ context.Context, name string) (*driver.EventSource, error) {
	if err := validateSourceName(name); err != nil {
		return nil, err
	}

	src, ok := m.ownSource(name)
	if !ok {
		return nil, errEventSourceNotExist(name)
	}

	return &src, nil
}

// ListEventSources lists the partner event sources shared with this account,
// sorted by name, one page at a time.
func (m *Mock) ListEventSources(_ context.Context, f driver.EventSourceFilter) ([]driver.EventSource, string, error) {
	var v violations

	if f.NamePrefix != "" {
		v.lengthBetween(f.NamePrefix, "namePrefix", 1, maxEventSourceNameLen)
		v.pattern(f.NamePrefix, "namePrefix", eventSourcePrefixRE, eventSourcePrefixPattern)
	}

	v.pageLimit(f.Limit)

	if err := v.err(); err != nil {
		return nil, "", err
	}

	var out []driver.EventSource

	for _, name := range m.partnerSources.Keys() {
		if !strings.HasPrefix(name, f.NamePrefix) {
			continue
		}

		if src, ok := m.ownSource(name); ok {
			out = append(out, src)
		}
	}

	return pageOf(out, func(a, b driver.EventSource) bool { return a.Name < b.Name }, f)
}

// transitionOwnSource applies fn to this account's entry of a partner event
// source in one atomic step. fn reports an error to leave the entry unchanged.
func (m *Mock) transitionOwnSource(name string, fn func(a *partnerAccount) error) error {
	notFound := true

	var applyErr error

	m.partnerSources.Update(name, func(s partnerSource) partnerSource {
		a, ok := s.Accounts[m.opts.AccountID]
		if !ok {
			return s
		}

		notFound = false

		if applyErr = fn(&a); applyErr != nil {
			return s
		}

		s.Accounts = maps.Clone(s.Accounts)
		s.Accounts[m.opts.AccountID] = a

		return s
	})

	if notFound {
		return errEventSourceNotExist(name)
	}

	return applyErr
}

// activate moves a PENDING or ACTIVE source to ACTIVE.
func activate(name string) func(a *partnerAccount) error {
	return func(a *partnerAccount) error {
		if a.State == sourceStateDeleted {
			return errEventSourceState(name, a.State)
		}

		a.State, a.PendingSince = sourceStateActive, time.Time{}

		return nil
	}
}

// ActivateEventSource moves a partner event source shared with this account
// from PENDING to ACTIVE, so its partner event bus receives events again.
func (m *Mock) ActivateEventSource(_ context.Context, name string) error {
	if err := validateSourceName(name); err != nil {
		return err
	}

	return m.transitionOwnSource(name, activate(name))
}

// DeactivateEventSource moves a partner event source shared with this account
// from ACTIVE to PENDING. Its partner event bus stays but receives no events.
func (m *Mock) DeactivateEventSource(_ context.Context, name string) error {
	if err := validateSourceName(name); err != nil {
		return err
	}

	now := m.opts.Clock.Now().UTC()

	return m.transitionOwnSource(name, func(a *partnerAccount) error {
		switch a.State {
		case sourceStateDeleted:
			return errEventSourceState(name, a.State)
		case sourceStateActive:
			a.State, a.PendingSince = sourceStatePending, now
		}

		return nil
	})
}

// checkPartnerBus validates a CreateEventBus for a partner event bus: the bus
// is named after its event source, which must be shared with this account and
// not deleted.
func (m *Mock) checkPartnerBus(cfg *driver.EventBusConfig) error {
	var v violations

	v.eventSourceName(cfg.EventSourceName, "eventSourceName")

	if err := v.err(); err != nil {
		return err
	}

	if cfg.Name != cfg.EventSourceName {
		return errInvalidParam("Name", "Event bus name must match the event source name "+cfg.EventSourceName+".")
	}

	src, ok := m.ownSource(cfg.EventSourceName)
	if !ok {
		return errEventSourceNotExist(cfg.EventSourceName)
	}

	if src.State == sourceStateDeleted {
		return errEventSourceState(src.Name, src.State)
	}

	return nil
}

// releasePartnerSource moves the event source a deleted partner event bus was
// matched to back to PENDING.
func (m *Mock) releasePartnerSource(busName string) {
	if !strings.HasPrefix(busName, partnerNamePrefix) {
		return
	}

	now := m.opts.Clock.Now().UTC()

	_ = m.transitionOwnSource(busName, func(a *partnerAccount) error {
		if a.State == sourceStateActive {
			a.State, a.PendingSince = sourceStatePending, now
		}

		return nil
	})
}

// partnerEntriesError checks the request-wide limits of a PutPartnerEvents
// call: 1 to 20 entries, a total size under the Region's limit, and each Source
// and Resource within the API model's constraints.
func partnerEntriesError(events []driver.Event, region string) error {
	if err := validateEntriesRequest(events, region, maxPartnerEntries); err != nil {
		return err
	}

	var v violations

	for i := range events {
		field := fmt.Sprintf("entries.%d.member.", i+1)

		if s := events[i].Source; s != "" {
			v.lengthBetween(s, field+"source", 1, maxEventSourceNameLen)
			v.pattern(s, field+"source", eventSourceNameRE, eventSourceNamePattern)
		}

		for j, r := range events[i].Resources {
			v.maxLen(r, fmt.Sprintf("%sresources.%d.member", field, j+1), maxEventResourceLen)
		}
	}

	return v.err()
}

// PutPartnerEventEntries publishes events sent by a SaaS partner, reporting each
// entry's outcome by index. An entry goes to the partner event bus named after
// its Source when that source is ACTIVE for this account. Otherwise it still
// gets an EventId but is dropped, as PutEvents does for a bus that does not
// exist.
func (m *Mock) PutPartnerEventEntries(ctx context.Context, events []driver.Event) ([]driver.PutEventsEntryResult, error) {
	if err := partnerEntriesError(events, m.opts.Region); err != nil {
		return nil, err
	}

	results := make([]driver.PutEventsEntryResult, len(events))

	for i := range events {
		if code, msg := validateEntry(&events[i]); code != "" {
			results[i] = driver.PutEventsEntryResult{ErrorCode: code, ErrorMessage: msg}

			continue
		}

		results[i] = driver.PutEventsEntryResult{EventID: m.publishPartnerEvent(ctx, &events[i], i)}
	}

	return results, nil
}

func (m *Mock) publishPartnerEvent(ctx context.Context, event *driver.Event, index int) string {
	if src, ok := m.ownSource(event.Source); ok && src.State == sourceStateActive {
		event.EventBus = event.Source

		return m.publishEvent(ctx, event, index)
	}

	return generateEventID(event, m.opts.Clock.Now(), index)
}
