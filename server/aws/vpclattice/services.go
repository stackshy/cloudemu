package vpclattice

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/vpclattice/driver"
)

type wireDNSEntry struct {
	DomainName   string `json:"domainName,omitempty"`
	HostedZoneID string `json:"hostedZoneId,omitempty"`
}

type wireService struct {
	Arn                string        `json:"arn,omitempty"`
	AuthType           string        `json:"authType,omitempty"`
	CertificateArn     string        `json:"certificateArn,omitempty"`
	CustomDomainName   string        `json:"customDomainName,omitempty"`
	DNSEntry           *wireDNSEntry `json:"dnsEntry,omitempty"`
	ID                 string        `json:"id,omitempty"`
	IdleTimeoutSeconds int32         `json:"idleTimeoutSeconds"`
	Name               string        `json:"name,omitempty"`
	Status             string        `json:"status,omitempty"`
	CreatedAt          string        `json:"createdAt,omitempty"`
	LastUpdatedAt      string        `json:"lastUpdatedAt,omitempty"`
}

func serviceToWire(s *driver.Service) wireService {
	w := wireService{
		Arn:                s.ARN,
		AuthType:           s.AuthType,
		CertificateArn:     s.CertificateARN,
		CustomDomainName:   s.CustomDomainName,
		ID:                 s.ID,
		IdleTimeoutSeconds: s.IdleTimeoutSeconds,
		Name:               s.Name,
		Status:             s.Status,
		CreatedAt:          s.CreatedAt,
		LastUpdatedAt:      s.LastUpdatedAt,
	}
	if s.DNSName != "" {
		w.DNSEntry = &wireDNSEntry{DomainName: s.DNSName, HostedZoneID: s.HostedZoneID}
	}

	return w
}

// serveServices routes /services[/{id}[/listeners...]].
func (h *Handler) serveServices(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 {
		routeCollection(w, r, h.createService, h.listServices)

		return
	}

	if len(rest) >= 2 && rest[1] == "listeners" {
		h.serveListeners(w, r, rest[0], rest[2:])

		return
	}

	routeByID(w, r, rest[0], h.getService, h.updateService, h.deleteService)
}

func (h *Handler) createService(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name               string            `json:"name"`
		AuthType           string            `json:"authType"`
		CertificateArn     string            `json:"certificateArn"`
		CustomDomainName   string            `json:"customDomainName"`
		IdleTimeoutSeconds int32             `json:"idleTimeoutSeconds"`
		Tags               map[string]string `json:"tags"`
	}

	if !decodeJSON(w, r, &req) {
		return
	}

	svc, err := h.lattice.CreateService(r.Context(), &driver.CreateServiceInput{
		Name: req.Name, AuthType: req.AuthType, CertificateARN: req.CertificateArn,
		CustomDomainName: req.CustomDomainName, IdleTimeoutSeconds: req.IdleTimeoutSeconds, Tags: req.Tags,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, serviceToWire(svc))
}

func (h *Handler) getService(w http.ResponseWriter, r *http.Request, id string) {
	svc, err := h.lattice.GetService(r.Context(), id)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, serviceToWire(svc))
}

func (h *Handler) updateService(w http.ResponseWriter, r *http.Request, id string) {
	var req struct {
		AuthType           string `json:"authType"`
		CertificateArn     string `json:"certificateArn"`
		IdleTimeoutSeconds int32  `json:"idleTimeoutSeconds"`
	}

	if !decodeJSON(w, r, &req) {
		return
	}

	svc, err := h.lattice.UpdateService(r.Context(), &driver.UpdateServiceInput{
		ID: id, AuthType: req.AuthType, CertificateARN: req.CertificateArn, IdleTimeoutSeconds: req.IdleTimeoutSeconds,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, serviceToWire(svc))
}

func (h *Handler) deleteService(w http.ResponseWriter, r *http.Request, id string) {
	if err := h.lattice.DeleteService(r.Context(), id); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, struct{}{})
}

func (h *Handler) listServices(w http.ResponseWriter, r *http.Request) {
	svcs, err := h.lattice.ListServices(r.Context())
	if err != nil {
		writeErr(w, err)

		return
	}

	items := make([]wireService, 0, len(svcs))
	for i := range svcs {
		items = append(items, serviceToWire(&svcs[i]))
	}

	writeJSON(w, map[string]any{"items": items})
}
