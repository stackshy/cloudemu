package ecs

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
	"github.com/stackshy/cloudemu/v2/services/ecs/driver"
)

// unsupportedFeature answers an operation the backing driver does not provide
// with the ECS UnsupportedFeatureException.
func unsupportedFeature(w http.ResponseWriter, op string) {
	wire.WriteJSONError(w, http.StatusBadRequest, "UnsupportedFeatureException",
		"The operation "+op+" is not supported by this ECS backend.")
}

// capabilityOf returns the optional capability T of the backing driver, or
// answers the call with UnsupportedFeatureException when it has none.
func capabilityOf[T any](d driver.ECS, w http.ResponseWriter, op string) (T, bool) {
	c, ok := d.(T)
	if !ok {
		unsupportedFeature(w, op)
	}

	return c, ok
}

func (h *Handler) routeTaskProtection(w http.ResponseWriter, r *http.Request, op string) bool {
	switch op {
	case "GetTaskProtection":
		if tp, ok := capabilityOf[driver.TaskProtection](h.ecs, w, op); ok {
			getTaskProtection(w, r, tp)
		}
	case "UpdateTaskProtection":
		if tp, ok := capabilityOf[driver.TaskProtection](h.ecs, w, op); ok {
			updateTaskProtection(w, r, tp)
		}
	default:
		return false
	}

	return true
}

type wireProtectedTask struct {
	TaskArn           string  `json:"taskArn"`
	ProtectionEnabled bool    `json:"protectionEnabled"`
	ExpirationDate    float64 `json:"expirationDate,omitempty"`
}

func getTaskProtection(w http.ResponseWriter, r *http.Request, tp driver.TaskProtection) {
	var req struct {
		Cluster string   `json:"cluster"`
		Tasks   []string `json:"tasks"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	tasks, failures, err := tp.GetTaskProtection(r.Context(), req.Cluster, req.Tasks)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeProtectedTasks(w, tasks, failures)
}

func updateTaskProtection(w http.ResponseWriter, r *http.Request, tp driver.TaskProtection) {
	var req struct {
		Cluster           string   `json:"cluster"`
		Tasks             []string `json:"tasks"`
		ProtectionEnabled bool     `json:"protectionEnabled"`
		ExpiresInMinutes  *int     `json:"expiresInMinutes"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	tasks, failures, err := tp.UpdateTaskProtection(r.Context(), driver.UpdateTaskProtectionInput{
		Cluster: req.Cluster, Tasks: req.Tasks, ProtectionEnabled: req.ProtectionEnabled, ExpiresInMinutes: req.ExpiresInMinutes,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeProtectedTasks(w, tasks, failures)
}

func writeProtectedTasks(w http.ResponseWriter, tasks []driver.ProtectedTask, failures []driver.Failure) {
	out := make([]wireProtectedTask, 0, len(tasks))
	for i := range tasks {
		out = append(out, wireProtectedTask{
			TaskArn: tasks[i].TaskARN, ProtectionEnabled: tasks[i].ProtectionEnabled, ExpirationDate: epoch(tasks[i].ExpirationDate),
		})
	}

	wire.WriteJSON(w, map[string]any{"protectedTasks": out, "failures": fromFailures(failures)})
}
