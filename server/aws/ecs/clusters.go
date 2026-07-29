package ecs

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire"
	"github.com/stackshy/cloudemu/v2/services/ecs/driver"
)

func (h *Handler) routeClusters(w http.ResponseWriter, r *http.Request, op string) bool {
	switch op {
	case "CreateCluster":
		h.createCluster(w, r)
	case "ListClusters":
		h.listClusters(w, r)
	case "DescribeClusters":
		h.describeClusters(w, r)
	case "DeleteCluster":
		h.deleteCluster(w, r)
	default:
		return false
	}

	return true
}

func (h *Handler) createCluster(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ClusterName string        `json:"clusterName"`
		Tags        []wireTag     `json:"tags"`
		Settings    []wireSetting `json:"settings"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	c, err := h.ecs.CreateCluster(r.Context(), driver.CreateClusterInput{
		Name:     req.ClusterName,
		Tags:     toTags(req.Tags),
		Settings: toSettings(req.Settings),
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"cluster": clusterToWire(c)})
}

func (h *Handler) listClusters(w http.ResponseWriter, r *http.Request) {
	clusters, err := h.ecs.ListClusters(r.Context())
	if err != nil {
		writeErr(w, err)

		return
	}

	arns := make([]string, 0, len(clusters))
	for i := range clusters {
		arns = append(arns, clusters[i].ARN)
	}

	wire.WriteJSON(w, map[string]any{"clusterArns": arns})
}

func (h *Handler) describeClusters(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Clusters []string `json:"clusters"`
		Include  []string `json:"include"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	clusters, failures, err := h.ecs.DescribeClusters(r.Context(), req.Clusters)
	if err != nil {
		writeErr(w, err)

		return
	}

	// Tags and settings are only returned when the caller opts in via include,
	// matching real ECS (ClusterField TAGS / SETTINGS).
	wantTags := includes(req.Include, "TAGS")
	wantSettings := includes(req.Include, "SETTINGS")

	out := make([]wireCluster, 0, len(clusters))

	for i := range clusters {
		wc := clusterToWire(&clusters[i])
		if !wantTags {
			wc.Tags = nil
		}

		if !wantSettings {
			wc.Settings = nil
		}

		out = append(out, wc)
	}

	wire.WriteJSON(w, map[string]any{"clusters": out, "failures": fromFailures(failures)})
}

// includes reports whether field (case-insensitive) is present in the request's
// include list.
func includes(include []string, field string) bool {
	for _, v := range include {
		if strings.EqualFold(v, field) {
			return true
		}
	}

	return false
}

func (h *Handler) deleteCluster(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cluster string `json:"cluster"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	c, err := h.ecs.DeleteCluster(r.Context(), req.Cluster)
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"cluster": clusterToWire(c)})
}
