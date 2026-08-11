package kafka

import (
	"context"
	"encoding/json"

	"github.com/stackshy/cloudemu/v2/services/kafka/driver"
)

// VPC-connection states reported via VpcConnection.State.
const (
	vpcConnStateAvailable = "AVAILABLE"
	vpcConnStateRejected  = "REJECTED"
)

// snapshotVpcConnection returns a deep copy of a stored VPC connection so a
// reader cannot alias the Tags or RawOptions maps.
//
//nolint:gocritic // hugeParam: takes a value by design to snapshot stored state.
func snapshotVpcConnection(v driver.VpcConnection) driver.VpcConnection {
	out := v
	out.Tags = copyTags(v.Tags)
	out.RawOptions = copyRaw(v.RawOptions)

	return out
}

// createVpcConnectionRequest is the modeled CreateVpcConnection body. Unmodeled
// blocks (clientSubnets, securityGroups) round-trip via RawOptions.
type createVpcConnectionRequest struct {
	TargetClusterArn string            `json:"targetClusterArn"`
	Authentication   string            `json:"authentication"`
	VpcID            string            `json:"vpcId"`
	ClientSubnets    []string          `json:"clientSubnets"`
	SecurityGroups   []string          `json:"securityGroups"`
	Tags             map[string]string `json:"tags"`
}

// CreateVpcConnection mints a VPC connection in the AVAILABLE state, carrying
// the client subnets and security groups verbatim in raw options so a later
// Describe reflects them.
func (m *Mock) CreateVpcConnection(_ context.Context, body json.RawMessage) (*driver.VpcConnection, error) {
	var req createVpcConnectionRequest
	if len(body) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, badRequest("invalid CreateVpcConnection body: %v", err)
		}
	}

	if req.TargetClusterArn == "" {
		return nil, badRequest("targetClusterArn is required")
	}

	vpc := driver.VpcConnection{
		VpcConnectionARN: m.vpcConnectionARN(),
		TargetClusterARN: req.TargetClusterArn,
		State:            vpcConnStateAvailable,
		Authentication:   req.Authentication,
		VpcID:            req.VpcID,
		CreationTime:     m.now(),
		Tags:             copyTags(req.Tags),
		RawOptions:       vpcRawOptions(req),
	}

	// The ARN embeds a fresh UUID; SetIfAbsent keeps the create atomic and guards
	// the (impossible-but-cheap) collision.
	if !m.vpcConns.SetIfAbsent(vpc.VpcConnectionARN, &vpcConnData{vpc: vpc}) {
		return nil, badRequest("vpc connection already exists: %s", vpc.VpcConnectionARN)
	}

	out := snapshotVpcConnection(vpc)

	return &out, nil
}

// vpcRawOptions carries the unmodeled subnet/security-group lists into the
// connection's raw options so they survive a round-trip.
//
//nolint:gocritic // hugeParam: rendered from a decoded request copy.
func vpcRawOptions(req createVpcConnectionRequest) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}

	if req.ClientSubnets != nil {
		if b, err := json.Marshal(req.ClientSubnets); err == nil {
			out["subnets"] = b
		}
	}

	if req.SecurityGroups != nil {
		if b, err := json.Marshal(req.SecurityGroups); err == nil {
			out["securityGroups"] = b
		}
	}

	if len(out) == 0 {
		return nil
	}

	return out
}

// getVpcConnection resolves a VPC connection by ARN, NotFoundException if absent.
func (m *Mock) getVpcConnection(arn string) (*vpcConnData, error) {
	return m.getVpcConnectionErr(arn, notFound)
}

// getVpcConnectionErr resolves a VPC connection, building the missing-resource
// error with mkErr. RejectClientVpcConnection does not model NotFoundException,
// so it passes badRequest.
func (m *Mock) getVpcConnectionErr(arn string, mkErr func(string, ...any) error) (*vpcConnData, error) {
	vd, ok := m.vpcConns.Get(arn)
	if !ok {
		return nil, mkErr("vpc connection not found: %s", arn)
	}

	return vd, nil
}

// DescribeVpcConnection returns a deep copy of the stored VPC connection.
func (m *Mock) DescribeVpcConnection(_ context.Context, arn string) (*driver.VpcConnection, error) {
	vd, err := m.getVpcConnection(arn)
	if err != nil {
		return nil, err
	}

	vd.mu.RLock()
	defer vd.mu.RUnlock()

	out := snapshotVpcConnection(vd.vpc)

	return &out, nil
}

// DeleteVpcConnection removes a VPC connection and returns its last state.
func (m *Mock) DeleteVpcConnection(_ context.Context, arn string) (*driver.VpcConnection, error) {
	vd, err := m.getVpcConnection(arn)
	if err != nil {
		return nil, err
	}

	vd.mu.RLock()
	out := snapshotVpcConnection(vd.vpc)
	vd.mu.RUnlock()

	m.vpcConns.Delete(arn)

	return &out, nil
}

// ListVpcConnections lists every VPC connection sorted by ARN, paginated.
func (m *Mock) ListVpcConnections(
	_ context.Context, page driver.Page,
) (conns []driver.VpcConnection, next string, err error) {
	all := m.snapshotVpcConnections("")

	start, end, nextTok, err := m.paginate(len(all), page)
	if err != nil {
		return nil, "", err
	}

	return all[start:end], nextTok, nil
}

// ListClientVpcConnections lists the VPC connections whose TargetClusterARN is
// the given cluster (its client connections), paginated.
func (m *Mock) ListClientVpcConnections(
	_ context.Context, clusterARN string, page driver.Page,
) (conns []driver.VpcConnection, next string, err error) {
	if _, err = m.getClusterBR(clusterARN); err != nil {
		return nil, "", err
	}

	all := m.snapshotVpcConnections(clusterARN)

	start, end, nextTok, err := m.paginate(len(all), page)
	if err != nil {
		return nil, "", err
	}

	return all[start:end], nextTok, nil
}

// snapshotVpcConnections returns deep copies of all VPC connections (optionally
// only those targeting targetCluster) in deterministic ARN order.
func (m *Mock) snapshotVpcConnections(targetCluster string) []driver.VpcConnection {
	vals := m.vpcConns.SortedValues()

	all := make([]driver.VpcConnection, 0, len(vals))

	for _, vd := range vals {
		vd.mu.RLock()
		snap := snapshotVpcConnection(vd.vpc)
		vd.mu.RUnlock()

		if targetCluster != "" && snap.TargetClusterARN != targetCluster {
			continue
		}

		all = append(all, snap)
	}

	return all
}

// rejectClientVpcConnectionRequest is the RejectClientVpcConnection body.
type rejectClientVpcConnectionRequest struct {
	VpcConnectionArn string `json:"vpcConnectionArn"`
}

// RejectClientVpcConnection marks a client VPC connection REJECTED for a
// cluster. The connection must both exist and target the given cluster.
func (m *Mock) RejectClientVpcConnection(_ context.Context, clusterARN string, body json.RawMessage) error {
	if _, err := m.getClusterBR(clusterARN); err != nil {
		return err
	}

	var req rejectClientVpcConnectionRequest
	if len(body) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			return badRequest("invalid RejectClientVpcConnection body: %v", err)
		}
	}

	if req.VpcConnectionArn == "" {
		return badRequest("vpcConnectionArn is required")
	}

	vd, err := m.getVpcConnectionErr(req.VpcConnectionArn, badRequest)
	if err != nil {
		return err
	}

	vd.mu.Lock()
	defer vd.mu.Unlock()

	if vd.vpc.TargetClusterARN != clusterARN {
		return badRequest("vpc connection %s does not target cluster %s", req.VpcConnectionArn, clusterARN)
	}

	vd.vpc.State = vpcConnStateRejected

	return nil
}
