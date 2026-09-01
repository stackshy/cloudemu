package emr

import (
	"context"
	"net/http"
	"time"
)

// runJobFlow handles RunJobFlow: create a cluster (JobFlow) and return its id + ARN.
func (h *Handler) runJobFlow(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, _ context.Context, in *runJobFlowInput) (any, error) {
		c := h.store.runJobFlow(in)

		return runJobFlowOutput{JobFlowID: c.id, ClusterArn: c.arn}, nil
	})
}

// describeCluster handles DescribeCluster.
func (h *Handler) describeCluster(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, _ context.Context, in *describeClusterInput) (any, error) {
		c, err := h.store.describeCluster(deref(in.ClusterID))
		if err != nil {
			return nil, err
		}

		return describeClusterOutput{Cluster: c}, nil
	})
}

// listClusters handles ListClusters with ClusterStates/CreatedAfter/CreatedBefore filters.
func (h *Handler) listClusters(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, _ context.Context, in *listClustersInput) (any, error) {
		f := clusterFilter{
			states:        stateSet(in.ClusterStates),
			createdAfter:  epochToTime(in.CreatedAfter),
			createdBefore: epochToTime(in.CreatedBefore),
		}

		return listClustersOutput{Clusters: h.store.listClusters(f)}, nil
	})
}

// terminateJobFlows handles TerminateJobFlows.
func (h *Handler) terminateJobFlows(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, _ context.Context, in *terminateJobFlowsInput) (any, error) {
		h.store.terminate(in.JobFlowIDs)

		return struct{}{}, nil
	})
}

// addJobFlowSteps handles AddJobFlowSteps.
func (h *Handler) addJobFlowSteps(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, _ context.Context, in *addJobFlowStepsInput) (any, error) {
		ids, err := h.store.addSteps(deref(in.JobFlowID), in.Steps)
		if err != nil {
			return nil, err
		}

		return addJobFlowStepsOutput{StepIDs: ids}, nil
	})
}

// listSteps handles ListSteps with StepStates/StepIds filters.
func (h *Handler) listSteps(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, _ context.Context, in *listStepsInput) (any, error) {
		f := stepFilter{states: stateSet(in.StepStates), ids: stateSet(in.StepIDs)}

		steps, err := h.store.listSteps(deref(in.ClusterID), f)
		if err != nil {
			return nil, err
		}

		return listStepsOutput{Steps: steps}, nil
	})
}

// describeStep handles DescribeStep.
func (h *Handler) describeStep(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, _ context.Context, in *describeStepInput) (any, error) {
		st, err := h.store.describeStep(deref(in.ClusterID), deref(in.StepID))
		if err != nil {
			return nil, err
		}

		return describeStepOutput{Step: st}, nil
	})
}

// cancelSteps handles CancelSteps.
func (h *Handler) cancelSteps(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, _ context.Context, in *cancelStepsInput) (any, error) {
		info, err := h.store.cancelSteps(deref(in.ClusterID), in.StepIDs)
		if err != nil {
			return nil, err
		}

		return cancelStepsOutput{CancelStepsInfoList: info}, nil
	})
}

// addInstanceGroups handles AddInstanceGroups.
func (h *Handler) addInstanceGroups(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, _ context.Context, in *addInstanceGroupsInput) (any, error) {
		c, ids, err := h.store.addInstanceGroups(deref(in.JobFlowID), in.InstanceGroups)
		if err != nil {
			return nil, err
		}

		return addInstanceGroupsOutput{ClusterArn: c.arn, JobFlowID: c.id, InstanceGroupIDs: ids}, nil
	})
}

// modifyInstanceGroups handles ModifyInstanceGroups.
func (h *Handler) modifyInstanceGroups(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, _ context.Context, in *modifyInstanceGroupsInput) (any, error) {
		if err := h.store.modifyInstanceGroups(in.InstanceGroups); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

// listInstanceGroups handles ListInstanceGroups.
func (h *Handler) listInstanceGroups(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, _ context.Context, in *listInstanceGroupsInput) (any, error) {
		groups, err := h.store.listInstanceGroups(deref(in.ClusterID))
		if err != nil {
			return nil, err
		}

		return listInstanceGroupsOutput{InstanceGroups: groups}, nil
	})
}

// listInstances handles ListInstances with group id/type and state filters.
func (h *Handler) listInstances(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, _ context.Context, in *listInstancesInput) (any, error) {
		f := instanceFilter{
			groupID:    deref(in.InstanceGroupID),
			groupTypes: stateSet(in.InstanceGroupTypes),
			states:     stateSet(in.InstanceStates),
		}

		insts, err := h.store.listInstances(deref(in.ClusterID), f)
		if err != nil {
			return nil, err
		}

		return listInstancesOutput{Instances: insts}, nil
	})
}

// listBootstrapActions handles ListBootstrapActions.
func (h *Handler) listBootstrapActions(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, _ context.Context, in *listBootstrapActionsInput) (any, error) {
		actions, err := h.store.listBootstrapActions(deref(in.ClusterID))
		if err != nil {
			return nil, err
		}

		return listBootstrapActionsOutput{BootstrapActions: actions}, nil
	})
}

// stateSet turns a list of filter values into a lookup set, or nil when empty.
func stateSet(values []string) map[string]bool {
	if len(values) == 0 {
		return nil
	}

	set := make(map[string]bool, len(values))
	for _, v := range values {
		set[v] = true
	}

	return set
}

// epochToTime converts an epoch-seconds filter value to a time, or nil.
func epochToTime(secs *float64) *time.Time {
	if secs == nil {
		return nil
	}

	t := time.Unix(0, int64(*secs*float64(time.Second))).UTC()

	return &t
}
