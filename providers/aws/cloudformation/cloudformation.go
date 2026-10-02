// Package cloudformation is the AWS CloudFormation stack-store mock: it owns the
// stack lifecycle state and orchestrates provisioning by driving a registry of
// Provisioners (one per resource type) that call the existing AWS service
// drivers: S3, DynamoDB, SQS, SNS, Lambda, IAM, Secrets Manager, SSM. It holds
// no copy of those resources; a stack's resources live in their own service
// backends and are queryable through those services' own SDK surfaces.
package cloudformation

import (
	"context"
	"sort"
	"strings"
	"sync"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	cfn "github.com/stackshy/cloudemu/v2/services/cloudformation"
)

// Mock is the in-memory CloudFormation stack store and orchestrator.
type Mock struct {
	stacks    *memstore.Store[*stackData]
	registry  cfn.Registry
	clock     config.Clock
	accountID string
	region    string
	// fetchTemplate reads a TemplateURL object from the emulated S3.
	fetchTemplate TemplateFetcher
	// readParameter reads Parameter Store for SSM parameter types.
	readParameter ParameterReader
	// exportMu serializes the export checks of different stacks, so two
	// stacks cannot claim one export name and an export cannot lose its
	// last guard while a stack starts importing it.
	exportMu sync.Mutex
}

// stackData is the stored state of one stack, guarded by its own mutex.
type stackData struct {
	mu    sync.RWMutex
	stack cfn.Stack
	// provisionOrder is the order resources were created, so teardown and
	// rollback delete them in reverse.
	provisionOrder []string
	// resolved maps a logical ID to its Ref value + GetAtt attributes, for
	// resolving references during an update.
	resolved map[string]cfn.ResolvedResource
	// deleteIDs maps a logical ID to the identifier its provisioner's Delete
	// needs, when that differs from the physical id (SNS/Secrets delete by name
	// but expose an ARN as the physical id).
	deleteIDs map[string]string
	// props maps a logical ID to the resolved properties it was last created
	// or updated with. The update diff compares new properties against it.
	props map[string]map[string]any
	// rollbackFailed lists the resources a failed update rollback could not
	// restore. It is set only while the stack is UPDATE_ROLLBACK_FAILED.
	rollbackFailed []string
	// changeSets holds the stack's change sets in creation order.
	changeSets []*changeSetRecord
	// retained holds the old physical resources of replacements made by a
	// failed update that was not rolled back. The next successful update
	// deletes them in its cleanup phase, and DeleteStack deletes them.
	retained []retainedResource
	// imports lists the export names the stack imports with
	// Fn::ImportValue. An export in the list cannot be changed or deleted.
	imports []string
	// policies maps a logical ID to the DeletionPolicy and
	// UpdateReplacePolicy it was last applied with.
	policies map[string]resourcePolicy
}

// resourcePolicy is the effective DeletionPolicy and UpdateReplacePolicy of
// a provisioned resource.
type resourcePolicy struct {
	Deletion string `json:"deletion,omitempty"`
	Replace  string `json:"replace,omitempty"`
}

// policyOf returns the policies CloudFormation applies to rdef.
func policyOf(rdef *cfn.ResourceDef) resourcePolicy {
	return resourcePolicy{Deletion: rdef.EffectiveDeletionPolicy(), Replace: rdef.EffectiveReplacePolicy()}
}

// policy returns the policies a resource was applied with. A resource
// without a record, such as one restored from an older snapshot, has the
// defaults.
func (sd *stackData) policy(id string) resourcePolicy {
	sd.mu.RLock()
	defer sd.mu.RUnlock()

	if p, ok := sd.policies[id]; ok {
		return p
	}

	return resourcePolicy{Deletion: cfn.PolicyValueDelete, Replace: cfn.PolicyValueDelete}
}

func (sd *stackData) setPolicy(id string, p resourcePolicy) {
	sd.mu.Lock()
	defer sd.mu.Unlock()

	if sd.policies == nil {
		sd.policies = map[string]resourcePolicy{}
	}

	sd.policies[id] = p
}

// retainedResource is the old physical resource of a replacement that is
// waiting for cleanup.
type retainedResource struct {
	LogicalID string               `json:"logicalId"`
	Type      string               `json:"type"`
	Resolved  cfn.ResolvedResource `json:"resolved"`
	Props     map[string]any       `json:"props,omitempty"`
	DeleteID  string               `json:"deleteId"`
	// ReplacePolicy is the UpdateReplacePolicy the cleanup applies.
	ReplacePolicy string `json:"replacePolicy,omitempty"`
}

func (r *retainedResource) replacement() replacement {
	return replacement{id: r.LogicalID, policy: r.ReplacePolicy, old: liveResource{
		typ: r.Type, resolved: r.Resolved, props: r.Props, deleteID: r.DeleteID,
	}}
}

// retain records the old resources of replacements a failed update keeps.
func (sd *stackData) retain(replaced []replacement) {
	sd.mu.Lock()
	defer sd.mu.Unlock()

	for i := range replaced {
		old := &replaced[i].old
		sd.retained = append(sd.retained, retainedResource{
			LogicalID: replaced[i].id, Type: old.typ, Resolved: old.resolved, Props: old.props, DeleteID: old.deleteID,
			ReplacePolicy: replaced[i].policy,
		})
	}
}

// drainRetained forgets the retained old resources and returns the ones to
// delete. A retained resource whose physical id a live resource of the
// stack now holds again is dropped, not deleted.
func (sd *stackData) drainRetained() []replacement {
	sd.mu.Lock()
	defer sd.mu.Unlock()

	live := make(map[string]bool, len(sd.resolved))
	for _, rr := range sd.resolved {
		live[rr.RefValue] = true
	}

	var out []replacement

	for i := range sd.retained {
		if !live[sd.retained[i].Resolved.RefValue] {
			out = append(out, sd.retained[i].replacement())
		}
	}

	sd.retained = nil

	return out
}

// takeRetained removes and returns the retained old resource of id whose
// custom name property is name.
func (sd *stackData) takeRetained(id, rtype, nameProp, name string) (liveResource, bool) {
	if name == "" {
		return liveResource{}, false
	}

	sd.mu.Lock()
	defer sd.mu.Unlock()

	for i := range sd.retained {
		r := &sd.retained[i]
		if r.LogicalID == id && r.Type == rtype && cfn.PropString(r.Props, nameProp) == name {
			old := r.replacement().old

			sd.retained = append(sd.retained[:i], sd.retained[i+1:]...)

			return old, true
		}
	}

	return liveResource{}, false
}

// New builds a CloudFormation mock with an empty provisioner registry. Callers
// (the provider factory) wire the AWS registry with SetRegistry.
func New(opts *config.Options) *Mock {
	return &Mock{
		stacks:    memstore.New[*stackData](),
		registry:  cfn.Registry{},
		clock:     opts.Clock,
		accountID: opts.AccountID,
		region:    opts.Region,
	}
}

// SetRegistry installs the resource-type provisioner registry. The provider
// factory calls it with provisioners built from the live service drivers.
func (m *Mock) SetRegistry(r cfn.Registry) {
	m.registry = r
}

// activeStack returns the stored stackData for an active (not deleted) stack by
// name or stack ID, or a NotFound error. A DELETE_COMPLETE stack is treated as
// absent, the way DescribeStacks-by-name behaves in real CloudFormation.
func (m *Mock) activeStack(nameOrID string) (*stackData, error) {
	sd, _, ok := m.findStack(nameOrID)
	if !ok || sd.status() == cfn.StatusDeleteComplete {
		return nil, cerrors.Newf(cerrors.NotFound, "Stack with id %s does not exist", nameOrID)
	}

	return sd, nil
}

// findStack resolves a StackName that is either a stack name or a stack ID
// (the stack ARN). byID reports the ID form. An ID only matches the stack it
// was minted for, not a later stack that reused the name.
func (m *Mock) findStack(nameOrID string) (sd *stackData, byID, ok bool) {
	name := nameOrID

	if rest, isARN := strings.CutPrefix(nameOrID, "arn:"); isARN {
		_, after, found := strings.Cut(rest, ":stack/")
		if !found {
			return nil, true, false
		}

		name, _, _ = strings.Cut(after, "/")
		byID = true
	}

	sd, ok = m.stacks.Get(name)
	if ok && byID && sd.stackID() != nameOrID {
		return nil, true, false
	}

	return sd, byID, ok
}

func (sd *stackData) stackID() string {
	sd.mu.RLock()
	defer sd.mu.RUnlock()

	return sd.stack.ID
}

func (sd *stackData) status() string {
	sd.mu.RLock()
	defer sd.mu.RUnlock()

	return sd.stack.Status
}

// DescribeStacks returns the named stack, or every active stack when name is "".
// A stack ID also finds a deleted stack, as in real CloudFormation.
func (m *Mock) DescribeStacks(_ context.Context, name string) ([]cfn.Stack, error) {
	if name != "" {
		sd, byID, ok := m.findStack(name)
		if !ok || (!byID && sd.status() == cfn.StatusDeleteComplete) {
			return nil, cerrors.Newf(cerrors.NotFound, "Stack with id %s does not exist", name)
		}

		return []cfn.Stack{sd.snapshotStack()}, nil
	}

	var out []cfn.Stack

	for _, sd := range m.sortedStacks() {
		if sd.status() == cfn.StatusDeleteComplete {
			continue
		}

		out = append(out, sd.snapshotStack())
	}

	return out, nil
}

// DescribeStackEvents returns the named stack's events, newest first.
func (m *Mock) DescribeStackEvents(_ context.Context, name string) ([]cfn.StackEvent, error) {
	sd, err := m.stackAnyState(name)
	if err != nil {
		return nil, err
	}

	sd.mu.RLock()
	defer sd.mu.RUnlock()

	n := len(sd.stack.Events)
	out := make([]cfn.StackEvent, n)

	for i := range sd.stack.Events {
		out[n-1-i] = sd.stack.Events[i]
	}

	return out, nil
}

// ListStacks returns a summary of every stack, optionally filtered by status.
func (m *Mock) ListStacks(_ context.Context, statusFilter []string) ([]cfn.StackSummary, error) {
	want := map[string]bool{}
	for _, s := range statusFilter {
		want[s] = true
	}

	var out []cfn.StackSummary

	for _, sd := range m.sortedStacks() {
		sd.mu.RLock()
		s := sd.stack
		sd.mu.RUnlock()

		if len(want) > 0 && !want[s.Status] {
			continue
		}

		out = append(out, cfn.StackSummary{
			ID: s.ID, Name: s.Name, Status: s.Status, StatusReason: s.StatusReason,
			TemplateDescription: s.Description, CreationTime: s.CreationTime,
			LastUpdated: s.LastUpdated, DeletionTime: s.DeletionTime,
		})
	}

	return out, nil
}

// DescribeStackResources returns the resources of an active stack.
func (m *Mock) DescribeStackResources(_ context.Context, name string) ([]cfn.StackResource, error) {
	sd, err := m.activeStack(name)
	if err != nil {
		return nil, err
	}

	sd.mu.RLock()
	defer sd.mu.RUnlock()

	out := make([]cfn.StackResource, len(sd.stack.Resources))
	copy(out, sd.stack.Resources)

	return out, nil
}

// ListStackResources is DescribeStackResources' summary form; it returns the
// same resource set.
func (m *Mock) ListStackResources(ctx context.Context, name string) ([]cfn.StackResource, error) {
	return m.DescribeStackResources(ctx, name)
}

// GetTemplate returns the template body an active stack was deployed with.
func (m *Mock) GetTemplate(_ context.Context, name string) (string, error) {
	sd, err := m.stackAnyState(name)
	if err != nil {
		return "", err
	}

	sd.mu.RLock()
	defer sd.mu.RUnlock()

	return sd.stack.TemplateBody, nil
}

// stackAnyState resolves a stack by name regardless of status (used by reads
// that remain valid after deletion, like GetTemplate and DescribeStackEvents).
func (m *Mock) stackAnyState(name string) (*stackData, error) {
	sd, _, ok := m.findStack(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "Stack with id %s does not exist", name)
	}

	return sd, nil
}

func (m *Mock) sortedStacks() []*stackData {
	all := m.stacks.All()

	names := make([]string, 0, len(all))
	for n := range all {
		names = append(names, n)
	}

	sort.Strings(names)

	out := make([]*stackData, 0, len(names))
	for _, n := range names {
		out = append(out, all[n])
	}

	return out
}

// snapshotStack returns a deep-enough copy of the stack for a reader: the struct
// plus fresh copies of its slices, so a caller cannot mutate stored state.
func (sd *stackData) snapshotStack() cfn.Stack {
	sd.mu.RLock()
	defer sd.mu.RUnlock()

	s := sd.stack
	s.Parameters = maskParameters(sd.stack.Parameters)
	s.Outputs = append([]cfn.Output(nil), sd.stack.Outputs...)
	s.Resources = append([]cfn.StackResource(nil), sd.stack.Resources...)
	s.Capabilities = append([]string(nil), sd.stack.Capabilities...)
	s.NotificationARNs = append([]string(nil), sd.stack.NotificationARNs...)
	s.Events = append([]cfn.StackEvent(nil), sd.stack.Events...)

	if sd.stack.Tags != nil {
		s.Tags = make(map[string]string, len(sd.stack.Tags))
		for k, v := range sd.stack.Tags {
			s.Tags[k] = v
		}
	}

	return s
}

// newStackID mints the ARN-shaped stack id CloudFormation assigns.
func (m *Mock) newStackID(name string) string {
	return idgen.AWSARN("cloudformation", m.region, m.accountID, "stack/"+name+"/"+idgen.UUID())
}
