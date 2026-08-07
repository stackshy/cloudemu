package vpclattice

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/vpclattice/driver"
)

type wireDomainVerification struct {
	Arn        string `json:"arn,omitempty"`
	ID         string `json:"id,omitempty"`
	DomainName string `json:"domainName,omitempty"`
	Status     string `json:"status,omitempty"`
	CreatedAt  string `json:"createdAt,omitempty"`
}

func domainVerificationToWire(d *driver.DomainVerification) wireDomainVerification {
	return wireDomainVerification{Arn: d.ARN, ID: d.ID, DomainName: d.DomainName, Status: d.Status, CreatedAt: d.CreatedAt}
}

func (h *Handler) serveDomainVerifications(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 {
		routeCollection(w, r, h.startDomainVerification, h.listDomainVerifications)

		return
	}

	routeByID(w, r, rest[0], h.getDomainVerification, nil, h.deleteDomainVerification)
}

func (h *Handler) startDomainVerification(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DomainName string            `json:"domainName"`
		Tags       map[string]string `json:"tags"`
	}

	if !decodeJSON(w, r, &req) {
		return
	}

	d, err := h.lattice.StartDomainVerification(r.Context(), req.DomainName, req.Tags)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, domainVerificationToWire(d))
}

func (h *Handler) getDomainVerification(w http.ResponseWriter, r *http.Request, id string) {
	d, err := h.lattice.GetDomainVerification(r.Context(), id)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, domainVerificationToWire(d))
}

func (h *Handler) deleteDomainVerification(w http.ResponseWriter, r *http.Request, id string) {
	if err := h.lattice.DeleteDomainVerification(r.Context(), id); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, struct{}{})
}

func (h *Handler) listDomainVerifications(w http.ResponseWriter, r *http.Request) {
	ds, err := h.lattice.ListDomainVerifications(r.Context())
	if err != nil {
		writeErr(w, err)

		return
	}

	items := make([]wireDomainVerification, 0, len(ds))
	for i := range ds {
		items = append(items, domainVerificationToWire(&ds[i]))
	}

	writeJSON(w, map[string]any{"items": items})
}
