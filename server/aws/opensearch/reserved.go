package opensearch

import "net/http"

func (h *Handler) purchaseReservedInstanceOffering(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)

		return
	}

	var req struct {
		ReservedInstanceOfferingID string `json:"ReservedInstanceOfferingId"`
		ReservationName            string `json:"ReservationName"`
		InstanceCount              int32  `json:"InstanceCount"`
	}

	if !decodeJSON(w, r, &req) {
		return
	}

	id, name, err := h.os.PurchaseReservedInstanceOffering(r.Context(), req.ReservedInstanceOfferingID, req.ReservationName, req.InstanceCount)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"ReservedInstanceId": id, "ReservationName": name})
}

func (h *Handler) describeReservedInstances(w http.ResponseWriter, r *http.Request) {
	page := pageFromQuery(r)

	list, next, err := h.os.DescribeReservedInstances(r.Context(), r.URL.Query().Get("reservationId"), page)
	if err != nil {
		writeErr(w, err)

		return
	}

	instances := make([]map[string]any, 0, len(list))
	for i := range list {
		instances = append(instances, reservedToWire(&list[i]))
	}

	writeJSON(w, withNext(map[string]any{"ReservedInstances": instances}, next))
}

func (h *Handler) describeReservedInstanceOfferings(w http.ResponseWriter, r *http.Request) {
	list, next, err := h.os.DescribeReservedInstanceOfferings(r.Context(), r.URL.Query().Get("offeringId"), pageFromQuery(r))
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, withNext(map[string]any{"ReservedInstanceOfferings": list}, next))
}
