// Package ecs implements the Amazon ECS (Elastic Container Service) AWS JSON
// 1.1 control-plane API as a server.Handler. Point the real
// aws-sdk-go-v2/service/ecs client at a Server registered with this handler and
// cluster, task-definition, task, service, and container-instance operations
// work end-to-end against an in-memory ECS driver.
//
// ECS uses the AWS JSON 1.1 wire shape (POST + JSON body, dispatched on the
// X-Amz-Target header with the prefix "AmazonEC2ContainerServiceV20141113.").
// Its Matches predicate is scoped to that prefix so it never shadows the
// catch-all S3 handler; registration order relative to other X-Amz-Target
// services is unconstrained.
package ecs

import (
	"net/http"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/server/wire"
	"github.com/stackshy/cloudemu/v2/services/ecs/driver"
)

const targetPrefix = "AmazonEC2ContainerServiceV20141113."

// Handler serves ECS control-plane requests against an ECS driver.
type Handler struct {
	ecs driver.ECS
}

// New returns an ECS handler backed by e.
func New(e driver.ECS) *Handler {
	return &Handler{ecs: e}
}

// Matches returns true for ECS-shaped requests, identified by an X-Amz-Target
// header of "AmazonEC2ContainerServiceV20141113.<Operation>".
func (*Handler) Matches(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)
}

// ServeHTTP dispatches ECS operations based on X-Amz-Target.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	op := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)

	routers := []func(http.ResponseWriter, *http.Request, string) bool{
		h.routeClusters, h.routeTaskDefs, h.routeTasks, h.routeServices, h.routeContainerInstances,
	}
	for _, route := range routers {
		if route(w, r, op) {
			return
		}
	}

	wire.WriteJSONError(w, http.StatusBadRequest, "ClientException", "unknown ECS operation: "+op)
}

// epoch converts a stored RFC3339 timestamp to Unix seconds for AWS JSON 1.1
// timestamp serialization (the ECS SDK decodes date fields as epoch seconds).
// Returns 0 on parse failure or empty input.
func epoch(iso string) float64 {
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return 0
	}

	return float64(t.Unix())
}
