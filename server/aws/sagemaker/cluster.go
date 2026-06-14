package sagemaker

import (
	"net/http"

	"github.com/stackshy/cloudemu/sagemaker/driver"
	"github.com/stackshy/cloudemu/server/wire"
)

// wireInstanceGroup is the JSON shape of a HyperPod instance group.
type wireInstanceGroup struct {
	InstanceGroupName string `json:"InstanceGroupName"`
	InstanceType      string `json:"InstanceType"`
	InstanceCount     int    `json:"InstanceCount"`
	ExecutionRole     string `json:"ExecutionRole"`
}

func toGroupSpecs(in []wireInstanceGroup) []driver.ClusterInstanceGroupSpec {
	out := make([]driver.ClusterInstanceGroupSpec, 0, len(in))
	for _, g := range in {
		out = append(out, driver.ClusterInstanceGroupSpec{
			GroupName:     g.InstanceGroupName,
			InstanceType:  g.InstanceType,
			InstanceCount: g.InstanceCount,
			ExecutionRole: g.ExecutionRole,
		})
	}

	return out
}

func (h *Handler) routeCluster(w http.ResponseWriter, r *http.Request, op string) bool {
	switch op {
	case "CreateCluster":
		h.createCluster(w, r)
	case "DescribeCluster":
		h.describeCluster(w, r)
	case "ListClusters":
		h.listClusters(w, r)
	case "UpdateCluster":
		h.updateCluster(w, r)
	case "DeleteCluster":
		h.deleteCluster(w, r)
	case "ListClusterNodes":
		h.listClusterNodes(w, r)
	case "DescribeClusterNode":
		h.describeClusterNode(w, r)
	default:
		return false
	}

	return true
}

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
	groups := make([]wireInstanceGroup, 0, len(c.InstanceGroups))
	for _, g := range c.InstanceGroups {
		groups = append(groups, wireInstanceGroup{
			InstanceGroupName: g.GroupName,
			InstanceType:      g.InstanceType,
			InstanceCount:     g.InstanceCount,
			ExecutionRole:     g.ExecutionRole,
		})
	}

	return map[string]any{
		"ClusterName":    c.ClusterName,
		"ClusterArn":     c.ClusterARN,
		"ClusterStatus":  c.Status,
		"InstanceGroups": groups,
		"CreationTime":   epoch(c.CreationTime),
	}
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
	clusters, err := h.svc.ListClusters(r.Context())
	if err != nil {
		writeDriverError(w, err)

		return
	}

	out := make([]map[string]any, 0, len(clusters))
	for i := range clusters {
		out = append(out, map[string]any{
			"ClusterName":   clusters[i].ClusterName,
			"ClusterArn":    clusters[i].ClusterARN,
			"ClusterStatus": clusters[i].Status,
			"CreationTime":  epoch(clusters[i].CreationTime),
		})
	}

	wire.WriteJSON(w, map[string]any{"ClusterSummaries": out})
}

func (h *Handler) updateCluster(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ClusterName    string              `json:"ClusterName"`
		InstanceGroups []wireInstanceGroup `json:"InstanceGroups"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	c, err := h.svc.UpdateCluster(r.Context(), req.ClusterName, toGroupSpecs(req.InstanceGroups))
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

	out := make([]map[string]any, 0, len(nodes))
	for i := range nodes {
		out = append(out, map[string]any{
			"InstanceGroupName": nodes[i].GroupName,
			"InstanceId":        nodes[i].NodeID,
			"InstanceType":      nodes[i].InstanceType,
			"InstanceStatus":    map[string]any{"Status": nodes[i].Status},
			"LaunchTime":        epoch(nodes[i].LaunchTime),
		})
	}

	wire.WriteJSON(w, map[string]any{"ClusterNodeSummaries": out})
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
