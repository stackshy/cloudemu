package emr

import (
	"fmt"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// instanceGroup is a set of like-configured EC2 instances (MASTER/CORE/TASK)
// within a cluster. RunJobFlow and AddInstanceGroups create them;
// ModifyInstanceGroups resizes them.
type instanceGroup struct {
	id           string
	name         string
	groupType    string // MASTER / CORE / TASK
	instanceType string
	market       string
	bidPrice     string
	requested    int32
	running      int32
	state        string
	created      time.Time
	instances    []*instance
}

// instance is one EC2 node belonging to an instance group.
type instance struct {
	id            string
	ec2InstanceID string
	groupID       string
	instanceType  string
	market        string
	privateDNS    string
	privateIP     string
	state         string
	created       time.Time
}

// bootstrapAction is a script run on cluster nodes before Hadoop starts.
type bootstrapAction struct {
	name       string
	scriptPath string
	args       []string
}

// buildInstanceGroups populates a cluster's instance groups from a RunJobFlow
// Instances config: explicit InstanceGroups when given, otherwise a MASTER (+
// CORE) pair synthesized from the uniform master/slave instance types.
func (s *store) buildInstanceGroups(c *cluster, in *jobFlowInstancesConfig, now time.Time) {
	if in == nil {
		return
	}

	if len(in.InstanceGroups) > 0 {
		for _, cfg := range in.InstanceGroups {
			c.instanceGroups = append(c.instanceGroups, s.buildGroup(c, cfg, now))
		}

		return
	}

	if in.MasterInstanceType == nil {
		return
	}

	master := instanceGroupConfigInput{
		Name: strPtr("Master"), InstanceRole: "MASTER",
		InstanceType: in.MasterInstanceType, InstanceCount: int32Ptr(1),
	}
	c.instanceGroups = append(c.instanceGroups, s.buildGroup(c, master, now))

	coreCount := derefInt32(in.InstanceCount) - 1
	if coreCount <= 0 {
		return
	}

	slaveType := in.SlaveInstanceType
	if slaveType == nil {
		slaveType = in.MasterInstanceType
	}

	core := instanceGroupConfigInput{
		Name: strPtr("Core"), InstanceRole: "CORE",
		InstanceType: slaveType, InstanceCount: int32Ptr(coreCount),
	}
	c.instanceGroups = append(c.instanceGroups, s.buildGroup(c, core, now))
}

// buildGroup creates one running instance group (and its instances) and indexes
// it for ModifyInstanceGroups lookup.
func (s *store) buildGroup(c *cluster, cfg instanceGroupConfigInput, now time.Time) *instanceGroup {
	count := derefInt32(cfg.InstanceCount)
	g := &instanceGroup{
		id:           s.newID("ig-"),
		name:         deref(cfg.Name),
		groupType:    groupTypeOrDefault(cfg.InstanceRole),
		instanceType: deref(cfg.InstanceType),
		market:       marketOrDefault(cfg.Market),
		bidPrice:     deref(cfg.BidPrice),
		requested:    count,
		running:      count,
		state:        igStateRunning,
		created:      now,
	}

	s.fillInstances(c, g, now)
	s.groups[g.id] = g

	return g
}

// fillInstances grows or shrinks a group's instance list to match its running
// count, assigning deterministic ids and private addresses.
func (s *store) fillInstances(c *cluster, g *instanceGroup, now time.Time) {
	want := int(g.running)

	for len(g.instances) > want {
		g.instances = g.instances[:len(g.instances)-1]
	}

	for len(g.instances) < want {
		octet := countInstances(c) + 1
		g.instances = append(g.instances, &instance{
			id:            s.newID("ci-"),
			ec2InstanceID: fmt.Sprintf("i-%013X", s.nextID),
			groupID:       g.id,
			instanceType:  g.instanceType,
			market:        g.market,
			privateIP:     fmt.Sprintf("10.0.0.%d", octet%254+1),
			privateDNS:    fmt.Sprintf("ip-10-0-0-%d.ec2.internal", octet%254+1),
			state:         instanceStateRunning,
			created:       now,
		})
	}
}

// countInstances returns the total instance count across a cluster's groups.
func countInstances(c *cluster) int {
	total := 0
	for _, g := range c.instanceGroups {
		total += len(g.instances)
	}

	return total
}

// recordBootstrap stores a cluster's bootstrap actions from a RunJobFlow request.
func recordBootstrap(c *cluster, actions []bootstrapActionConfigInput) {
	for _, a := range actions {
		ba := bootstrapAction{name: deref(a.Name)}
		if a.ScriptBootstrapAction != nil {
			ba.scriptPath = deref(a.ScriptBootstrapAction.Path)
			ba.args = a.ScriptBootstrapAction.Args
		}

		c.bootstrapActions = append(c.bootstrapActions, ba)
	}
}

// addInstanceGroups appends instance groups to a live cluster (AddInstanceGroups).
func (s *store) addInstanceGroups(
	clusterID string, groups []instanceGroupConfigInput,
) (*cluster, []string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	c, ok := s.clusters[clusterID]
	if !ok {
		return nil, nil, notFound(clusterID)
	}

	if isTerminal(c.state) {
		return nil, nil, cerrors.Newf(cerrors.FailedPrecondition, "Cluster '%s' is terminated.", clusterID)
	}

	now := s.clock.Now().UTC()
	ids := make([]string, 0, len(groups))

	for _, cfg := range groups {
		g := s.buildGroup(c, cfg, now)
		c.instanceGroups = append(c.instanceGroups, g)
		ids = append(ids, g.id)
	}

	return c, ids, nil
}

// modifyInstanceGroups resizes instance groups by id (ModifyInstanceGroups).
func (s *store) modifyInstanceGroups(configs []instanceGroupModifyInput) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.clock.Now().UTC()

	for _, cfg := range configs {
		g, ok := s.groups[deref(cfg.InstanceGroupID)]
		if !ok {
			return cerrors.Newf(cerrors.InvalidArgument,
				"Instance group id '%s' is not valid.", deref(cfg.InstanceGroupID))
		}

		if cfg.InstanceCount == nil {
			continue
		}

		g.requested = *cfg.InstanceCount
		g.running = *cfg.InstanceCount

		if c := s.clusterOf(g.id); c != nil {
			s.fillInstances(c, g, now)
		}
	}

	return nil
}

// clusterOf returns the cluster that owns instance group groupID, or nil.
func (s *store) clusterOf(groupID string) *cluster {
	for _, c := range s.clusters {
		for _, g := range c.instanceGroups {
			if g.id == groupID {
				return c
			}
		}
	}

	return nil
}

// listInstanceGroups returns a cluster's instance groups (ListInstanceGroups).
func (s *store) listInstanceGroups(clusterID string) ([]instanceGroupWire, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	c, ok := s.clusters[clusterID]
	if !ok {
		return nil, notFound(clusterID)
	}

	out := make([]instanceGroupWire, 0, len(c.instanceGroups))
	for _, g := range c.instanceGroups {
		out = append(out, toInstanceGroupWire(g))
	}

	return out, nil
}

// listInstances returns a cluster's instances, filtered by group id/type and
// state (ListInstances).
func (s *store) listInstances(clusterID string, f instanceFilter) ([]instanceWire, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	c, ok := s.clusters[clusterID]
	if !ok {
		return nil, notFound(clusterID)
	}

	var out []instanceWire

	for _, g := range c.instanceGroups {
		if !f.matchesGroup(g) {
			continue
		}

		for _, inst := range g.instances {
			if f.matchesInstance(inst) {
				out = append(out, toInstanceWire(inst))
			}
		}
	}

	return out, nil
}

// listBootstrapActions returns a cluster's bootstrap actions (ListBootstrapActions).
func (s *store) listBootstrapActions(clusterID string) ([]commandWire, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	c, ok := s.clusters[clusterID]
	if !ok {
		return nil, notFound(clusterID)
	}

	out := make([]commandWire, 0, len(c.bootstrapActions))
	for _, ba := range c.bootstrapActions {
		out = append(out, commandWire{Name: ba.name, ScriptPath: ba.scriptPath, Args: ba.args})
	}

	return out, nil
}

// groupTypeOrDefault returns the instance role, defaulting empty to TASK.
func groupTypeOrDefault(role string) string {
	if role == "" {
		return "TASK"
	}

	return role
}

// marketOrDefault returns the market, defaulting empty to ON_DEMAND.
func marketOrDefault(market string) string {
	if market == "" {
		return marketOnDemand
	}

	return market
}

func strPtr(s string) *string { return &s }

func int32Ptr(v int32) *int32 { return &v }
