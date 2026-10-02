package ssm

import (
	"context"
	"slices"
	"sort"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
	ssmdriver "github.com/stackshy/cloudemu/v2/providers/aws/ssm/driver"
)

// Command filter keys and limits.
const (
	filterInvokedAfter   = "InvokedAfter"
	filterInvokedBefore  = "InvokedBefore"
	filterStatus         = "Status"
	filterExecutionStage = "ExecutionStage"
	filterDocumentName   = "DocumentName"

	stageExecuting = "Executing"
	stageComplete  = "Complete"

	maxCommandFilters = 5
	commandIDLength   = 36
)

// commandStatusValues are the Status filter values ListCommands accepts.
func commandStatusValues() []string {
	return []string{
		"Pending", "InProgress", "Success", "Cancelled", "Failed", "TimedOut", "AccessDenied", //nolint:misspell // SSM literal.
		"DeliveryTimedOut", "ExecutionTimedOut", "Incomplete", "NoInstancesInTag", "LimitExceeded",
	}
}

// invocationStatusValues are the Status filter values ListCommandInvocations
// accepts.
func invocationStatusValues() []string {
	return []string{
		"Pending", "InProgress", "Delayed", "Success", "Cancelled", "Failed", "TimedOut", "AccessDenied", //nolint:misspell // SSM literal.
		"DeliveryTimedOut", "ExecutionTimedOut", "Undeliverable", "InvalidPlatform", "Terminated",
	}
}

// commandMatcher is a validated CommandQuery.
type commandMatcher struct {
	q      ssmdriver.CommandQuery
	after  *time.Time
	before *time.Time
	status string
	stage  string
	doc    string
}

// newCommandMatcher validates q. forCommands selects the ListCommands rules;
// ListCommandInvocations does not take ExecutionStage.
func newCommandMatcher(q ssmdriver.CommandQuery, forCommands bool) (*commandMatcher, error) {
	if q.CommandID != "" && len(q.CommandID) != commandIDLength {
		return nil, ssmErrf(excValidation, errors.InvalidArgument,
			"1 validation error detected: Value '%s' at 'commandId' failed to satisfy constraint: "+
				"Member must have length greater than or equal to 36", q.CommandID)
	}

	if q.InstanceID != "" && !instanceIDPattern.MatchString(q.InstanceID) {
		return nil, patternErr(q.InstanceID, "instanceId", `(^i-(\w{8}|\w{17})$)|(^mi-\w{17}$)`)
	}

	if len(q.Filters) > maxCommandFilters {
		return nil, ssmErrf(excValidation, errors.InvalidArgument,
			"1 validation error detected: Value at 'filters' failed to satisfy constraint: "+
				"Member must have length less than or equal to %d", maxCommandFilters)
	}

	mt := &commandMatcher{q: q}

	for _, f := range q.Filters {
		if err := mt.add(f, forCommands); err != nil {
			return nil, err
		}
	}

	return mt, nil
}

func (mt *commandMatcher) add(f ssmdriver.CommandFilter, forCommands bool) error {
	statuses := invocationStatusValues()
	if forCommands {
		statuses = commandStatusValues()
	}

	switch f.Key {
	case filterInvokedAfter:
		return parseFilterTime(f.Value, &mt.after)
	case filterInvokedBefore:
		return parseFilterTime(f.Value, &mt.before)
	case filterStatus:
		if !slices.Contains(statuses, f.Value) {
			return ssmErrf(excValidation, errors.InvalidArgument, "The filter value %s is not a valid Status.", f.Value)
		}

		mt.status = f.Value
	case filterExecutionStage:
		return mt.setStage(f.Value, forCommands)
	case filterDocumentName:
		mt.doc = f.Value
	default:
		return ssmErrf(excInvalidFilterKey, errors.InvalidArgument, "The filter key %s is not valid.", f.Key)
	}

	return nil
}

// setStage applies an ExecutionStage filter, which only ListCommands takes.
func (mt *commandMatcher) setStage(v string, forCommands bool) error {
	if !forCommands {
		return ssmErrf(excInvalidFilterKey, errors.InvalidArgument,
			"The ExecutionStage filter can't be used with ListCommandInvocations.")
	}

	if v != stageExecuting && v != stageComplete {
		return ssmErrf(excValidation, errors.InvalidArgument,
			"The filter value %s is not a valid ExecutionStage. Use Executing or Complete.", v)
	}

	mt.stage = v

	return nil
}

// parseFilterTime reads an InvokedAfter/InvokedBefore value into *dst.
func parseFilterTime(v string, dst **time.Time) error {
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return ssmErrf(excValidation, errors.InvalidArgument,
			"The filter value %s is not a valid timestamp. Use the format 2024-07-07T00:00:00Z.", v)
	}

	*dst = &t

	return nil
}

// matchCommand checks the command-level criteria (everything but InstanceID).
func (mt *commandMatcher) matchCommand(cmd *ssmdriver.Command) bool {
	switch {
	case mt.q.CommandID != "" && cmd.CommandID != mt.q.CommandID:
		return false
	case mt.after != nil && cmd.RequestedDateTime.Before(*mt.after):
		return false
	case mt.before != nil && cmd.RequestedDateTime.After(*mt.before):
		return false
	case mt.doc != "" && cmd.DocumentName != mt.doc:
		return false
	}

	return true
}

// matchStatus checks the Status and ExecutionStage filters.
func (mt *commandMatcher) matchStatus(status, details string, terminal bool) bool {
	if mt.status != "" && mt.status != status && mt.status != details {
		return false
	}

	switch mt.stage {
	case stageExecuting:
		return !terminal
	case stageComplete:
		return terminal
	default:
		return true
	}
}

// sortedRecords returns every command record, newest first. The caller holds
// cmdMu.
func (m *Mock) sortedRecords() []*commandRecord {
	all := m.commands.All()
	out := make([]*commandRecord, 0, len(all))

	for _, r := range all {
		out = append(out, r)
	}

	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].Command, out[j].Command
		if !a.RequestedDateTime.Equal(b.RequestedDateTime) {
			return a.RequestedDateTime.After(b.RequestedDateTime)
		}

		return a.CommandID < b.CommandID
	})

	return out
}

func (r *commandRecord) hasInstance(id string) bool {
	for _, inv := range r.Invocations {
		if inv.InstanceID == id {
			return true
		}
	}

	return false
}

// ListCommands returns the matching commands, newest first.
func (m *Mock) ListCommands(ctx context.Context, q ssmdriver.CommandQuery) ([]ssmdriver.Command, error) {
	mt, err := newCommandMatcher(q, true)
	if err != nil {
		return nil, err
	}

	now := m.opts.Clock.Now()
	m.flushOutputs(ctx, now)

	m.cmdMu.RLock()
	defer m.cmdMu.RUnlock()

	out := make([]ssmdriver.Command, 0)

	for _, r := range m.sortedRecords() {
		if !mt.matchCommand(&r.Command) || (q.InstanceID != "" && !r.hasInstance(q.InstanceID)) {
			continue
		}

		cmd, _ := r.observe(now)
		if mt.matchStatus(cmd.Status, cmd.StatusDetails, int(cmd.CompletedCount) == int(cmd.TargetCount)) {
			out = append(out, cmd)
		}
	}

	return out, nil
}

// ListCommandInvocations returns the matching invocations, newest command
// first.
func (m *Mock) ListCommandInvocations(
	ctx context.Context, q ssmdriver.CommandQuery, details bool,
) ([]ssmdriver.CommandInvocation, error) {
	mt, err := newCommandMatcher(q, false)
	if err != nil {
		return nil, err
	}

	now := m.opts.Clock.Now()
	m.flushOutputs(ctx, now)

	m.cmdMu.RLock()
	defer m.cmdMu.RUnlock()

	out := make([]ssmdriver.CommandInvocation, 0)

	for _, r := range m.sortedRecords() {
		if !mt.matchCommand(&r.Command) {
			continue
		}

		_, states := r.observe(now)

		for i, inv := range r.Invocations {
			s := states[i]
			if (q.InstanceID != "" && inv.InstanceID != q.InstanceID) || !mt.matchStatus(s.status, s.details, s.terminal()) {
				continue
			}

			ci := r.render(i, &s)
			if !details {
				ci.Plugins = nil
			}

			out = append(out, ci)
		}
	}

	return out, nil
}

// GetCommandInvocation returns one instance's run, narrowed to pluginName
// when it is set.
//
// An unknown pair is InvocationDoesNotExist rather than a fabricated success:
// a caller polling a command it never sent has a real bug, and answering
// "Success" would bury it.
func (m *Mock) GetCommandInvocation(
	ctx context.Context, commandID, instanceID, pluginName string,
) (*ssmdriver.CommandInvocation, error) {
	if len(commandID) != commandIDLength {
		return nil, ssmErrf(excValidation, errors.InvalidArgument,
			"1 validation error detected: Value '%s' at 'commandId' failed to satisfy constraint: "+
				"Member must have length greater than or equal to 36", commandID)
	}

	now := m.opts.Clock.Now()
	m.flushOutputs(ctx, now)

	m.cmdMu.RLock()
	defer m.cmdMu.RUnlock()

	r, ok := m.commands.Get(commandID)
	if !ok {
		return nil, invocationMissing(commandID, instanceID)
	}

	for i, inv := range r.Invocations {
		if inv.InstanceID != instanceID {
			continue
		}

		_, states := r.observe(now)
		ci := r.render(i, &states[i])

		if err := narrowToPlugin(&ci, pluginName); err != nil {
			return nil, err
		}

		return &ci, nil
	}

	return nil, invocationMissing(commandID, instanceID)
}

func invocationMissing(commandID, instanceID string) error {
	return ssmErrf(excInvocationDoesNotExist, errors.NotFound,
		"No invocation of command %s on instance %s.", commandID, instanceID)
}

// narrowToPlugin fills the invocation's plugin fields from the named plugin,
// or from the first one when none is named.
func narrowToPlugin(ci *ssmdriver.CommandInvocation, name string) error {
	if len(ci.Plugins) == 0 {
		if name != "" {
			return ssmErrf(excInvalidPluginName, errors.InvalidArgument, "The plugin name %s is not valid.", name)
		}

		return nil
	}

	p := &ci.Plugins[0]

	if name != "" {
		idx := slices.IndexFunc(ci.Plugins, func(c ssmdriver.CommandPlugin) bool { return c.Name == name })
		if idx < 0 {
			return ssmErrf(excInvalidPluginName, errors.InvalidArgument, "The plugin name %s is not valid.", name)
		}

		p = &ci.Plugins[idx]
		ci.Status, ci.StatusDetails = p.Status, p.StatusDetails
	}

	ci.PluginName = p.Name
	ci.ResponseCode = p.ResponseCode
	ci.ExecutionStartTime, ci.ExecutionEndTime = p.ResponseStartDateTime, p.ResponseFinishDateTime
	ci.Stdout = p.Output
	ci.StandardOutputURL, ci.StandardErrorURL = p.StandardOutputURL, p.StandardErrorURL

	return nil
}

// render builds invocation i from its observed state.
func (r *commandRecord) render(i int, s *invocationState) ssmdriver.CommandInvocation {
	cmd := &r.Command
	inv := r.Invocations[i]

	ci := ssmdriver.CommandInvocation{
		CommandID: cmd.CommandID, InstanceID: inv.InstanceID, Comment: cmd.Comment,
		DocumentName: cmd.DocumentName, DocumentVersion: cmd.DocumentVersion, RequestedDateTime: cmd.RequestedDateTime,
		Status: s.status, StatusDetails: s.details, ResponseCode: s.code,
		ExecutionStartTime: s.start, ExecutionEndTime: s.end, ServiceRole: cmd.ServiceRole,
		Notification: cmd.Notification, CloudWatchOutput: cmd.CloudWatchOutput,
		Plugins: make([]ssmdriver.CommandPlugin, 0, len(r.Steps)),
	}

	pluginStatus := s.status
	if pluginStatus == ssmdriver.CommandDelayed {
		pluginStatus = ssmdriver.CommandPending
	}

	for _, step := range r.Steps {
		p := ssmdriver.CommandPlugin{
			Name: step.Name, Status: pluginStatus, StatusDetails: s.details, ResponseCode: s.code,
			ResponseStartDateTime: s.start, ResponseFinishDateTime: s.end,
		}

		if cmd.OutputS3BucketName != "" {
			p.OutputS3Region, p.OutputS3BucketName = cmd.OutputS3Region, cmd.OutputS3BucketName
			p.OutputS3KeyPrefix = r.outputFolder(inv.InstanceID, step)
			p.StandardOutputURL = r.outputURL(p.OutputS3KeyPrefix + "/stdout")
			p.StandardErrorURL = r.outputURL(p.OutputS3KeyPrefix + "/stderr")
		}

		ci.Plugins = append(ci.Plugins, p)
	}

	// An invocation carries the output URLs only when the document has one
	// plugin.
	if len(ci.Plugins) == 1 {
		ci.StandardOutputURL, ci.StandardErrorURL = ci.Plugins[0].StandardOutputURL, ci.Plugins[0].StandardErrorURL
	}

	return ci
}

// CancelCommand cancels the unfinished invocations of a command, on the given
// instances or on all of them. Finished invocations keep their result.
func (m *Mock) CancelCommand(_ context.Context, commandID string, instanceIDs []string) error {
	m.cmdMu.Lock()
	defer m.cmdMu.Unlock()

	r, ok := m.commands.Get(commandID)
	if !ok {
		return ssmErrf(excInvalidCommandID, errors.NotFound, "The command ID %s is not valid.", commandID)
	}

	for _, id := range instanceIDs {
		if !r.hasInstance(id) {
			return ssmErrf(excInvalidInstanceID, errors.NotFound,
				"Instance %s is not a target of command %s.", id, commandID)
		}
	}

	now := m.opts.Clock.Now()
	_, states := r.observe(now)

	for i, inv := range r.Invocations {
		if len(instanceIDs) > 0 && !slices.Contains(instanceIDs, inv.InstanceID) {
			continue
		}

		if !states[i].terminal() {
			inv.CancelledAt = now
		}
	}

	return nil
}
