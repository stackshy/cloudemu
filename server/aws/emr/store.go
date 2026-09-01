package emr

import (
	"fmt"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
)

// Cluster lifecycle states (a subset of the EMR ClusterState enum). RunJobFlow
// creates a cluster that is immediately WAITING — up, healthy and ready to run
// steps; TerminateJobFlows moves it to TERMINATED. The emulator runs steps
// instantly, so a live cluster is always WAITING (idle) rather than lingering
// in RUNNING.
const (
	stateWaiting     = "WAITING"
	stateTerminating = "TERMINATING"
	stateTerminated  = "TERMINATED"
)

// Step states. Submitted steps execute instantly and land in COMPLETED.
const (
	stepCompleted = "COMPLETED"
)

const (
	// idHexWidth is the width of the uppercase-hex suffix in a generated
	// cluster/step id (j-XXXXXXXXXXXXX), matching EMR's 13-character shape.
	idHexWidth = 13
	// reasonUserRequest is the StateChangeReason code EMR reports when a cluster
	// is terminated by TerminateJobFlows.
	reasonUserRequest = "USER_REQUEST"
)

// application is an installed EMR application (name/version, e.g. Spark 3.5.0).
type application struct {
	name    string
	version string
	args    []string
}

// keyValue is a Hadoop step property.
type keyValue struct {
	key   string
	value string
}

// tag is a user resource tag.
type tag struct {
	key   string
	value string
}

// step is a cluster step (a submitted job). Steps execute instantly, so their
// creation/start/end timeline collapses to the submission instant.
type step struct {
	id              string
	name            string
	jar             string
	mainClass       string
	args            []string
	properties      []keyValue
	actionOnFailure string
	state           string
	submitted       time.Time
}

// cluster is a JobFlow: the unit RunJobFlow creates and TerminateJobFlows tears
// down.
type cluster struct {
	id                   string
	name                 string
	arn                  string
	releaseLabel         string
	logURI               string
	serviceRole          string
	jobFlowRole          string
	ec2SubnetID          string
	ec2KeyName           string
	masterInstanceType   string
	instanceCount        int32
	keepAlive            bool
	terminationProtected bool
	visibleToAll         bool
	autoTerminate        bool
	state                string
	stateChangeCode      string
	stateChangeMessage   string
	creation             time.Time
	ready                time.Time
	end                  *time.Time
	applications         []application
	tags                 []tag
	steps                []*step
}

// store is the in-memory backing state for the EMR wire handler. EMR clusters
// and steps live only in the wire server (no portable driver consumes them), so
// the handler owns a self-contained thread-safe store rather than a
// three-layer provider driver.
type store struct {
	mu        sync.RWMutex
	clock     config.Clock
	accountID string
	region    string
	clusters  map[string]*cluster
	order     []string // cluster ids in creation order (ListClusters returns newest first)
	nextID    int64
}

// newStore returns an empty EMR store. A nil clock falls back to the real clock.
func newStore(accountID, region string, clock config.Clock) *store {
	if clock == nil {
		clock = config.RealClock{}
	}

	return &store{
		clock:     clock,
		accountID: accountID,
		region:    region,
		clusters:  map[string]*cluster{},
	}
}

// newID returns the next monotonic id with the given prefix (j- for clusters,
// s- for steps). The shared counter keeps every id unique across both kinds.
func (s *store) newID(prefix string) string {
	s.nextID++

	return fmt.Sprintf("%s%0*X", prefix, idHexWidth, s.nextID)
}

// isTerminal reports whether a cluster state is a final (post-termination) one.
func isTerminal(state string) bool {
	return state == stateTerminating || state == stateTerminated
}

// runJobFlow creates a cluster in WAITING and returns it. Steps carried in the
// request execute instantly.
func (s *store) runJobFlow(in *runJobFlowInput) *cluster {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.clock.Now().UTC()
	id := s.newID("j-")

	c := &cluster{
		id:                 id,
		name:               deref(in.Name),
		arn:                idgen.AWSARN("elasticmapreduce", s.region, s.accountID, "cluster/"+id),
		releaseLabel:       deref(in.ReleaseLabel),
		logURI:             deref(in.LogURI),
		serviceRole:        deref(in.ServiceRole),
		jobFlowRole:        deref(in.JobFlowRole),
		state:              stateWaiting,
		stateChangeMessage: "Cluster ready to run steps.",
		creation:           now,
		ready:              now,
		visibleToAll:       derefBool(in.VisibleToAllUsers, true),
	}

	applyInstances(c, in.Instances)
	c.autoTerminate = !c.keepAlive

	for _, a := range in.Applications {
		c.applications = append(c.applications,
			application{name: deref(a.Name), version: deref(a.Version), args: a.Args})
	}

	for _, t := range in.Tags {
		c.tags = append(c.tags, tag{key: deref(t.Key), value: deref(t.Value)})
	}

	for _, sc := range in.Steps {
		c.steps = append(c.steps, s.newStep(sc, now))
	}

	s.clusters[id] = c
	s.order = append(s.order, id)

	return c
}

// applyInstances copies the EC2/instance shape of a RunJobFlow request onto c.
func applyInstances(c *cluster, in *jobFlowInstancesConfig) {
	if in == nil {
		return
	}

	c.ec2SubnetID = deref(in.Ec2SubnetID)
	c.ec2KeyName = deref(in.Ec2KeyName)
	c.masterInstanceType = deref(in.MasterInstanceType)
	c.instanceCount = derefInt32(in.InstanceCount)
	c.keepAlive = derefBool(in.KeepJobFlowAliveWhenNoSteps, false)
	c.terminationProtected = derefBool(in.TerminationProtected, false)
}

// newStep builds a step in the COMPLETED state (instant execution).
func (s *store) newStep(sc stepConfig, now time.Time) *step {
	st := &step{
		id:              s.newID("s-"),
		name:            deref(sc.Name),
		actionOnFailure: sc.ActionOnFailure,
		state:           stepCompleted,
		submitted:       now,
	}

	if h := sc.HadoopJarStep; h != nil {
		st.jar = deref(h.Jar)
		st.mainClass = deref(h.MainClass)
		st.args = h.Args

		for _, p := range h.Properties {
			st.properties = append(st.properties, keyValue{key: deref(p.Key), value: deref(p.Value)})
		}
	}

	return st
}

// addSteps appends steps to a live cluster and returns their ids.
func (s *store) addSteps(clusterID string, steps []stepConfig) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	c, ok := s.clusters[clusterID]
	if !ok {
		return nil, notFound(clusterID)
	}

	if isTerminal(c.state) {
		return nil, cerrors.Newf(cerrors.FailedPrecondition, "Cluster '%s' is terminated.", clusterID)
	}

	now := s.clock.Now().UTC()
	ids := make([]string, 0, len(steps))

	for _, sc := range steps {
		st := s.newStep(sc, now)
		c.steps = append(c.steps, st)
		ids = append(ids, st.id)
	}

	return ids, nil
}

// terminate moves each named live cluster to TERMINATED. Unknown or
// already-terminated ids are ignored, matching TerminateJobFlows' idempotency.
func (s *store) terminate(ids []string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.clock.Now().UTC()

	for _, id := range ids {
		c, ok := s.clusters[id]
		if !ok || isTerminal(c.state) {
			continue
		}

		end := now
		c.state = stateTerminated
		c.stateChangeCode = reasonUserRequest
		c.stateChangeMessage = "Terminated by user request"
		c.end = &end
	}
}

// describeCluster returns the wire view of a cluster.
func (s *store) describeCluster(id string) (*clusterWire, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	c, ok := s.clusters[id]
	if !ok {
		return nil, notFound(id)
	}

	return toClusterWire(c), nil
}

// listClusters returns cluster summaries newest-first, filtered by state and
// creation window.
func (s *store) listClusters(f clusterFilter) []clusterSummaryWire {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]clusterSummaryWire, 0, len(s.order))

	for i := len(s.order) - 1; i >= 0; i-- {
		c := s.clusters[s.order[i]]
		if f.matches(c) {
			out = append(out, toClusterSummaryWire(c))
		}
	}

	return out
}

// listSteps returns step summaries for a cluster newest-first, filtered by state
// and id.
func (s *store) listSteps(clusterID string, f stepFilter) ([]stepSummaryWire, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	c, ok := s.clusters[clusterID]
	if !ok {
		return nil, notFound(clusterID)
	}

	out := make([]stepSummaryWire, 0, len(c.steps))

	for i := len(c.steps) - 1; i >= 0; i-- {
		st := c.steps[i]
		if f.matches(st) {
			out = append(out, toStepSummaryWire(st))
		}
	}

	return out, nil
}

// describeStep returns the wire view of a single step on a cluster.
func (s *store) describeStep(clusterID, stepID string) (*stepWire, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	c, ok := s.clusters[clusterID]
	if !ok {
		return nil, notFound(clusterID)
	}

	for _, st := range c.steps {
		if st.id == stepID {
			return toStepWire(st), nil
		}
	}

	return nil, cerrors.Newf(cerrors.InvalidArgument, "Step id '%s' is not valid.", stepID)
}

// notFound builds the InvalidRequestException EMR returns for an unknown cluster.
func notFound(clusterID string) error {
	return cerrors.Newf(cerrors.InvalidArgument, "Cluster id '%s' is not valid.", clusterID)
}
