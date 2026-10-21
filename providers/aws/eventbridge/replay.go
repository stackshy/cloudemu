package eventbridge

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/eventbus/driver"
)

const (
	replayStateStarting   = "STARTING"
	replayStateRunning    = "RUNNING"
	replayStateCancelling = "CANCELLING" //nolint:misspell // AWS ReplayState enum value
	replayStateCompleted  = "COMPLETED"
	replayStateCancelled  = "CANCELLED" //nolint:misspell // AWS ReplayState enum value

	maxReplayNameLen = 64

	// maxActiveReplays is how many replays may run at once in an account.
	maxActiveReplays = 10

	archiveARNInfix = ":archive/"
)

// StartReplay validation messages, as EventBridge words them.
const (
	msgDestinationNotBus = "Parameter Destination.Arn is not valid. Reason: Must contain an event bus ARN."
	msgCrossBusReplay    = "Parameter Destination.Arn is not valid. Reason: Cross event bus replay is not permitted."
	msgReplayWindow      = "Parameter EventEndTime is not valid. Reason: EventStartTime must be before EventEndTime."
	msgReplayLimit       = "The requested resource exceeds the maximum number allowed."
)

// replayStates is the ReplayState enum, in the order EventBridge lists it.
func replayStates() []string {
	return []string{
		replayStateStarting, replayStateRunning, replayStateCancelling,
		replayStateCompleted, replayStateCancelled, "FAILED",
	}
}

func errReplayNotExist(name string) error {
	return errors.Newf(errors.NotFound, "Replay %s does not exist.", name)
}

func validateReplayConfig(cfg *driver.ReplayConfig) error {
	var v violations

	v.name(cfg.Name, "replayName", maxReplayNameLen)
	v.eventsARN(cfg.EventSourceARN, "eventSourceArn")
	v.maxLen(cfg.Description, "description", maxDescriptionLen)

	if cfg.Destination.ARN == "" {
		v.add("", "destination.arn", "Member must have length greater than or equal to 1")
	}

	return v.err()
}

// replayTarget resolves and checks what a StartReplay call refers to: the
// destination bus must exist, the archive must exist, the destination must be
// the archive's own bus, and the time window must be non-empty.
func (m *Mock) replayTarget(cfg *driver.ReplayConfig) (*archiveData, error) {
	destBus := busNameFromARN(cfg.Destination.ARN)
	if destBus == "" {
		return nil, errors.New(errors.InvalidArgument, msgDestinationNotBus)
	}

	if !m.buses.Has(destBus) {
		return nil, errBusNotExist(destBus)
	}

	archiveName := cfg.EventSourceARN[strings.LastIndex(cfg.EventSourceARN, "/")+1:]

	ad, ok := m.archives.Get(archiveName)
	if !ok || !strings.Contains(cfg.EventSourceARN, archiveARNInfix) {
		return nil, errors.Newf(errors.InvalidArgument,
			"Parameter EventSourceArn is not valid. Reason: Archive %s does not exist.", archiveName)
	}

	if ad.busName != destBus {
		return nil, errors.New(errors.InvalidArgument, msgCrossBusReplay)
	}

	if !cfg.EventStartTime.Before(cfg.EventEndTime) {
		return nil, errors.New(errors.InvalidArgument, msgReplayWindow)
	}

	return ad, nil
}

// StartReplay replays the archived events in [EventStartTime, EventEndTime) to
// the archive's event bus, delivering each only to the rules in
// Destination.FilterARNs (or every enabled rule on the bus when none are given).
// Replayed events carry a "replay-name" field and are never archived again.
//
// The replay runs to completion before StartReplay returns, so the response
// reports STARTING while DescribeReplay right after reports COMPLETED.
func (m *Mock) StartReplay(ctx context.Context, cfg *driver.ReplayConfig) (*driver.Replay, error) {
	if err := validateReplayConfig(cfg); err != nil {
		return nil, err
	}

	ad, err := m.replayTarget(cfg)
	if err != nil {
		return nil, err
	}

	if m.activeReplays.Add(1) > maxActiveReplays {
		m.activeReplays.Add(-1)

		return nil, errors.New(errors.ResourceExhausted, msgReplayLimit)
	}
	defer m.activeReplays.Add(-1)

	replay := driver.Replay{
		Name:            cfg.Name,
		ARN:             idgen.AWSARN("events", m.opts.Region, m.opts.AccountID, "replay/"+cfg.Name),
		Description:     cfg.Description,
		State:           replayStateStarting,
		EventSourceARN:  cfg.EventSourceARN,
		Destination:     driver.ReplayDestination{ARN: cfg.Destination.ARN, FilterARNs: append([]string(nil), cfg.Destination.FilterARNs...)},
		EventStartTime:  cfg.EventStartTime.UTC(),
		EventEndTime:    cfg.EventEndTime.UTC(),
		ReplayStartTime: m.opts.Clock.Now().UTC(),
	}

	if !m.replays.SetIfAbsent(cfg.Name, replay) {
		return nil, errors.Newf(errors.AlreadyExists, "Replay %s already exists.", cfg.Name)
	}

	started := replay

	m.runReplay(ctx, &replay, ad)

	return &started, nil
}

// runReplay delivers the replay's events with no lock held, stopping early once
// a stop request has been recorded, then records the final state.
func (m *Mock) runReplay(ctx context.Context, replay *driver.Replay, ad *archiveData) {
	m.setReplayState(replay.Name, replayStateStarting, replayStateRunning)

	events := m.archiveEventsBetween(ad, replay.EventStartTime, replay.EventEndTime)
	filter := make(map[string]bool, len(replay.Destination.FilterARNs))

	for _, arn := range replay.Destination.FilterARNs {
		filter[ruleKeyFromARN(arn)] = true
	}

	var last driver.Event

	for i := range events {
		if r, ok := m.replays.Get(replay.Name); !ok || r.State == replayStateCancelling {
			break
		}

		m.replayEvent(ctx, &events[i], replay.Name, filter)
		last = events[i]
	}

	now := m.opts.Clock.Now().UTC()

	m.replays.Update(replay.Name, func(r driver.Replay) driver.Replay {
		r.ReplayEndTime = now
		if !last.Time.IsZero() {
			r.EventLastReplayedTime = last.Time.UTC()
		}

		if r.State == replayStateCancelling {
			r.State = replayStateCancelled
		} else {
			r.State = replayStateCompleted
		}

		return r
	})
}

// replayEvent delivers one archived event to the matching rules of its bus that
// the replay's filter allows, with the replay name added to the envelope.
func (m *Mock) replayEvent(ctx context.Context, event *driver.Event, replayName string, filter map[string]bool) {
	matched := m.MatchedRules(event)

	if len(filter) > 0 {
		kept := matched[:0]

		for i := range matched {
			if filter[ruleKey(matched[i].EventBus, matched[i].Name)] {
				kept = append(kept, matched[i])
			}
		}

		matched = kept
	}

	m.deliverEnvelope(ctx, matched, event, m.replayEnvelope(event, replayName))
}

// ruleKey identifies a rule by its bus and name, the way its ARN does: the
// default bus is omitted ("rule/<name>"), a custom bus is not
// ("rule/<bus>/<name>").
func ruleKey(bus, name string) string {
	if bus == "" || bus == defaultBusName {
		return name
	}

	return bus + "/" + name
}

// ruleKeyFromARN returns the ruleKey a rule ARN refers to. The account and
// region segments are not compared, so a FilterArns entry matches the rule it
// names however the caller spelled those.
func ruleKeyFromARN(arn string) string {
	const infix = ":rule/"

	if idx := strings.Index(arn, infix); idx != -1 {
		return arn[idx+len(infix):]
	}

	return arn
}

// setReplayState moves a replay from one state to another, doing nothing when
// it is no longer in the expected state.
func (m *Mock) setReplayState(name, from, to string) {
	m.replays.Update(name, func(r driver.Replay) driver.Replay {
		if r.State == from {
			r.State = to
		}

		return r
	})
}

// DescribeReplay returns a replay and its progress.
func (m *Mock) DescribeReplay(_ context.Context, name string) (*driver.Replay, error) {
	r, ok := m.replays.Get(name)
	if !ok {
		return nil, errReplayNotExist(name)
	}

	return &r, nil
}

// CancelReplay asks a starting or running replay to stop. The replay reports the
// stopping state until its runner exits, then the final stopped state; a replay
// in any other state cannot be stopped.
func (m *Mock) CancelReplay(_ context.Context, name string) (*driver.Replay, error) {
	var (
		out     driver.Replay
		illegal bool
	)

	found := m.replays.Update(name, func(r driver.Replay) driver.Replay {
		if r.State != replayStateStarting && r.State != replayStateRunning {
			illegal = true
		} else {
			r.State = replayStateCancelling
		}

		out = r

		return r
	})

	if !found {
		return nil, errReplayNotExist(name)
	}

	if illegal {
		return nil, fmt.Errorf("%w: %w", driver.ErrIllegalStatus,
			errors.Newf(errors.FailedPrecondition, "Replay %s is not in a valid state for this operation.", name))
	}

	return &out, nil
}

// ListReplays lists replays sorted by name, narrowed by at most one filter.
func (m *Mock) ListReplays(_ context.Context, filter driver.ReplayFilter) ([]driver.Replay, error) {
	if err := m.validateListFilter("ListReplays", filter.NamePrefix, filter.EventSourceARN, filter.State, replayStates()); err != nil {
		return nil, err
	}

	out := make([]driver.Replay, 0, m.replays.Len())

	all := m.replays.All()

	for name := range all {
		r := all[name]
		if !strings.HasPrefix(r.Name, filter.NamePrefix) ||
			(filter.State != "" && r.State != filter.State) ||
			(filter.EventSourceARN != "" && r.EventSourceARN != filter.EventSourceARN) {
			continue
		}

		out = append(out, r)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out, nil
}
