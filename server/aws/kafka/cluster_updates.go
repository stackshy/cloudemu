package kafka

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/kafka/driver"
)

// clusterOpJSON writes a cluster-operation result: { clusterArn, clusterOperationArn }.
func (*Handler) clusterOpJSON(w http.ResponseWriter, op *driver.ClusterOperation, err error) {
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"clusterArn": op.ClusterARN, "clusterOperationArn": op.OperationARN})
}

// currentVersionBody reads the currentVersion field common to update requests.
type currentVersionBody struct {
	CurrentVersion string `json:"currentVersion"`
}

func (*Handler) readCurrentVersion(w http.ResponseWriter, r *http.Request) (body []byte, cv string, ok bool) {
	var cvb currentVersionBody

	b, ok := decodeBody(w, r, &cvb)
	if !ok {
		return nil, "", false
	}

	return b, cvb.CurrentVersion, true
}

func (h *Handler) updateBrokerCount(w http.ResponseWriter, r *http.Request, arn string) {
	var req struct {
		CurrentVersion            string `json:"currentVersion"`
		TargetNumberOfBrokerNodes int32  `json:"targetNumberOfBrokerNodes"`
	}

	if _, ok := decodeBody(w, r, &req); !ok {
		return
	}

	op, err := h.k.UpdateBrokerCount(r.Context(), arn, req.CurrentVersion, req.TargetNumberOfBrokerNodes)
	h.clusterOpJSON(w, op, err)
}

func (h *Handler) updateBrokerStorage(w http.ResponseWriter, r *http.Request, arn string) {
	body, cv, ok := h.readCurrentVersion(w, r)
	if !ok {
		return
	}

	op, err := h.k.UpdateBrokerStorage(r.Context(), arn, cv, body)
	h.clusterOpJSON(w, op, err)
}

func (h *Handler) updateBrokerType(w http.ResponseWriter, r *http.Request, arn string) {
	var req struct {
		CurrentVersion     string `json:"currentVersion"`
		TargetInstanceType string `json:"targetInstanceType"`
	}

	if _, ok := decodeBody(w, r, &req); !ok {
		return
	}

	op, err := h.k.UpdateBrokerType(r.Context(), arn, req.CurrentVersion, req.TargetInstanceType)
	h.clusterOpJSON(w, op, err)
}

func (h *Handler) updateStorage(w http.ResponseWriter, r *http.Request, arn string) {
	body, cv, ok := h.readCurrentVersion(w, r)
	if !ok {
		return
	}

	op, err := h.k.UpdateStorage(r.Context(), arn, cv, body)
	h.clusterOpJSON(w, op, err)
}

func (h *Handler) updateClusterConfiguration(w http.ResponseWriter, r *http.Request, arn string) {
	body, cv, ok := h.readCurrentVersion(w, r)
	if !ok {
		return
	}

	op, err := h.k.UpdateClusterConfiguration(r.Context(), arn, cv, body)
	h.clusterOpJSON(w, op, err)
}

func (h *Handler) updateClusterKafkaVersion(w http.ResponseWriter, r *http.Request, arn string) {
	body, cv, ok := h.readCurrentVersion(w, r)
	if !ok {
		return
	}

	op, err := h.k.UpdateClusterKafkaVersion(r.Context(), arn, cv, body)
	h.clusterOpJSON(w, op, err)
}

func (h *Handler) updateConnectivity(w http.ResponseWriter, r *http.Request, arn string) {
	body, cv, ok := h.readCurrentVersion(w, r)
	if !ok {
		return
	}

	op, err := h.k.UpdateConnectivity(r.Context(), arn, cv, body)
	h.clusterOpJSON(w, op, err)
}

func (h *Handler) updateMonitoring(w http.ResponseWriter, r *http.Request, arn string) {
	body, cv, ok := h.readCurrentVersion(w, r)
	if !ok {
		return
	}

	op, err := h.k.UpdateMonitoring(r.Context(), arn, cv, body)
	h.clusterOpJSON(w, op, err)
}

func (h *Handler) updateSecurity(w http.ResponseWriter, r *http.Request, arn string) {
	body, cv, ok := h.readCurrentVersion(w, r)
	if !ok {
		return
	}

	op, err := h.k.UpdateSecurity(r.Context(), arn, cv, body)
	h.clusterOpJSON(w, op, err)
}

func (h *Handler) updateRebalancing(w http.ResponseWriter, r *http.Request, arn string) {
	body, cv, ok := h.readCurrentVersion(w, r)
	if !ok {
		return
	}

	op, err := h.k.UpdateRebalancing(r.Context(), arn, cv, body)
	h.clusterOpJSON(w, op, err)
}

func (h *Handler) rebootBroker(w http.ResponseWriter, r *http.Request, arn string) {
	var req struct {
		BrokerIDs []string `json:"brokerIds"`
	}

	if _, ok := decodeBody(w, r, &req); !ok {
		return
	}

	op, err := h.k.RebootBroker(r.Context(), arn, req.BrokerIDs)
	h.clusterOpJSON(w, op, err)
}
