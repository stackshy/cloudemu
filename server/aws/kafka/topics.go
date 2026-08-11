package kafka

import (
	"encoding/json"
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/kafka/driver"
)

// topicStateActive is the state the emulator reports for every live topic.
const topicStateActive = "ACTIVE"

// routeTopics dispatches /v1/clusters/{arn}/topics and its sub-paths.
func (h *Handler) routeTopics(w http.ResponseWriter, r *http.Request, clusterARN string, rest []string) {
	if len(rest) == 0 {
		switch r.Method {
		case http.MethodPost:
			h.createTopic(w, r, clusterARN)
		case http.MethodGet:
			h.listTopics(w, r, clusterARN)
		default:
			methodNotAllowed(w)
		}

		return
	}

	topic := rest[0]

	if len(rest) == 2 && rest[1] == "partitions" {
		h.describeTopicPartitions(w, r, clusterARN, topic)

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.describeTopic(w, r, clusterARN, topic)
	case http.MethodPut:
		h.updateTopic(w, r, clusterARN, topic)
	case http.MethodDelete:
		h.deleteTopic(w, r, clusterARN, topic)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) createTopic(w http.ResponseWriter, r *http.Request, clusterARN string) {
	body, ok := readBody(w, r)
	if !ok {
		return
	}

	out, err := h.k.CreateTopic(r.Context(), clusterARN, body)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{
		"topicName": out.TopicName,
		"status":    topicStateActive,
	})
}

func (h *Handler) listTopics(w http.ResponseWriter, r *http.Request, clusterARN string) {
	list, next, err := h.k.ListTopics(r.Context(), clusterARN, pageFromQuery(r))
	if err != nil {
		writeErr(w, err)

		return
	}

	topics := make([]map[string]any, 0, len(list))
	for i := range list {
		topics = append(topics, topicInfoToWire(&list[i]))
	}

	writeJSON(w, withNext(map[string]any{"topics": topics}, next))
}

func (h *Handler) describeTopic(w http.ResponseWriter, r *http.Request, clusterARN, topic string) {
	out, err := h.k.DescribeTopic(r.Context(), clusterARN, topic)
	if err != nil {
		writeErr(w, err)

		return
	}

	resp := map[string]any{
		"topicName":         out.TopicName,
		"partitionCount":    out.NumberOfPartitions,
		"replicationFactor": out.ReplicationFactor,
		"status":            topicStateActive,
	}

	if cfg := topicConfigsString(out.RawOptions); cfg != "" {
		resp["configs"] = cfg
	}

	writeJSON(w, resp)
}

func (h *Handler) updateTopic(w http.ResponseWriter, r *http.Request, clusterARN, topic string) {
	body, ok := readBody(w, r)
	if !ok {
		return
	}

	out, err := h.k.UpdateTopic(r.Context(), clusterARN, topic, body)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{
		"topicName": out.TopicName,
		"status":    topicStateActive,
	})
}

func (h *Handler) deleteTopic(w http.ResponseWriter, r *http.Request, clusterARN, topic string) {
	if err := h.k.DeleteTopic(r.Context(), clusterARN, topic); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"topicName": topic, "status": topicStateActive})
}

func (h *Handler) describeTopicPartitions(w http.ResponseWriter, r *http.Request, clusterARN, topic string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)

		return
	}

	parts, next, err := h.k.DescribeTopicPartitions(r.Context(), clusterARN, topic, pageFromQuery(r))
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, withNext(map[string]any{"partitions": parts}, next))
}

// topicInfoToWire renders a topic as a TopicInfo (ListTopics item).
func topicInfoToWire(t *driver.Topic) map[string]any {
	return map[string]any{
		"topicName":         t.TopicName,
		"partitionCount":    t.NumberOfPartitions,
		"replicationFactor": t.ReplicationFactor,
	}
}

// topicConfigsString returns the stored topic configs blob as a string (the
// SDK's Configs field is a string), or "" when none was set.
func topicConfigsString(raw map[string]json.RawMessage) string {
	v, ok := raw["configs"]
	if !ok || len(v) == 0 {
		return ""
	}

	var s string
	if err := json.Unmarshal(v, &s); err == nil {
		return s
	}

	return string(v)
}
