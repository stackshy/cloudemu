package sagemaker

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
	"github.com/stackshy/cloudemu/v2/services/sagemaker/driver"
)

// wireLifeCycleConfig is the JSON shape of ClusterLifeCycleConfig.
type wireLifeCycleConfig struct {
	SourceS3URI string `json:"SourceS3Uri"`
	OnCreate    string `json:"OnCreate"`
}

// wireInstanceGroup is the JSON request shape of a HyperPod instance group
// (ClusterInstanceGroupSpecification).
type wireInstanceGroup struct {
	InstanceGroupName string               `json:"InstanceGroupName"`
	InstanceType      string               `json:"InstanceType"`
	InstanceCount     int                  `json:"InstanceCount"`
	MinInstanceCount  int                  `json:"MinInstanceCount"`
	ThreadsPerCore    int                  `json:"ThreadsPerCore"`
	ExecutionRole     string               `json:"ExecutionRole"`
	LifeCycleConfig   *wireLifeCycleConfig `json:"LifeCycleConfig"`
}

func toGroupSpecs(in []wireInstanceGroup) []driver.ClusterInstanceGroupSpec {
	out := make([]driver.ClusterInstanceGroupSpec, 0, len(in))

	for _, g := range in {
		spec := driver.ClusterInstanceGroupSpec{
			GroupName:        g.InstanceGroupName,
			InstanceType:     g.InstanceType,
			InstanceCount:    g.InstanceCount,
			MinInstanceCount: g.MinInstanceCount,
			ThreadsPerCore:   g.ThreadsPerCore,
			ExecutionRole:    g.ExecutionRole,
		}

		if g.LifeCycleConfig != nil {
			spec.LifeCycleConfig = &driver.LifeCycleConfig{SourceS3URI: g.LifeCycleConfig.SourceS3URI, OnCreate: g.LifeCycleConfig.OnCreate}
		}

		out = append(out, spec)
	}

	return out
}

//nolint:dupl // SDK-compat decode/encode shim; the skeleton recurs but each op maps a distinct type.
func (h *Handler) createCluster(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ClusterName    string              `json:"ClusterName"`
		InstanceGroups []wireInstanceGroup `json:"InstanceGroups"`
		Tags           []wireTag           `json:"Tags"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	c, err := h.svc.CreateCluster(r.Context(), driver.ClusterSpec{
		ClusterName:    req.ClusterName,
		InstanceGroups: toGroupSpecs(req.InstanceGroups),
		Tags:           toTags(req.Tags),
	})
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"ClusterArn": c.ClusterARN})
}

func clusterToWire(c *driver.Cluster) map[string]any {
	groups := make([]map[string]any, 0, len(c.InstanceGroups))
	for i := range c.InstanceGroups {
		groups = append(groups, instanceGroupToWire(&c.InstanceGroups[i]))
	}

	resp := map[string]any{
		"ClusterName":    c.ClusterName,
		"ClusterArn":     c.ClusterARN,
		"ClusterStatus":  c.Status,
		"InstanceGroups": groups,
		"CreationTime":   epoch(c.CreationTime),
	}
	setIf(resp, "FailureMessage", c.FailureMessage)

	return resp
}

// instanceGroupToWire renders ClusterInstanceGroupDetails: current versus
// target count, threads per core, lifecycle config and status.
func instanceGroupToWire(g *driver.ClusterInstanceGroup) map[string]any {
	out := map[string]any{
		"InstanceGroupName": g.GroupName,
		"InstanceType":      g.InstanceType,
		"CurrentCount":      g.CurrentCount,
		"TargetCount":       g.TargetCount,
		"ThreadsPerCore":    g.ThreadsPerCore,
		"ExecutionRole":     g.ExecutionRole,
		"Status":            g.Status,
	}

	if g.MinCount > 0 {
		out["MinCount"] = g.MinCount
	}

	if g.LifeCycleConfig != nil {
		out["LifeCycleConfig"] = wireLifeCycleConfig{SourceS3URI: g.LifeCycleConfig.SourceS3URI, OnCreate: g.LifeCycleConfig.OnCreate}
	}

	return out
}

func (h *Handler) describeCluster(w http.ResponseWriter, r *http.Request) {
	name, ok := decodeName1(w, r, "ClusterName")
	if !ok {
		return
	}

	c, err := h.svc.DescribeCluster(r.Context(), name)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, clusterToWire(c))
}

func (h *Handler) listClusters(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeListReq(w, r)
	if !ok {
		return
	}

	clusters, err := h.svc.ListClusters(r.Context(), req.options())
	if err != nil {
		writeDriverError(w, err)

		return
	}

	writeSummaries(w, "ClusterSummaries", clusters, func(c *driver.Cluster) map[string]any {
		return map[string]any{
			"ClusterName":   c.ClusterName,
			"ClusterArn":    c.ClusterARN,
			"ClusterStatus": c.Status,
			"CreationTime":  epoch(c.CreationTime),
		}
	})
}

func (h *Handler) updateCluster(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ClusterName            string              `json:"ClusterName"`
		InstanceGroups         []wireInstanceGroup `json:"InstanceGroups"`
		InstanceGroupsToDelete []string            `json:"InstanceGroupsToDelete"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	c, err := h.svc.UpdateCluster(r.Context(), driver.ClusterUpdate{
		ClusterName:            req.ClusterName,
		InstanceGroups:         toGroupSpecs(req.InstanceGroups),
		InstanceGroupsToDelete: req.InstanceGroupsToDelete,
	})
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"ClusterArn": c.ClusterARN})
}

func (h *Handler) deleteCluster(w http.ResponseWriter, r *http.Request) {
	name, ok := decodeName1(w, r, "ClusterName")
	if !ok {
		return
	}

	if err := h.svc.DeleteCluster(r.Context(), name); err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"ClusterArn": ""})
}

func (h *Handler) listClusterNodes(w http.ResponseWriter, r *http.Request) {
	name, ok := decodeName1(w, r, "ClusterName")
	if !ok {
		return
	}

	nodes, err := h.svc.ListClusterNodes(r.Context(), name)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	writeSummaries(w, "ClusterNodeSummaries", driver.ListPage[driver.ClusterNode]{Items: nodes}, func(n *driver.ClusterNode) map[string]any {
		return map[string]any{
			"InstanceGroupName": n.GroupName,
			"InstanceId":        n.NodeID,
			"InstanceType":      n.InstanceType,
			"InstanceStatus":    map[string]any{"Status": n.Status},
			"LaunchTime":        epoch(n.LaunchTime),
		}
	})
}

func (h *Handler) describeClusterNode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ClusterName string `json:"ClusterName"`
		NodeID      string `json:"NodeId"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	node, err := h.svc.DescribeClusterNode(r.Context(), req.ClusterName, req.NodeID)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{
		"NodeDetails": map[string]any{
			"InstanceGroupName": node.GroupName,
			"InstanceId":        node.NodeID,
			"InstanceType":      node.InstanceType,
			"InstanceStatus":    map[string]any{"Status": node.Status},
			"LaunchTime":        epoch(node.LaunchTime),
		},
	})
}
