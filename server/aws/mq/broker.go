package mq

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/mq/driver"
)

func (h *Handler) createBroker(w http.ResponseWriter, r *http.Request) {
	raw, ok := decodeBodyMap(w, r)
	if !ok {
		return
	}

	out, err := h.mq.CreateBroker(r.Context(), &driver.CreateBrokerInput{
		BrokerName:    rawString(raw, "brokerName"),
		EngineType:    rawString(raw, "engineType"),
		Config:        raw,
		Tags:          tagsFromBody(raw),
		Users:         usersFromBody(raw),
		Configuration: configRefFromBody(raw),
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"brokerArn": out.BrokerArn, "brokerId": out.BrokerID})
}

func (h *Handler) describeBroker(w http.ResponseWriter, r *http.Request, brokerID string) {
	out, err := h.mq.DescribeBroker(r.Context(), brokerID)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, brokerToWire(out))
}

func (h *Handler) updateBroker(w http.ResponseWriter, r *http.Request, brokerID string) {
	raw, ok := decodeBodyMap(w, r)
	if !ok {
		return
	}

	out, err := h.mq.UpdateBroker(r.Context(), &driver.UpdateBrokerInput{
		BrokerID:      brokerID,
		Config:        raw,
		Configuration: configRefFromBody(raw),
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, brokerToWire(out))
}

func (h *Handler) deleteBroker(w http.ResponseWriter, r *http.Request, brokerID string) {
	if err := h.mq.DeleteBroker(r.Context(), brokerID); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"brokerId": brokerID})
}

//nolint:dupl // parallel list-handler shape; distinct driver call and response key.
func (h *Handler) listBrokers(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	brokers, next, err := h.mq.ListBrokers(r.Context(), driver.Page{
		NextToken:  q.Get("nextToken"),
		MaxResults: atoiDefault(q.Get("maxResults")),
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	summaries := make([]map[string]any, 0, len(brokers))
	for _, b := range brokers {
		summaries = append(summaries, brokerSummaryToWire(b))
	}

	body := map[string]any{"brokerSummaries": summaries}
	if next != "" {
		body["nextToken"] = next
	}

	writeJSON(w, body)
}

func (h *Handler) rebootBroker(w http.ResponseWriter, r *http.Request, brokerID string) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)

		return
	}

	if err := h.mq.RebootBroker(r.Context(), brokerID); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{})
}
