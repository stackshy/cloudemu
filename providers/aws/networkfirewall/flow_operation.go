package networkfirewall

import (
	"context"
	"sort"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/settle"
	nfdriver "github.com/stackshy/cloudemu/v2/services/networkfirewall/driver"
)

const (
	flowCapture = "FLOW_CAPTURE"
	flowFlush   = "FLOW_FLUSH"

	flowCompleted  = "COMPLETED"
	flowInProgress = "IN_PROGRESS"
)

// StartFlowCapture records a flow capture request on a firewall endpoint.
//
//nolint:gocritic // req matches the driver signature.
func (m *Mock) StartFlowCapture(_ context.Context, req nfdriver.FlowOperationRequest) (*nfdriver.FlowOperation, error) {
	return m.startFlowOperation(flowCapture, &req)
}

// StartFlowFlush records a flow flush request on a firewall endpoint.
//
//nolint:gocritic // req matches the driver signature.
func (m *Mock) StartFlowFlush(_ context.Context, req nfdriver.FlowOperationRequest) (*nfdriver.FlowOperation, error) {
	return m.startFlowOperation(flowFlush, &req)
}

func (m *Mock) startFlowOperation(opType string, req *nfdriver.FlowOperationRequest) (*nfdriver.FlowOperation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	scope := nfdriver.FlowOperationScope{
		FirewallARN: req.FirewallARN, AvailabilityZone: req.AvailabilityZone,
		VPCEndpointID: req.VPCEndpointID, VPCEndpointAssociationARN: req.VPCEndpointAssociationARN,
	}
	if err := m.validateFlowScope(scope); err != nil {
		return nil, err
	}

	if req.MinimumFlowAgeSeconds != nil && *req.MinimumFlowAgeSeconds < 0 {
		return nil, invalid("MinimumFlowAgeInSeconds must not be negative")
	}

	op := &nfdriver.FlowOperation{
		ID: idgen.UUID(), FirewallARN: req.FirewallARN, Type: opType, Status: flowCompleted,
		AvailabilityZone: req.AvailabilityZone, VPCEndpointID: req.VPCEndpointID,
		VPCEndpointAssociationARN: req.VPCEndpointAssociationARN,
		Filters:                   cloneRawJSON(req.Filters), RequestedAt: m.now(),
	}

	if req.MinimumFlowAgeSeconds != nil {
		v := *req.MinimumFlowAgeSeconds
		op.MinimumFlowAgeSeconds = &v
	}

	m.flowOperations.Set(op.ID, op)

	return m.outFlowOperation(op), nil
}

// validateFlowScope checks the firewall exists and that the endpoint selector
// (an Availability Zone of the firewall, or a VPC endpoint association of it)
// is valid.
func (m *Mock) validateFlowScope(s nfdriver.FlowOperationScope) error {
	if err := validateARN("FirewallArn", s.FirewallARN); err != nil {
		return err
	}

	fw, ok := m.firewallByARN(s.FirewallARN)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "firewall %q not found", s.FirewallARN)
	}

	if s.VPCEndpointAssociationARN != "" {
		a, err := m.findAssociation(s.VPCEndpointAssociationARN)
		if err != nil {
			return err
		}

		if a.FirewallARN != s.FirewallARN {
			return invalid("VPC endpoint association %q does not belong to firewall %q", a.ARN, s.FirewallARN)
		}

		return nil
	}

	if s.AvailabilityZone == "" {
		return invalid("either AvailabilityZone or VpcEndpointAssociationArn is required")
	}

	if !oneOf(s.AvailabilityZone, nfdriver.SyncZones(fw, m.opts.Region)...) {
		return invalid("firewall %q has no endpoint in Availability Zone %q", fw.Name, s.AvailabilityZone)
	}

	return nil
}

// DescribeFlowOperation returns one flow operation.
func (m *Mock) DescribeFlowOperation(_ context.Context, scope nfdriver.FlowOperationScope, id string) (*nfdriver.FlowOperation, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	op, err := m.findFlowOperation(scope, id)
	if err != nil {
		return nil, err
	}

	return m.outFlowOperation(op), nil
}

// ListFlowOperations lists the flow operations of a firewall endpoint, optionally
// filtered by type, oldest first.
func (m *Mock) ListFlowOperations(
	_ context.Context, scope nfdriver.FlowOperationScope, opType string, opts nfdriver.ListOptions,
) ([]nfdriver.FlowOperation, string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if err := m.validateFlowScope(scope); err != nil {
		return nil, "", err
	}

	if opType != "" && !oneOf(opType, flowFlush, flowCapture) {
		return nil, "", invalid("FlowOperationType %q is not valid", opType)
	}

	var matched []*nfdriver.FlowOperation

	for _, op := range m.flowOperations.SortedValues() {
		if flowInScope(op, scope) && (opType == "" || op.Type == opType) {
			matched = append(matched, op)
		}
	}

	sort.SliceStable(matched, func(i, j int) bool { return matched[i].RequestedAt.Before(matched[j].RequestedAt) })

	items, next, err := paginate(matched, opts)
	if err != nil {
		return nil, "", err
	}

	out := make([]nfdriver.FlowOperation, 0, len(items))
	for _, op := range items {
		out = append(out, *m.outFlowOperation(op))
	}

	return out, next, nil
}

// ListFlowOperationResults returns the operation; its flows are always empty
// because the emulator has no data plane. The page arguments are validated so a
// caller sees the same errors as against AWS.
func (m *Mock) ListFlowOperationResults(
	_ context.Context, scope nfdriver.FlowOperationScope, id string, opts nfdriver.ListOptions,
) (*nfdriver.FlowOperation, string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	op, err := m.findFlowOperation(scope, id)
	if err != nil {
		return nil, "", err
	}

	if _, _, err := paginate([]struct{}{}, opts); err != nil {
		return nil, "", err
	}

	return m.outFlowOperation(op), "", nil
}

func (m *Mock) findFlowOperation(scope nfdriver.FlowOperationScope, id string) (*nfdriver.FlowOperation, error) {
	if err := m.validateFlowScope(scope); err != nil {
		return nil, err
	}

	if id == "" {
		return nil, invalid("FlowOperationId is required")
	}

	op, ok := m.flowOperations.Get(id)
	if !ok || !flowInScope(op, scope) {
		return nil, cerrors.Newf(cerrors.NotFound, "flow operation %q not found", id)
	}

	return op, nil
}

func flowInScope(op *nfdriver.FlowOperation, s nfdriver.FlowOperationScope) bool {
	if op.FirewallARN != s.FirewallARN {
		return false
	}

	if s.VPCEndpointAssociationARN != "" {
		return op.VPCEndpointAssociationARN == s.VPCEndpointAssociationARN
	}

	return op.VPCEndpointAssociationARN == "" && op.AvailabilityZone == s.AvailabilityZone
}

// outFlowOperation returns a copy whose status reflects --async-settle: the
// operation reports IN_PROGRESS until the settle window has elapsed.
func (m *Mock) outFlowOperation(op *nfdriver.FlowOperation) *nfdriver.FlowOperation {
	out := *op
	out.Filters = cloneRawJSON(op.Filters)

	if op.MinimumFlowAgeSeconds != nil {
		v := *op.MinimumFlowAgeSeconds
		out.MinimumFlowAgeSeconds = &v
	}

	window := settle.Pending(flowInProgress, op.RequestedAt, m.opts.SettleDuration(settle.DefaultFlowOperationSettle))
	out.Status = window.Observe(m.now(), flowCompleted)

	return &out
}
