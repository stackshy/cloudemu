package ssm

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/settle"
	ssmdriver "github.com/stackshy/cloudemu/v2/providers/aws/ssm/driver"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
)

var _ ssmdriver.RunCommand = (*Mock)(nil)

// SendCommand request limits and defaults from the SSM API reference.
const (
	defaultCommandTimeout = 3600
	minCommandTimeout     = 30
	maxCommandTimeout     = 2592000
	maxCommandComment     = 100
	maxCommandInstances   = 50
	maxCommandTargets     = 5
	defaultMaxConcurrency = "50"
	defaultMaxErrors      = "0"
	percent               = 100
)

// EC2 instance states Run Command cares about.
const (
	stateRunning      = "running"
	stateTerminated   = "terminated"
	stateShuttingDown = "shutting-down"
)

var (
	maxConcurrencyPattern = regexp.MustCompile(`^([1-9]\d*|[1-9]\d%|[1-9]%|100%)$`)
	maxErrorsPattern      = regexp.MustCompile(`^([1-9]\d*|0|[1-9]\d%|\d%|100%)$`)
	instanceIDPattern     = regexp.MustCompile(`^(i-(\w{8}|\w{17})|mi-\w{17})$`)
)

// InstanceResolver is the slice of the compute mock this package needs to
// check that a Run Command target exists.
type InstanceResolver interface {
	DescribeInstances(ctx context.Context, instanceIDs []string,
		filters []computedriver.DescribeFilter, opts ...computedriver.DescribeInstancesOptions) ([]computedriver.Instance, error)
}

// SetInstanceResolver wires the compute mock in so SendCommand can reject a
// target that does not exist. Without it, targets are not validated.
func (m *Mock) SetInstanceResolver(r InstanceResolver) {
	m.instanceResolver = r
}

// commandDoc is the document version a send runs.
type commandDoc struct {
	name   string
	specs  map[string]paramSpec
	steps  []commandStep
	strict bool // false for an AWS-owned name the catalog does not hold
}

// SendCommand validates a Run Command send, records it and returns the
// command as it stands right after the send.
//
// Nothing executes: an emulated instance has no guest operating system. Each
// invocation moves Pending -> InProgress -> Success over the settle windows
// (immediately when async settle is off), so a caller's send/poll loop runs to
// completion. See driver.RunCommand.
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) SendCommand(ctx context.Context, cfg ssmdriver.CommandConfig) (*ssmdriver.Command, error) {
	if err := validateCommandConfig(&cfg); err != nil {
		return nil, err
	}

	doc, err := m.commandDocument(cfg.DocumentName, cfg.DocumentVersion)
	if err != nil {
		return nil, err
	}

	resolved := cfg.Parameters

	if doc.strict {
		if resolved, err = validateParameters(doc.name, doc.specs, cfg.Parameters); err != nil {
			return nil, err
		}
	}

	// Real SSM answers InvalidInstanceId when an explicitly listed target is not
	// a managed instance, and that is the single most common Run Command failure
	// during bring-up. Accepting any id hides it until the caller runs for real.
	if err := m.checkTargets(ctx, cfg.InstanceIDs); err != nil {
		return nil, err
	}

	// Resolve tag/attribute Targets to concrete instance ids. Unlike an explicit
	// id, a Target that matches nothing is not an error. Real SSM accepts the
	// command with a TargetCount of zero.
	invocations := make([]*invocationRecord, 0, len(cfg.InstanceIDs))
	seen := map[string]bool{}

	for _, id := range cfg.InstanceIDs {
		if !seen[id] {
			seen[id] = true

			invocations = append(invocations, &invocationRecord{InstanceID: id})
		}
	}

	for _, t := range m.resolveTargets(ctx, cfg.Targets) {
		if !seen[t.ID] {
			seen[t.ID] = true

			invocations = append(invocations, &invocationRecord{InstanceID: t.ID, Offline: t.State != stateRunning})
		}
	}

	rec := m.newCommandRecord(&cfg, doc, resolved, invocations)

	// The response reports the command as real SSM does at acceptance:
	// Pending with nothing completed, even when it finishes at once here. The
	// caller learns the outcome by polling.
	cmd := rec.Command
	cmd.Status, cmd.StatusDetails = ssmdriver.CommandPending, ssmdriver.CommandPending
	cmd.TargetCount = int32(len(invocations)) //nolint:gosec // at most 50 explicit ids plus tag matches.

	m.cmdMu.Lock()
	m.commands.Set(rec.Command.CommandID, rec)
	m.cmdMu.Unlock()

	m.flushOutputs(ctx, m.opts.Clock.Now())

	return &cmd, nil
}

// newCommandRecord builds the stored record of a send.
func (m *Mock) newCommandRecord(cfg *ssmdriver.CommandConfig, doc commandDoc,
	resolved map[string][]string, invocations []*invocationRecord,
) *commandRecord {
	now := m.opts.Clock.Now()
	timeout := cfg.TimeoutSeconds

	if timeout == 0 {
		timeout = defaultCommandTimeout
	}

	version := cfg.DocumentVersion
	if version == "" {
		version = versionDefault
	}

	rec := &commandRecord{
		Command: ssmdriver.Command{
			CommandID: idgen.UUID(), DocumentName: doc.name, DocumentVersion: version, Comment: cfg.Comment,
			Parameters: cfg.Parameters, InstanceIDs: cfg.InstanceIDs, Targets: cfg.Targets,
			RequestedDateTime: now, OutputS3Region: cfg.OutputS3Region, OutputS3BucketName: cfg.OutputS3BucketName,
			OutputS3KeyPrefix: cfg.OutputS3KeyPrefix, MaxConcurrency: orDefault(cfg.MaxConcurrency, defaultMaxConcurrency),
			MaxErrors: orDefault(cfg.MaxErrors, defaultMaxErrors), ServiceRole: cfg.ServiceRoleArn,
			Notification: cfg.Notification, CloudWatchOutput: cfg.CloudWatchOutput, TimeoutSeconds: timeout,
		},
		Steps:       doc.steps,
		Delivery:    m.opts.SettleDuration(settle.DefaultCommandDeliverySettle),
		Run:         m.opts.SettleDuration(settle.DefaultCommandRunSettle),
		Invocations: invocations,
	}

	if rec.Command.OutputS3BucketName != "" && rec.Command.OutputS3Region == "" {
		rec.Command.OutputS3Region = m.opts.Region
	}

	if len(resolved["executionTimeout"]) == 1 {
		rec.ExecutionTimeout, _ = strconv.Atoi(resolved["executionTimeout"][0])
	}

	total := time.Duration(timeout) * time.Second
	if rec.ExecutionTimeout > 0 {
		total += time.Duration(rec.ExecutionTimeout) * time.Second
	} else {
		total += defaultCommandTimeout * time.Second
	}

	rec.Command.ExpiresAfter = now.Add(total)

	return rec
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}

	return v
}

// validateCommandConfig applies the SendCommand request constraints real SSM
// checks before it looks at the document.
//
//nolint:gocyclo // one flat check per request field.
func validateCommandConfig(cfg *ssmdriver.CommandConfig) error {
	// Real SSM accepts EITHER explicit InstanceIds OR tag/attribute Targets;
	// supplying neither is a ValidationException.
	if len(cfg.InstanceIDs) == 0 && len(cfg.Targets) == 0 {
		return errors.New(errors.InvalidArgument, "either instance IDs or targets must be specified")
	}

	if cfg.DocumentName == "" {
		return errors.New(errors.InvalidArgument, "DocumentName is required")
	}

	switch {
	case cfg.DocumentVersion != "" && !docVersionPattern.MatchString(cfg.DocumentVersion):
		return patternErr(cfg.DocumentVersion, "documentVersion", "([$]LATEST|[$]DEFAULT|^[1-9][0-9]*$)")
	case cfg.MaxConcurrency != "" && !maxConcurrencyPattern.MatchString(cfg.MaxConcurrency):
		return patternErr(cfg.MaxConcurrency, "maxConcurrency", maxConcurrencyPattern.String())
	case cfg.MaxErrors != "" && !maxErrorsPattern.MatchString(cfg.MaxErrors):
		return patternErr(cfg.MaxErrors, "maxErrors", maxErrorsPattern.String())
	case cfg.TimeoutSeconds != 0 && (cfg.TimeoutSeconds < minCommandTimeout || cfg.TimeoutSeconds > maxCommandTimeout):
		return ssmErrf(excValidation, errors.InvalidArgument,
			"1 validation error detected: Value '%d' at 'timeoutSeconds' failed to satisfy constraint: "+
				"Member must have value between %d and %d", cfg.TimeoutSeconds, minCommandTimeout, maxCommandTimeout)
	case len(cfg.Comment) > maxCommandComment:
		return lengthErr(cfg.Comment, "comment", maxCommandComment)
	case len(cfg.InstanceIDs) > maxCommandInstances:
		return ssmErrf(excValidation, errors.InvalidArgument,
			"1 validation error detected: Value at 'instanceIds' failed to satisfy constraint: "+
				"Member must have length less than or equal to %d", maxCommandInstances)
	case len(cfg.Targets) > maxCommandTargets:
		return ssmErrf(excValidation, errors.InvalidArgument,
			"1 validation error detected: Value at 'targets' failed to satisfy constraint: "+
				"Member must have length less than or equal to %d", maxCommandTargets)
	}

	for _, id := range cfg.InstanceIDs {
		if !instanceIDPattern.MatchString(id) {
			return patternErr(id, "instanceIds", `(^i-(\w{8}|\w{17})$)|(^mi-\w{17}$)`)
		}
	}

	return nil
}

func patternErr(value, field, pattern string) error {
	return ssmErrf(excValidation, errors.InvalidArgument,
		"1 validation error detected: Value '%s' at '%s' failed to satisfy constraint: "+
			"Member must satisfy regular expression pattern: %s", value, field, pattern)
}

func lengthErr(value, field string, limit int) error {
	return ssmErrf(excValidation, errors.InvalidArgument,
		"1 validation error detected: Value '%s' at '%s' failed to satisfy constraint: "+
			"Member must have length less than or equal to %d", value, field, limit)
}

// commandDocument resolves a SendCommand DocumentName (a name or an ARN) and
// DocumentVersion through the customer documents and the AWS-owned catalog.
// Only Command documents can be sent.
func (m *Mock) commandDocument(ref, version string) (commandDoc, error) {
	m.docMu.RLock()
	defer m.docMu.RUnlock()

	d, err := m.lookupDocument(ref)
	if err != nil {
		// The catalog holds the common AWS-owned documents, not all of them.
		// An unknown name in the AWS namespace is taken as an AWS-owned Command
		// document so a real one the catalog lacks still runs. Its parameters
		// are unknown, so they are not checked.
		if name := documentName(ref); awsOwnedName(name) {
			if version != "" && version != versionDefault && version != versionLatest && version != "1" {
				return commandDoc{}, ssmErrf(excInvalidDocumentVersion, errors.NotFound,
					"The document version isn't valid or doesn't exist.")
			}

			return commandDoc{name: name}, nil
		}

		return commandDoc{}, err
	}

	if d.docType != ssmdriver.DocumentTypeCommand {
		return commandDoc{}, ssmErrf(excInvalidDocument, errors.InvalidArgument,
			"Document %s of type %s can't be used with SendCommand.", d.name, d.docType)
	}

	v, err := d.resolveVersion(version, "")
	if err != nil {
		return commandDoc{}, err
	}

	return commandDoc{name: d.name, specs: v.meta.paramSpecs, steps: v.meta.steps, strict: true}, nil
}

// resolvedTarget is an instance a tag/attribute Target selected.
type resolvedTarget struct {
	ID    string
	State string
}

// resolveTargets maps SSM Targets to the instances they select. Multiple
// targets are AND-combined, matching real SSM. Terminated instances are not
// managed nodes and are left out. Resolution needs the compute mock; without it
// (or on a lookup error) nothing is resolved, which still yields an accepted
// command.
func (m *Mock) resolveTargets(ctx context.Context, targets []ssmdriver.CommandTarget) []resolvedTarget {
	if len(targets) == 0 || m.instanceResolver == nil {
		return nil
	}

	filters := make([]computedriver.DescribeFilter, 0, len(targets))

	for _, t := range targets {
		name, ok := targetFilterName(t.Key)
		if !ok {
			// A documented Run Command target key the emulator cannot resolve
			// (resource-groups:Name, resource-groups:ResourceTypeFilters, tag-key).
			// Targets are AND-combined, so an unresolvable one must select nothing.
			// Forwarding the raw key as an EC2 describe-filter name instead falls
			// into matchesTagFilter's default branch, which matches every instance
			// unconditionally, fanning the command out to the whole fleet.
			return nil
		}

		filters = append(filters, computedriver.DescribeFilter{
			Name: name, Values: t.Values,
		})
	}

	found, err := m.instanceResolver.DescribeInstances(ctx, nil, filters,
		computedriver.DescribeInstancesOptions{IncludeManagedResources: true})
	if err != nil {
		return nil
	}

	out := make([]resolvedTarget, 0, len(found))

	for i := range found {
		if managedState(found[i].State) {
			out = append(out, resolvedTarget{ID: found[i].ID, State: found[i].State})
		}
	}

	return out
}

// managedState reports whether an instance in this state is a managed node.
func managedState(state string) bool {
	return state != stateTerminated && state != stateShuttingDown
}

// targetFilterName maps a supported SSM Target Key to the equivalent EC2
// describe-filter name, reporting whether the key is one the emulator can
// resolve. Only the two Run Command keys the EC2 matcher actually understands
// are supported: the "InstanceIds" pseudo-key (selects by instance id) and
// "tag:<name>" (passes through unchanged). Other documented keys such as
// resource-groups:Name / resource-groups:ResourceTypeFilters, and the bare
// "tag-key" form, are reported unsupported so the caller can decline to forward
// them. The EC2 matcher would otherwise treat them as an unrestricted match.
func targetFilterName(key string) (string, bool) {
	switch {
	case key == "InstanceIds":
		return "instance-id", true
	case strings.HasPrefix(key, "tag:") && len(key) > len("tag:"):
		return key, true
	default:
		return "", false
	}
}

// checkTargets rejects instance ids the compute mock does not know, and
// instances that are not running: their agent is offline, which real SSM
// reports as not in a valid state.
func (m *Mock) checkTargets(ctx context.Context, instanceIDs []string) error {
	if m.instanceResolver == nil || len(instanceIDs) == 0 {
		return nil
	}

	// SSM Run Command is an internal/system caller: a managed (service-owned)
	// instance is a valid target even when the account hides managed resources
	// from the public Describe API. Opt in so hiding doesn't spuriously report
	// InvalidInstanceId for a real, running instance.
	found, err := m.instanceResolver.DescribeInstances(ctx, instanceIDs, nil,
		computedriver.DescribeInstancesOptions{IncludeManagedResources: true})
	if err != nil {
		return ssmErrf(excInvalidInstanceID, errors.NotFound, "%v", err)
	}

	state := make(map[string]string, len(found))
	for i := range found {
		state[found[i].ID] = found[i].State
	}

	for _, id := range instanceIDs {
		s, ok := state[id]

		switch {
		case !ok || !managedState(s):
			return ssmErrf(excInvalidInstanceID, errors.NotFound, "Instance %s is not a managed instance.", id)
		case s != stateRunning:
			return ssmErrf(excInvalidInstanceID, errors.FailedPrecondition,
				"Instances [[%s]] not in a valid state for account %s", id, m.opts.AccountID)
		}
	}

	return nil
}
