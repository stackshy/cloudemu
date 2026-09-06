package batch

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/batch/driver"
)

func (h *Handler) registerJobDefinition(w http.ResponseWriter, r *http.Request) {
	var req registerJobDefinitionRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	jd, err := h.batch.RegisterJobDefinition(r.Context(), driver.RegisterJobDefinitionInput{
		Name:                 req.JobDefinitionName,
		Type:                 req.Type,
		ContainerProperties:  req.ContainerProperties,
		NodeProperties:       req.NodeProperties,
		EcsProperties:        req.EcsProperties,
		EksProperties:        req.EksProperties,
		RetryStrategy:        req.RetryStrategy,
		Timeout:              req.Timeout,
		Parameters:           req.Parameters,
		PlatformCapabilities: req.PlatformCapabilities,
		PropagateTags:        req.PropagateTags,
		SchedulingPriority:   req.SchedulingPriority,
		Tags:                 req.Tags,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, registerJobDefinitionResponse{
		JobDefinitionName: jd.Name,
		JobDefinitionArn:  jd.ARN,
		Revision:          jd.Revision,
	})
}

func (h *Handler) describeJobDefinitions(w http.ResponseWriter, r *http.Request) {
	var req describeJobDefinitionsRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	jds, err := h.batch.DescribeJobDefinitions(r.Context(), driver.DescribeJobDefinitionsInput{
		JobDefinitions: req.JobDefinitions,
		Name:           req.JobDefinitionName,
		Status:         req.Status,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	details := make([]jobDefinitionDetail, 0, len(jds))
	for i := range jds {
		details = append(details, toJDDetail(&jds[i]))
	}

	writeJSON(w, describeJobDefinitionsResponse{JobDefinitions: details})
}

func (h *Handler) deregisterJobDefinition(w http.ResponseWriter, r *http.Request) {
	var req deregisterJobDefinitionRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if err := h.batch.DeregisterJobDefinition(r.Context(), req.JobDefinition); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, emptyResponse{})
}
