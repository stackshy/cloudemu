// Package cloudformation is the AWS CloudFormation stack-store mock: it owns the
// stack lifecycle state and orchestrates provisioning by driving a registry of
// Provisioners (one per resource type) that call the existing AWS service
// drivers — S3, DynamoDB, SQS, SNS, Lambda, IAM, Secrets Manager, SSM. It holds
// no copy of those resources; a stack's resources live in their own service
// backends and are queryable through those services' own SDK surfaces.
package cloudformation

import (
	"context"
	"sort"
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
// name, or a NotFound error. A DELETE_COMPLETE stack is treated as absent, the
// way DescribeStacks-by-name behaves in real CloudFormation.
func (m *Mock) activeStack(name string) (*stackData, error) {
	sd, ok := m.stacks.Get(name)
	if !ok || sd.status() == cfn.StatusDeleteComplete {
		return nil, cerrors.Newf(cerrors.NotFound, "Stack with id %s does not exist", name)
	}

	return sd, nil
}

func (sd *stackData) status() string {
	sd.mu.RLock()
	defer sd.mu.RUnlock()

	return sd.stack.Status
}

// DescribeStacks returns the named stack, or every active stack when name is "".
func (m *Mock) DescribeStacks(_ context.Context, name string) ([]cfn.Stack, error) {
	if name != "" {
		sd, err := m.activeStack(name)
		if err != nil {
			return nil, err
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
	sd, ok := m.stacks.Get(name)
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
	s.Parameters = append([]cfn.Parameter(nil), sd.stack.Parameters...)
	s.Outputs = append([]cfn.Output(nil), sd.stack.Outputs...)
	s.Resources = append([]cfn.StackResource(nil), sd.stack.Resources...)
	s.Capabilities = append([]string(nil), sd.stack.Capabilities...)
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
