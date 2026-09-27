package cloudformation

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/pagination"
	cfn "github.com/stackshy/cloudemu/v2/services/cloudformation"
)

// Error and status texts of the change set operations.
const (
	msgNoChanges = "The submitted information didn't contain changes. " +
		"Submit different information to create a change set."
	msgStackMissing          = "Stack [%s] does not exist"
	msgStackExists           = "Stack [%s] already exists and cannot be created again with the changeSet [%s]."
	msgChangeSetExists       = "ChangeSet [%s] already exists"
	msgChangeSetNotFound     = "ChangeSet [%s] does not exist"
	msgNeedStackName         = "StackName must be specified if ChangeSetName is not specified as an ARN."
	msgCannotExecuteStatus   = "ChangeSet [%s] cannot be executed in its current status of [%s]"
	msgCannotExecuteExec     = "ChangeSet [%s] cannot be executed in its current execution status of [%s]"
	msgCannotDelete          = "ChangeSet [%s] cannot be deleted in its current execution status of [%s]"
	msgOnFailureDeleteUpdate = "OnStackFailure DELETE is only valid when ChangeSetType is CREATE."
	msgBothFailureOptions    = "You cannot specify both OnStackFailure and DisableRollback."
	msgImportUnsupported     = "ChangeSetType IMPORT is not supported."
	msgInvalidNextToken      = "Invalid NextToken"
	msgFieldPattern          = "1 validation error detected: Value '%s' at '%s' failed to satisfy constraint: " +
		"Member must satisfy regular expression pattern: %s"
	msgFieldLength = "1 validation error detected: Value '%s' at '%s' failed to satisfy constraint: " +
		"Member must have length less than or equal to %d"
	msgFieldEnum = "1 validation error detected: Value '%s' at '%s' failed to satisfy constraint: " +
		"Member must satisfy enum value set: [%s]"
)

const (
	// changeSetNamePattern is the ChangeSetName constraint.
	changeSetNamePattern = "[a-zA-Z][-a-zA-Z0-9]*"
	changeSetNameMaxLen  = 128
	// changeSetsPageSize is the number of summaries a ListChangeSets page holds.
	changeSetsPageSize = 100
	// changesPageBytes is the size at which DescribeChangeSet pages its
	// changes.
	changesPageBytes = 1 << 20
)

// changeSetRecord is a stored change set. ChangeSet is what reads report.
// The other fields are the request values the execution applies. Nil tags,
// capabilities and topics keep the stack's own.
type changeSetRecord struct {
	ChangeSet        cfn.ChangeSet     `json:"changeSet"`
	Template         string            `json:"template"`
	Tags             map[string]string `json:"tags"`
	Capabilities     []string          `json:"capabilities"`
	NotificationARNs []string          `json:"notificationArns"`
	// ClientToken is the CreateChangeSet token, and ExecuteToken the
	// ExecuteChangeSet token of the execution that ran.
	ClientToken  string `json:"clientToken,omitempty"`
	ExecuteToken string `json:"executeToken,omitempty"`
}

// CreateChangeSet plans a change set. ChangeSetType CREATE plans a new stack
// and, when the name is free, records it as REVIEW_IN_PROGRESS. UPDATE, the
// default, plans an update of a live stack. The plan is worked out at once, so
// the change set is CREATE_COMPLETE and AVAILABLE, or FAILED when it would
// change nothing. A template or parameter error is returned and nothing is
// stored.
func (m *Mock) CreateChangeSet(ctx context.Context, in *cfn.CreateChangeSetInput) (*cfn.ChangeSet, error) {
	if err := validateChangeSetInput(in); err != nil {
		return nil, err
	}

	if prior := m.retriedChangeSet(in); prior != nil {
		return prior, nil
	}

	var (
		sd  *stackData
		rec *changeSetRecord
		err error
	)

	if in.ChangeSetType == cfn.ChangeSetTypeCreate {
		sd, rec, err = m.planCreateChangeSet(ctx, in)
	} else {
		sd, rec, err = m.planUpdateChangeSet(ctx, in)
	}

	if err != nil {
		return nil, err
	}

	out := rec.ChangeSet

	if aerr := sd.addChangeSet(rec); aerr != nil {
		return nil, aerr
	}

	return &out, nil
}

// retriedChangeSet returns the change set an earlier request with the same
// name and ClientToken made, or nil.
func (m *Mock) retriedChangeSet(in *cfn.CreateChangeSetInput) *cfn.ChangeSet {
	if in.ClientToken == "" {
		return nil
	}

	sd, _, ok := m.findStack(in.StackName)
	if !ok {
		return nil
	}

	rec := sd.changeSet(func(cs *cfn.ChangeSet) bool { return cs.Name == in.ChangeSetName })
	if rec == nil {
		return nil
	}

	sd.mu.RLock()
	defer sd.mu.RUnlock()

	if rec.ClientToken != in.ClientToken {
		return nil
	}

	out := rec.ChangeSet

	return &out
}

// validateChangeSetInput checks the request fields and defaults the type.
func validateChangeSetInput(in *cfn.CreateChangeSetInput) error {
	if in.StackName == "" {
		return cerrors.New(cerrors.InvalidArgument, "StackName is required")
	}

	if err := checkChangeSetName(in.ChangeSetName); err != nil {
		return err
	}

	if in.ChangeSetType == "" {
		in.ChangeSetType = cfn.ChangeSetTypeUpdate
	}

	types := []string{cfn.ChangeSetTypeCreate, cfn.ChangeSetTypeUpdate, cfn.ChangeSetTypeImport}
	if !slices.Contains(types, in.ChangeSetType) {
		return cerrors.Newf(cerrors.InvalidArgument, msgFieldEnum, in.ChangeSetType, "changeSetType", strings.Join(types, ", "))
	}

	if in.ChangeSetType == cfn.ChangeSetTypeImport {
		return cerrors.New(cerrors.InvalidArgument, msgImportUnsupported)
	}

	modes := []string{cfn.OnStackFailureDoNothing, cfn.OnStackFailureRollback, cfn.OnStackFailureDelete}
	if in.OnStackFailure != "" && !slices.Contains(modes, in.OnStackFailure) {
		return cerrors.Newf(cerrors.InvalidArgument, msgFieldEnum, in.OnStackFailure, "onStackFailure", strings.Join(modes, ", "))
	}

	if in.OnStackFailure == cfn.OnStackFailureDelete && in.ChangeSetType != cfn.ChangeSetTypeCreate {
		return cerrors.New(cerrors.InvalidArgument, msgOnFailureDeleteUpdate)
	}

	return nil
}

func checkChangeSetName(name string) error {
	if len(name) > changeSetNameMaxLen {
		return cerrors.Newf(cerrors.InvalidArgument, msgFieldLength, name, "changeSetName", changeSetNameMaxLen)
	}

	if !validChangeSetName(name) {
		return cerrors.Newf(cerrors.InvalidArgument, msgFieldPattern, name, "changeSetName", changeSetNamePattern)
	}

	return nil
}

// validChangeSetName reports whether name matches changeSetNamePattern.
func validChangeSetName(name string) bool {
	for i, r := range name {
		letter := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
		if !letter && (i == 0 || (r != '-' && (r < '0' || r > '9'))) {
			return false
		}
	}

	return name != ""
}

// planCreateChangeSet plans a CREATE change set. It joins a stack already in
// REVIEW_IN_PROGRESS, or makes one once the template checks out.
func (m *Mock) planCreateChangeSet(ctx context.Context, in *cfn.CreateChangeSetInput) (*stackData, *changeSetRecord, error) {
	sd, err := m.reviewStack(in)
	if err != nil {
		return nil, nil, err
	}

	if sd != nil {
		name, stackID := sd.identity()

		rec, perr := m.planNewStack(ctx, in, name, stackID)

		return sd, rec, perr
	}

	name, stackID := in.StackName, m.newStackID(in.StackName)

	rec, err := m.planNewStack(ctx, in, name, stackID)
	if err != nil {
		return nil, nil, err
	}

	sd = &stackData{
		resolved:  map[string]cfn.ResolvedResource{},
		deleteIDs: map[string]string{},
		props:     map[string]map[string]any{},
		stack: cfn.Stack{
			ID: stackID, Name: name, Status: cfn.StatusReviewInProgress, CreationTime: m.clock.Now(),
		},
	}

	if !m.claimStackSlot(name, sd) {
		return nil, nil, cerrors.Newf(cerrors.InvalidArgument, msgStackExists, name, in.ChangeSetName)
	}

	m.emitStackEvent(sd, cfn.StatusReviewInProgress, reasonUserInitiated)

	return sd, rec, nil
}

// reviewStack returns the REVIEW_IN_PROGRESS stack a CREATE change set
// joins, or nil when the name is free.
func (m *Mock) reviewStack(in *cfn.CreateChangeSetInput) (*stackData, error) {
	sd, byID, found := m.findStack(in.StackName)
	if found && sd.status() == cfn.StatusDeleteComplete {
		found = false
	}

	switch {
	case found && sd.status() != cfn.StatusReviewInProgress:
		return nil, cerrors.Newf(cerrors.InvalidArgument, msgStackExists, in.StackName, in.ChangeSetName)
	case !found && byID:
		return nil, cerrors.Newf(cerrors.InvalidArgument, msgStackMissing, in.StackName)
	case !found:
		return nil, nil
	}

	return sd, nil
}

// planNewStack checks a CREATE change set's template and parameters and
// lists every resource as an Add.
func (m *Mock) planNewStack(ctx context.Context, in *cfn.CreateChangeSetInput, name, stackID string) (*changeSetRecord, error) {
	body, err := m.templateBody(ctx, in.TemplateBody, in.TemplateURL)
	if err != nil {
		return nil, err
	}

	t, err := cfn.ParseTemplate(body)
	if err != nil {
		return nil, err
	}

	params, values, err := m.mergeParameters(ctx, t, in.Parameters)
	if err != nil {
		return nil, err
	}

	res := m.newResolver(name, stackID, values, in.NotificationARNs)

	effective, err := res.Prepare(t)
	if err != nil {
		return nil, err
	}

	if cerr := cfn.CheckCapabilities(t, in.Capabilities); cerr != nil {
		return nil, cerr
	}

	changes := cfn.PlanChanges(&cfn.ChangePlanInput{
		New: effective, NewProps: resolvableProps(effective, res), Registry: m.registry,
	})

	rec := m.newRecord(in, name, stackID, body, params)
	rec.ChangeSet.Changes = changes
	rec.ChangeSet.Tags = in.Tags
	rec.ChangeSet.NotificationARNs = in.NotificationARNs

	return rec, nil
}

// planUpdateChangeSet plans an UPDATE change set with the same planner
// UpdateStack uses.
func (m *Mock) planUpdateChangeSet(ctx context.Context, in *cfn.CreateChangeSetInput) (*stackData, *changeSetRecord, error) {
	sd, _, ok := m.findStack(in.StackName)
	if !ok || sd.status() == cfn.StatusDeleteComplete {
		return nil, nil, cerrors.Newf(cerrors.InvalidArgument, msgStackMissing, in.StackName)
	}

	if err := checkUpdatable(sd); err != nil {
		return nil, nil, err
	}

	uin := &cfn.UpdateStackInput{
		StackName: in.StackName, TemplateBody: in.TemplateBody, TemplateURL: in.TemplateURL,
		Parameters: in.Parameters, Tags: in.Tags, Capabilities: in.Capabilities,
		UsePreviousTemplate: in.UsePreviousTemplate, NotificationARNs: in.NotificationARNs,
	}

	plan, err := m.planUpdate(ctx, sd, uin)
	if err != nil {
		return nil, nil, err
	}

	name, id := sd.identity()
	rec := m.newRecord(in, name, id, plan.body, plan.params)
	rec.ChangeSet.NotificationARNs = plan.notificationARNs

	rec.ChangeSet.Tags = in.Tags
	if in.Tags == nil {
		rec.ChangeSet.Tags = sd.snapshotStack().Tags
	}

	if m.noChanges(sd, plan, uin) {
		rec.ChangeSet.Status = cfn.ChangeSetStatusFailed
		rec.ChangeSet.StatusReason = msgNoChanges
		rec.ChangeSet.ExecutionStatus = cfn.ExecutionUnavailable

		return sd, rec, nil
	}

	rec.ChangeSet.Changes = m.planChanges(sd, plan)

	return sd, rec, nil
}

// newRecord builds an AVAILABLE change set record from a request.
func (m *Mock) newRecord(in *cfn.CreateChangeSetInput, stackName, stackID, body string, params []cfn.Parameter) *changeSetRecord {
	return &changeSetRecord{
		ChangeSet: cfn.ChangeSet{
			ID:   idgen.AWSARN("cloudformation", m.region, m.accountID, "changeSet/"+in.ChangeSetName+"/"+idgen.UUID()),
			Name: in.ChangeSetName, StackID: stackID, StackName: stackName, Type: in.ChangeSetType,
			Description: in.Description, Status: cfn.ChangeSetStatusCreateComplete,
			ExecutionStatus: cfn.ExecutionAvailable, CreationTime: m.clock.Now(), Parameters: params,
			Capabilities: in.Capabilities, OnStackFailure: in.OnStackFailure,
		},
		Template: body, Tags: in.Tags, Capabilities: in.Capabilities, NotificationARNs: in.NotificationARNs,
		ClientToken: in.ClientToken,
	}
}

// planChanges lists what an update plan does to the stack's resources.
func (m *Mock) planChanges(sd *stackData, plan *updatePlan) []cfn.ResourceChange {
	sd.mu.RLock()
	live := make(map[string]cfn.LiveResource, len(sd.resolved))

	for id, rr := range sd.resolved {
		live[id] = cfn.LiveResource{PhysicalID: rr.RefValue, Props: sd.props[id]}
	}
	sd.mu.RUnlock()

	for id, lr := range live {
		lr.Type = sd.rowType(id)
		live[id] = lr
	}

	seedResolver(sd, plan.newT, plan.newRes)

	prior := paramValuesFrom(plan.prior.params)
	changed := map[string]bool{}

	for name, v := range paramValuesFrom(plan.params) {
		if old, ok := prior[name]; !ok || old != v {
			changed[name] = true
		}
	}

	return cfn.PlanChanges(&cfn.ChangePlanInput{
		Old: plan.oldT, New: plan.newT, Live: live, NewProps: resolvableProps(plan.newT, plan.newRes),
		ChangedParams: changed, Registry: m.registry,
	})
}

// resolvableProps resolves each resource's properties that can be resolved
// before execution.
func resolvableProps(t *cfn.Template, res *cfn.Resolver) map[string]map[string]any {
	out := make(map[string]map[string]any, len(t.Resources))

	for id, rdef := range t.Resources {
		if props, err := resolveProps(res, rdef.Properties); err == nil {
			out[id] = props
		}
	}

	return out
}

// addChangeSet stores a change set, refusing a duplicate name and a stack
// that no longer accepts one of its type.
func (sd *stackData) addChangeSet(rec *changeSetRecord) error {
	sd.mu.Lock()
	defer sd.mu.Unlock()

	cs := &rec.ChangeSet

	if slices.ContainsFunc(sd.changeSets, func(r *changeSetRecord) bool { return r.ChangeSet.Name == cs.Name }) {
		return cfn.NewException(cfn.ExceptionAlreadyExists, cerrors.Newf(cerrors.AlreadyExists, msgChangeSetExists, cs.Name))
	}

	review := sd.stack.Status == cfn.StatusReviewInProgress

	switch {
	case cs.Type == cfn.ChangeSetTypeCreate && !review:
		return cerrors.Newf(cerrors.InvalidArgument, msgStackExists, cs.StackName, cs.Name)
	case cs.Type != cfn.ChangeSetTypeCreate && !updatableStatus(sd.stack.Status):
		return cerrors.Newf(cerrors.InvalidArgument, msgStackCannotUpdate, sd.stack.ID, sd.stack.Status)
	}

	sd.changeSets = append(sd.changeSets, rec)

	return nil
}

// lookupChangeSet finds a change set by ARN, or by name within stackName.
func (m *Mock) lookupChangeSet(nameOrID, stackName string) (*stackData, *changeSetRecord, error) {
	notFound := cfn.NewException(cfn.ExceptionChangeSetNotFound,
		cerrors.Newf(cerrors.NotFound, msgChangeSetNotFound, nameOrID))

	if strings.HasPrefix(nameOrID, "arn:") {
		for _, sd := range m.sortedStacks() {
			if rec := sd.changeSet(func(cs *cfn.ChangeSet) bool { return cs.ID == nameOrID }); rec != nil {
				return sd, rec, nil
			}
		}

		return nil, nil, notFound
	}

	if stackName == "" {
		return nil, nil, cerrors.New(cerrors.InvalidArgument, msgNeedStackName)
	}

	sd, _, ok := m.findStack(stackName)
	if !ok {
		return nil, nil, notFound
	}

	rec := sd.changeSet(func(cs *cfn.ChangeSet) bool { return cs.Name == nameOrID })
	if rec == nil {
		return nil, nil, notFound
	}

	return sd, rec, nil
}

func (sd *stackData) changeSet(match func(*cfn.ChangeSet) bool) *changeSetRecord {
	sd.mu.RLock()
	defer sd.mu.RUnlock()

	for _, rec := range sd.changeSets {
		if match(&rec.ChangeSet) {
			return rec
		}
	}

	return nil
}

// view returns a copy of a change set for a reader, with NoEcho parameter
// values masked.
func (sd *stackData) view(rec *changeSetRecord) cfn.ChangeSet {
	sd.mu.RLock()
	defer sd.mu.RUnlock()

	cs := rec.ChangeSet
	cs.Parameters = maskParameters(cs.Parameters)
	cs.Tags = maps.Clone(cs.Tags)
	cs.Capabilities = slices.Clone(cs.Capabilities)
	cs.NotificationARNs = slices.Clone(cs.NotificationARNs)
	cs.Changes = slices.Clone(cs.Changes)

	return cs
}

// DescribeChangeSet returns a change set and one page of its changes.
// Property values are included only when IncludePropertyValues is set.
func (m *Mock) DescribeChangeSet(_ context.Context, in *cfn.DescribeChangeSetInput) (*cfn.ChangeSet, error) {
	sd, rec, err := m.lookupChangeSet(in.ChangeSetName, in.StackName)
	if err != nil {
		return nil, err
	}

	cs := sd.view(rec)

	if !in.IncludePropertyValues {
		for i := range cs.Changes {
			stripValues(&cs.Changes[i])
		}
	}

	cs.Changes, cs.NextToken, err = pageChanges(cs.Changes, in.NextToken)
	if err != nil {
		return nil, err
	}

	return &cs, nil
}

// stripValues drops a change's property values and contexts. The details
// are copied first, since the stored change shares them.
func stripValues(c *cfn.ResourceChange) {
	c.BeforeContext, c.AfterContext = "", ""
	c.Details = slices.Clone(c.Details)

	for i := range c.Details {
		t := &c.Details[i].Target
		t.BeforeValue, t.AfterValue, t.AttributeChangeType = "", "", ""
	}
}

// pageChanges returns the changes from token on that fit in one response.
// A page always holds at least one change.
func pageChanges(changes []cfn.ResourceChange, token string) ([]cfn.ResourceChange, string, error) {
	pt, err := pagination.DecodeToken(token)
	if err != nil || pt.Offset > len(changes) {
		return nil, "", cerrors.New(cerrors.InvalidArgument, msgInvalidNextToken)
	}

	end, size := pt.Offset, 0

	for end < len(changes) {
		b, _ := json.Marshal(changes[end])
		if end > pt.Offset && size+len(b) > changesPageBytes {
			break
		}

		size += len(b)
		end++
	}

	next := ""
	if end < len(changes) {
		next = pagination.EncodeToken(end)
	}

	return changes[pt.Offset:end], next, nil
}

// ListChangeSets returns one page of a stack's change sets in creation
// order.
func (m *Mock) ListChangeSets(_ context.Context, in *cfn.ListChangeSetsInput) (*cfn.ChangeSetList, error) {
	sd, err := m.activeStack(in.StackName)
	if err != nil {
		return nil, err
	}

	sd.mu.RLock()
	all := make([]cfn.ChangeSet, 0, len(sd.changeSets))

	for _, rec := range sd.changeSets {
		cs := rec.ChangeSet
		cs.Parameters, cs.Tags, cs.Capabilities, cs.NotificationARNs, cs.Changes = nil, nil, nil, nil, nil
		all = append(all, cs)
	}
	sd.mu.RUnlock()

	page, err := pagination.Paginate(all, in.NextToken, changeSetsPageSize)
	if err != nil {
		return nil, cerrors.New(cerrors.InvalidArgument, msgInvalidNextToken)
	}

	return &cfn.ChangeSetList{Summaries: page.Items, NextToken: page.NextPageToken}, nil
}

// ExecuteChangeSet runs an AVAILABLE change set. The stack's other change
// sets are deleted as it starts. OnStackFailure, or DisableRollback when the
// change set has none, decides what a failure does.
func (m *Mock) ExecuteChangeSet(ctx context.Context, in *cfn.ExecuteChangeSetInput) error {
	sd, rec, err := m.lookupChangeSet(in.ChangeSetName, in.StackName)
	if err != nil {
		return err
	}

	sd.mu.RLock()
	retried := in.ClientRequestToken != "" && rec.ExecuteToken == in.ClientRequestToken
	err = executable(rec)
	sd.mu.RUnlock()

	if retried {
		return nil
	}

	if err != nil {
		return err
	}

	onFailure, err := failureMode(rec.ChangeSet.OnStackFailure, in.DisableRollback)
	if err != nil {
		return err
	}

	run := execution{rec: rec, onFailure: onFailure, token: in.ClientRequestToken}

	if rec.ChangeSet.Type == cfn.ChangeSetTypeCreate {
		return m.executeCreate(ctx, sd, &run)
	}

	return m.executeUpdate(ctx, sd, &run)
}

// execution is one ExecuteChangeSet call.
type execution struct {
	rec       *changeSetRecord
	onFailure string
	token     string
}

// executable rejects a change set that cannot run. The caller holds the
// stack lock.
func executable(rec *changeSetRecord) error {
	cs := &rec.ChangeSet

	if cs.Status != cfn.ChangeSetStatusCreateComplete {
		return cfn.NewException(cfn.ExceptionInvalidChangeSetStatus,
			cerrors.Newf(cerrors.FailedPrecondition, msgCannotExecuteStatus, cs.ID, cs.Status))
	}

	if cs.ExecutionStatus != cfn.ExecutionAvailable {
		return cfn.NewException(cfn.ExceptionInvalidChangeSetStatus,
			cerrors.Newf(cerrors.FailedPrecondition, msgCannotExecuteExec, cs.ID, cs.ExecutionStatus))
	}

	return nil
}

// failureMode resolves what a failed execution does.
func failureMode(onStackFailure string, disableRollback *bool) (string, error) {
	switch {
	case onStackFailure != "" && disableRollback != nil:
		return "", cerrors.New(cerrors.InvalidArgument, msgBothFailureOptions)
	case onStackFailure != "":
		return onStackFailure, nil
	case disableRollback != nil && *disableRollback:
		return cfn.OnStackFailureDoNothing, nil
	default:
		return cfn.OnStackFailureRollback, nil
	}
}

// executeCreate creates the resources of a REVIEW_IN_PROGRESS stack.
func (m *Mock) executeCreate(ctx context.Context, sd *stackData, run *execution) error {
	rec := run.rec

	t, err := cfn.ParseTemplate(rec.Template)
	if err != nil {
		return err
	}

	name, id := sd.identity()
	res := m.newResolver(name, id, paramValuesFrom(rec.ChangeSet.Parameters), rec.NotificationARNs)

	effective, err := res.Prepare(t)
	if err != nil {
		return err
	}

	isReview := func(s string) bool { return s == cfn.StatusReviewInProgress }

	err = m.beginExecute(sd, run, isReview, cfn.StatusCreateInProgress, func(s *cfn.Stack) {
		s.Parameters = rec.ChangeSet.Parameters
		s.Description = t.Description
		s.Tags = rec.Tags
		s.Capabilities = rec.Capabilities
		s.TemplateBody = rec.Template
		s.NotificationARNs = rec.NotificationARNs
		s.LastUpdated = m.clock.Now()
	})
	if err != nil {
		return err
	}

	sd.finishExecute(rec, m.provision(ctx, sd, effective, res, run.onFailure))

	return nil
}

// executeUpdate applies an UPDATE change set through the UpdateStack path.
func (m *Mock) executeUpdate(ctx context.Context, sd *stackData, run *execution) error {
	rec := run.rec
	name, _ := sd.identity()
	in := &cfn.UpdateStackInput{
		StackName: name, TemplateBody: rec.Template, Parameters: rec.ChangeSet.Parameters,
		Tags: rec.Tags, Capabilities: rec.Capabilities, NotificationARNs: rec.NotificationARNs,
	}

	plan, err := m.planUpdate(ctx, sd, in)
	if err != nil {
		return err
	}

	if berr := m.beginExecute(sd, run, updatableStatus, cfn.StatusUpdateInProgress, nil); berr != nil {
		return berr
	}

	sd.finishExecute(rec, m.runUpdate(ctx, sd, in, plan, run.onFailure))

	return nil
}

// beginExecute starts a change set's execution in one locked step: it
// checks the change set and the stack status, deletes the stack's other
// change sets, applies setup to the stack and moves it to status.
func (m *Mock) beginExecute(
	sd *stackData, run *execution, allowed func(string) bool, status string, setup func(*cfn.Stack),
) error {
	sd.mu.Lock()
	defer sd.mu.Unlock()

	rec := run.rec

	if err := executable(rec); err != nil {
		return err
	}

	if !allowed(sd.stack.Status) {
		return cerrors.Newf(cerrors.InvalidArgument, msgStackCannotUpdate, sd.stack.ID, sd.stack.Status)
	}

	rec.ChangeSet.ExecutionStatus = cfn.ExecutionInProgress
	rec.ExecuteToken = run.token
	sd.changeSets = []*changeSetRecord{rec}

	if setup != nil {
		setup(&sd.stack)
	}

	sd.stack.ChangeSetID = rec.ChangeSet.ID
	sd.stack.DisableRollback = run.onFailure == cfn.OnStackFailureDoNothing
	sd.stack.Status = status
	sd.stack.StatusReason = reasonUserInitiated
	sd.stack.Events = append(sd.stack.Events,
		m.event(sd, sd.stack.Name, sd.stack.ID, stackResourceType, status, reasonUserInitiated))

	return nil
}

// finishExecute records how an execution ended.
func (sd *stackData) finishExecute(rec *changeSetRecord, ok bool) {
	sd.mu.Lock()
	defer sd.mu.Unlock()

	rec.ChangeSet.ExecutionStatus = cfn.ExecutionComplete
	if !ok {
		rec.ChangeSet.ExecutionStatus = cfn.ExecutionFailed
	}
}

// obsoleteChangeSets marks the change sets an update made outdated.
func (sd *stackData) obsoleteChangeSets() {
	sd.mu.Lock()
	defer sd.mu.Unlock()

	for _, rec := range sd.changeSets {
		if rec.ChangeSet.ExecutionStatus == cfn.ExecutionAvailable {
			rec.ChangeSet.ExecutionStatus = cfn.ExecutionObsolete
		}
	}
}

// DeleteChangeSet deletes a change set that is not executing. A stack in
// REVIEW_IN_PROGRESS stays when its last change set is deleted, as in AWS.
// Named within a stack that exists, a change set that is already gone is a
// successful no-op.
func (m *Mock) DeleteChangeSet(_ context.Context, in *cfn.DeleteChangeSetInput) error {
	sd, rec, err := m.lookupChangeSet(in.ChangeSetName, in.StackName)

	var named *cfn.ExceptionError
	if errors.As(err, &named) && named.Exception() == cfn.ExceptionChangeSetNotFound && in.StackName != "" &&
		!strings.HasPrefix(in.ChangeSetName, "arn:") {
		if _, serr := m.activeStack(in.StackName); serr != nil {
			return cerrors.Newf(cerrors.InvalidArgument, msgStackMissing, in.StackName)
		}

		return nil
	}

	if err != nil {
		return err
	}

	sd.mu.Lock()
	defer sd.mu.Unlock()

	if cs := &rec.ChangeSet; cs.ExecutionStatus == cfn.ExecutionInProgress {
		return cfn.NewException(cfn.ExceptionInvalidChangeSetStatus,
			cerrors.Newf(cerrors.FailedPrecondition, msgCannotDelete, cs.ID, cs.ExecutionStatus))
	}

	sd.changeSets = slices.DeleteFunc(sd.changeSets, func(r *changeSetRecord) bool { return r == rec })

	return nil
}
