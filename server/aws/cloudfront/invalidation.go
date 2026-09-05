package cloudfront

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
	cfdriver "github.com/stackshy/cloudemu/v2/services/cloudfront/driver"
)

func (h *Handler) serveInvalidationCollection(w http.ResponseWriter, r *http.Request, distID string) {
	switch r.Method {
	case http.MethodPost:
		h.createInvalidation(w, r, distID)
	case http.MethodGet:
		h.listInvalidations(w, r, distID)
	default:
		writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func (h *Handler) serveInvalidationItem(w http.ResponseWriter, r *http.Request, distID, invID string) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	inv, err := h.cf.GetInvalidation(r.Context(), distID, invID)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteXML(w, http.StatusOK, toInvalidationXML(inv))
}

func (h *Handler) createInvalidation(w http.ResponseWriter, r *http.Request, distID string) {
	var req invalidationRequest
	if !decodeXML(w, r, &req) {
		return
	}

	inv, err := h.cf.CreateInvalidation(r.Context(), distID, &cfdriver.CreateInvalidationInput{
		CallerReference: req.CallerReference,
		Paths:           req.Paths.Items,
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	w.Header().Set("Location", distPrefix+"/"+distID+"/"+invalidationSeg+"/"+inv.ID)
	wire.WriteXML(w, http.StatusCreated, toInvalidationXML(inv))
}

func (h *Handler) listInvalidations(w http.ResponseWriter, r *http.Request, distID string) {
	invs, err := h.cf.ListInvalidations(r.Context(), distID)
	if err != nil {
		writeErr(w, err)
		return
	}

	resp := invalidationListResponse{
		Xmlns:       xmlns,
		MaxItems:    listMaxItems,
		IsTruncated: false,
		Quantity:    len(invs),
		Items:       make([]invalidationSummaryXML, 0, len(invs)),
	}

	for i := range invs {
		resp.Items = append(resp.Items, invalidationSummaryXML{
			ID:         invs[i].ID,
			CreateTime: isoTime(invs[i].CreateTime),
			Status:     invs[i].Status,
		})
	}

	wire.WriteXML(w, http.StatusOK, resp)
}

// toInvalidationXML builds the <Invalidation> response for a stored invalidation.
func toInvalidationXML(inv *cfdriver.Invalidation) invalidationXML {
	return invalidationXML{
		Xmlns:      xmlns,
		ID:         inv.ID,
		Status:     inv.Status,
		CreateTime: isoTime(inv.CreateTime),
		InvalidationBatch: invalidationBatchXML{
			CallerReference: inv.CallerReference,
			Paths: pathsXML{
				Quantity: len(inv.Paths),
				Items:    inv.Paths,
			},
		},
	}
}
