package batch

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/batch/driver"
)

// nodeSeparator joins a multi-node parallel job's ID and a node index: node i
// of job J is "J#i" (Multi-node parallel jobs, Batch User Guide).
const nodeSeparator = "#"

// Compute resource types that run on Fargate (ComputeResource.type), and the
// job definition platform capability for Fargate.
const (
	crTypeFargate     = "FARGATE"
	crTypeFargateSpot = "FARGATE_SPOT"
	platformFargate   = "FARGATE"
)

// nodeLayout is the node count, main node and node ranges of a multi-node
// parallel job, read from its nodeProperties (overrides already applied).
type nodeLayout struct {
	numNodes int32
	mainNode int32
	ranges   []jsonObject
}

// nodeLayoutWire decodes the part of nodeProperties a node layout needs. The
// int32 fields make an out-of-range numNodes or mainNode a decode error.
type nodeLayoutWire struct {
	NumNodes            int32        `json:"numNodes"`
	MainNode            int32        `json:"mainNode"`
	NodeRangeProperties []jsonObject `json:"nodeRangeProperties"`
}

// parseNodeLayout reads a job's node layout. ok is false for a job that isn't
// a multi-node parallel job.
func parseNodeLayout(props json.RawMessage) (nodeLayout, bool) {
	if !isSet(props) {
		return nodeLayout{}, false
	}

	var w nodeLayoutWire
	if json.Unmarshal(props, &w) != nil || w.NumNodes < 1 || w.MainNode < 0 || w.MainNode >= w.NumNodes {
		return nodeLayout{}, false
	}

	return nodeLayout{numNodes: w.NumNodes, mainNode: w.MainNode, ranges: w.NodeRangeProperties}, true
}

// rangeFor returns the node range whose targetNodes covers node i, or nil.
func (l *nodeLayout) rangeFor(i int32) jsonObject {
	for _, r := range l.ranges {
		var target string
		_ = json.Unmarshal(r["targetNodes"], &target)

		nr, ok := parseTargetNodes(target, int(l.numNodes))
		if ok && int(i) >= nr.start && int(i) <= nr.end {
			return r
		}
	}

	return nil
}

// splitNodeID splits "parent#index" into its parts.
func splitNodeID(id string) (parent string, idx int32, ok bool) {
	i := strings.LastIndex(id, nodeSeparator)
	if i <= 0 {
		return "", 0, false
	}

	n, err := strconv.ParseInt(id[i+1:], 10, 32)
	if err != nil || n < 0 {
		return "", 0, false
	}

	return id[:i], int32(n), true
}

// lookupNode resolves a node job ID "<jobId>#<nodeIndex>" to its multi-node
// job's record, the node index and the job's node layout.
func (m *Mock) lookupNode(id string) (rec jobRecord, idx int32, layout nodeLayout, ok bool) {
	parent, i, isNode := splitNodeID(id)
	if !isNode {
		return jobRecord{}, 0, nodeLayout{}, false
	}

	r, found := m.jobs.Get(parent)
	if !found {
		return jobRecord{}, 0, nodeLayout{}, false
	}

	l, isMNP := parseNodeLayout(r.Job.NodeProperties)
	if !isMNP || i >= l.numNodes {
		return jobRecord{}, 0, nodeLayout{}, false
	}

	return r, i, l, true
}

// renderNode builds node i of a multi-node parallel job. Nodes are not stored:
// each node follows the job's own lifecycle (status, reason, times and
// cancel/terminate flags), carries nodeDetails, and reports the container,
// ECS or EKS properties of the node range that covers it instead of the job's
// nodeProperties.
func (o *observer) renderNode(rec *jobRecord, l *nodeLayout, i int32) driver.Job {
	j := o.render(rec, -1)

	suffix := nodeSeparator + strconv.Itoa(int(i))
	j.JobID += suffix
	j.JobARN += suffix
	j.NodeProperties = nil
	j.NodeDetails = &driver.NodeDetails{NodeIndex: i, IsMainNode: i == l.mainNode, NumNodes: l.numNodes}

	r := l.rangeFor(i)
	j.Container = copyRaw(r["container"])
	j.EcsProperties = copyRaw(r["ecsProperties"])
	j.EksProperties = copyRaw(r["eksProperties"])

	if j.Status == driver.JobStatusSucceeded {
		j.Container = withExitCode(j.Container)
	}

	return j
}

// nodes lists the nodes of a multi-node parallel job by index, resuming after
// the cursor index. Every node shares the job's name, definition, creation
// time, share identifier and status, so a status or filter that rejects one
// node rejects them all: the first node decides and the loop stays bounded by
// the page size.
func (l *jobLister) nodes(m *Mock) *driver.ListJobsOutput {
	out := &driver.ListJobsOutput{Jobs: []driver.Job{}}

	rec, ok := m.jobs.Get(l.in.MultiNodeJobID)
	if !ok {
		return out
	}

	layout, isMNP := parseNodeLayout(rec.Job.NodeProperties)

	next := l.after + 1
	if !isMNP || next < 0 || next > math.MaxInt32 || next >= int64(layout.numNodes) {
		return out
	}

	start := int32(next)

	first := l.o.renderNode(&rec, &layout, start)
	if !l.keeps(&first) {
		return out
	}

	for i := start; i < layout.numNodes; i++ {
		if len(out.Jobs) == l.limit {
			out.NextToken = encodeListCursor(int64(i - 1))

			break
		}

		out.Jobs = append(out.Jobs, l.o.renderNode(&rec, &layout, i))
	}

	return out
}

// keeps reports whether a listed job passes the status and filters.
func (l *jobLister) keeps(j *driver.Job) bool {
	return (!l.useStatus || j.Status == l.status) && matchesFilters(j, l.in.Filters)
}

// fargateQueue reports whether a job queue runs its jobs on Fargate: its
// compute environments have FARGATE or FARGATE_SPOT compute resources. A queue
// can't mix Fargate and EC2 compute environments (Job queues on Fargate, Batch
// User Guide), so one Fargate compute environment decides.
func (m *Mock) fargateQueue(q *driver.JobQueue) bool {
	for _, o := range q.ComputeEnvironmentOrder {
		ce, ok := m.computeEnvs.Get(nameFromARN(kindComputeEnvironment, o.ComputeEnvironment))
		if !ok || !isSet(ce.ComputeResources) {
			continue
		}

		var cr struct {
			Type string `json:"type"`
		}

		if json.Unmarshal(ce.ComputeResources, &cr) == nil && (cr.Type == crTypeFargate || cr.Type == crTypeFargateSpot) {
			return true
		}
	}

	return false
}

// validateJobPlatform rejects a submission the queue's platform can't run. On
// a Fargate queue, nodeOverrides isn't applicable (SubmitJob API reference:
// "use containerOverrides instead") and multi-node parallel jobs aren't
// supported (RegisterJobDefinition API reference, type and nodeProperties).
func (m *Mock) validateJobPlatform(q *driver.JobQueue, jd *driver.JobDefinition, in *driver.SubmitJobInput) error {
	if !isSet(in.NodeOverrides) && !isMultiNode(jd) {
		return nil
	}

	if !m.fargateQueue(q) {
		return nil
	}

	if isSet(in.NodeOverrides) {
		return cerrors.Newf(cerrors.InvalidArgument,
			"nodeOverrides isn't applicable to jobs that run on Fargate resources (job queue %q uses Fargate "+
				"compute environments); use containerOverrides instead", q.Name)
	}

	return cerrors.Newf(cerrors.InvalidArgument,
		"multi-node parallel job definition %q can't run on Fargate resources (job queue %q uses Fargate "+
			"compute environments)", jd.ARN, q.Name)
}

func isMultiNode(jd *driver.JobDefinition) bool {
	return jd.Type == driver.JDTypeMultiNode || isSet(jd.NodeProperties)
}

// validateJobDefinitionPlatform enforces the RegisterJobDefinition rule that a
// job run on Fargate resources can't be multi-node: nodeProperties (and so
// type multinode) can't be combined with the FARGATE platform capability.
func validateJobDefinitionPlatform(in *driver.RegisterJobDefinitionInput) error {
	if !isSet(in.NodeProperties) && in.Type != driver.JDTypeMultiNode {
		return nil
	}

	for _, c := range in.PlatformCapabilities {
		if c == platformFargate {
			return cerrors.New(cerrors.InvalidArgument,
				"multinode job definitions and nodeProperties aren't supported for jobs that run on Fargate "+
					"resources (platformCapabilities FARGATE)")
		}
	}

	return nil
}
