package route53

import (
	"encoding/xml"
	"net"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
	dnsdriver "github.com/stackshy/cloudemu/v2/services/dns/driver"
)

// healthCheckPrefix roots the Route 53 health-check REST URLs:
//
//	POST   /2013-04-01/healthcheck        — CreateHealthCheck
//	GET    /2013-04-01/healthcheck        — ListHealthChecks
//	GET    /2013-04-01/healthcheck/{id}   — GetHealthCheck
//	POST   /2013-04-01/healthcheck/{id}   — UpdateHealthCheck
//	DELETE /2013-04-01/healthcheck/{id}   — DeleteHealthCheck
const healthCheckPrefix = "/2013-04-01/healthcheck"

// healthCheckVersion is the version stamped on every health check; the mock
// does not track config revisions.
const healthCheckVersion = 1

type healthCheckConfigXML struct {
	IPAddress                string `xml:"IPAddress,omitempty"`
	Port                     int    `xml:"Port,omitempty"`
	Type                     string `xml:"Type,omitempty"`
	ResourcePath             string `xml:"ResourcePath,omitempty"`
	FullyQualifiedDomainName string `xml:"FullyQualifiedDomainName,omitempty"`
	RequestInterval          int    `xml:"RequestInterval,omitempty"`
	FailureThreshold         int    `xml:"FailureThreshold,omitempty"`
}

type healthCheckXML struct {
	ID                 string               `xml:"Id"`
	CallerReference    string               `xml:"CallerReference"`
	HealthCheckConfig  healthCheckConfigXML `xml:"HealthCheckConfig"`
	HealthCheckVersion int                  `xml:"HealthCheckVersion"`
}

type createHealthCheckRequest struct {
	XMLName           xml.Name             `xml:"CreateHealthCheckRequest"`
	CallerReference   string               `xml:"CallerReference"`
	HealthCheckConfig healthCheckConfigXML `xml:"HealthCheckConfig"`
}

type createHealthCheckResponse struct {
	XMLName     xml.Name       `xml:"CreateHealthCheckResponse"`
	Xmlns       string         `xml:"xmlns,attr"`
	HealthCheck healthCheckXML `xml:"HealthCheck"`
}

type getHealthCheckResponse struct {
	XMLName     xml.Name       `xml:"GetHealthCheckResponse"`
	Xmlns       string         `xml:"xmlns,attr"`
	HealthCheck healthCheckXML `xml:"HealthCheck"`
}

type listHealthChecksResponse struct {
	XMLName      xml.Name         `xml:"ListHealthChecksResponse"`
	Xmlns        string           `xml:"xmlns,attr"`
	HealthChecks []healthCheckXML `xml:"HealthChecks>HealthCheck"`
	// Marker echoes the request marker; NextMarker is present only on a truncated
	// page and carries the health-check id the caller passes back as Marker.
	Marker      string `xml:"Marker,omitempty"`
	IsTruncated bool   `xml:"IsTruncated"`
	NextMarker  string `xml:"NextMarker,omitempty"`
	MaxItems    int    `xml:"MaxItems"`
}

type updateHealthCheckRequest struct {
	XMLName                  xml.Name `xml:"UpdateHealthCheckRequest"`
	IPAddress                string   `xml:"IPAddress"`
	Port                     int      `xml:"Port"`
	ResourcePath             string   `xml:"ResourcePath"`
	FullyQualifiedDomainName string   `xml:"FullyQualifiedDomainName"`
	FailureThreshold         int      `xml:"FailureThreshold"`
}

type updateHealthCheckResponse struct {
	XMLName     xml.Name       `xml:"UpdateHealthCheckResponse"`
	Xmlns       string         `xml:"xmlns,attr"`
	HealthCheck healthCheckXML `xml:"HealthCheck"`
}

type deleteHealthCheckResponse struct {
	XMLName xml.Name `xml:"DeleteHealthCheckResponse"`
	Xmlns   string   `xml:"xmlns,attr"`
}

// serveHealthCheck dispatches /2013-04-01/healthcheck[/{id}] requests.
func (h *Handler) serveHealthCheck(w http.ResponseWriter, r *http.Request) {
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, healthCheckPrefix), "/")
	if id == "" {
		h.serveHealthCheckCollection(w, r)
		return
	}

	h.serveHealthCheckResource(w, r, id)
}

// serveHealthCheckCollection dispatches /healthcheck collection requests.
func (h *Handler) serveHealthCheckCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.createHealthCheck(w, r)
	case http.MethodGet:
		h.listHealthChecks(w, r)
	default:
		writeMethodNotAllowed(w)
	}
}

// serveHealthCheckResource dispatches /healthcheck/{id} resource requests.
func (h *Handler) serveHealthCheckResource(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		h.getHealthCheck(w, r, id)
	case http.MethodPost:
		h.updateHealthCheck(w, r, id)
	case http.MethodDelete:
		h.deleteHealthCheck(w, r, id)
	default:
		writeMethodNotAllowed(w)
	}
}

func (h *Handler) createHealthCheck(w http.ResponseWriter, r *http.Request) {
	var req createHealthCheckRequest
	if !decodeXML(w, r, &req) {
		return
	}

	info, err := h.dns.CreateHealthCheck(r.Context(), toHealthCheckConfig(&req.HealthCheckConfig))
	if err != nil {
		writeHealthCheckErr(w, err)
		return
	}

	hc := toHealthCheckXML(info)
	hc.CallerReference = req.CallerReference

	w.Header().Set("Location", healthCheckPrefix+"/"+info.ID)
	wire.WriteXML(w, http.StatusCreated, createHealthCheckResponse{Xmlns: xmlns, HealthCheck: hc})
}

func (h *Handler) getHealthCheck(w http.ResponseWriter, r *http.Request, id string) {
	info, err := h.dns.GetHealthCheck(r.Context(), id)
	if err != nil {
		writeHealthCheckErr(w, err)
		return
	}

	wire.WriteXML(w, http.StatusOK, getHealthCheckResponse{Xmlns: xmlns, HealthCheck: toHealthCheckXML(info)})
}

// listHealthChecks answers ListHealthChecks. Health checks are returned in a
// stable id order and paginated Route 53 Marker-style: maxitems bounds the page
// and NextMarker (echoed back as the next marker) resumes at the following id.
func (h *Handler) listHealthChecks(w http.ResponseWriter, r *http.Request) {
	infos, err := h.dns.ListHealthChecks(r.Context())
	if err != nil {
		writeHealthCheckErr(w, err)
		return
	}

	checks := make([]healthCheckXML, 0, len(infos))
	for i := range infos {
		checks = append(checks, toHealthCheckXML(&infos[i]))
	}

	marker := r.URL.Query().Get("marker")
	maxItems := parseMaxItems(r.URL.Query().Get("maxitems"))
	page, next := markerPage(checks, marker, maxItems, func(c healthCheckXML) string { return c.ID })

	wire.WriteXML(w, http.StatusOK, listHealthChecksResponse{
		Xmlns:        xmlns,
		HealthChecks: page,
		Marker:       marker,
		IsTruncated:  next != "",
		NextMarker:   next,
		MaxItems:     maxItems,
	})
}

// updateHealthCheck merges the request's mutable fields onto the existing check
// — real Route 53's UpdateHealthCheck omits the immutable Type and
// RequestInterval, so those are preserved.
func (h *Handler) updateHealthCheck(w http.ResponseWriter, r *http.Request, id string) {
	var req updateHealthCheckRequest
	if !decodeXML(w, r, &req) {
		return
	}

	existing, err := h.dns.GetHealthCheck(r.Context(), id)
	if err != nil {
		writeHealthCheckErr(w, err)
		return
	}

	cfg := dnsdriver.HealthCheckConfig{
		Endpoint:         firstNonEmpty(req.IPAddress, req.FullyQualifiedDomainName, existing.Endpoint),
		Port:             valueOr(req.Port, existing.Port),
		Protocol:         existing.Protocol,
		Path:             firstNonEmpty(req.ResourcePath, existing.Path),
		IntervalSeconds:  existing.IntervalSeconds,
		FailureThreshold: valueOr(req.FailureThreshold, existing.FailureThreshold),
	}

	info, err := h.dns.UpdateHealthCheck(r.Context(), id, cfg)
	if err != nil {
		writeHealthCheckErr(w, err)
		return
	}

	wire.WriteXML(w, http.StatusOK, updateHealthCheckResponse{Xmlns: xmlns, HealthCheck: toHealthCheckXML(info)})
}

func (h *Handler) deleteHealthCheck(w http.ResponseWriter, r *http.Request, id string) {
	if err := h.dns.DeleteHealthCheck(r.Context(), id); err != nil {
		writeHealthCheckErr(w, err)
		return
	}

	wire.WriteXML(w, http.StatusOK, deleteHealthCheckResponse{Xmlns: xmlns})
}

// toHealthCheckConfig maps a parsed config element to the driver config. Route
// 53 carries the target as either IPAddress or FullyQualifiedDomainName; the
// driver keeps a single Endpoint.
func toHealthCheckConfig(x *healthCheckConfigXML) dnsdriver.HealthCheckConfig {
	return dnsdriver.HealthCheckConfig{
		Endpoint:         firstNonEmpty(x.IPAddress, x.FullyQualifiedDomainName),
		Port:             x.Port,
		Protocol:         x.Type,
		Path:             x.ResourcePath,
		IntervalSeconds:  x.RequestInterval,
		FailureThreshold: x.FailureThreshold,
	}
}

func toHealthCheckXML(info *dnsdriver.HealthCheckInfo) healthCheckXML {
	cfg := healthCheckConfigXML{
		Port:             info.Port,
		Type:             info.Protocol,
		ResourcePath:     info.Path,
		RequestInterval:  info.IntervalSeconds,
		FailureThreshold: info.FailureThreshold,
	}

	if net.ParseIP(info.Endpoint) != nil {
		cfg.IPAddress = info.Endpoint
	} else {
		cfg.FullyQualifiedDomainName = info.Endpoint
	}

	return healthCheckXML{ID: info.ID, HealthCheckConfig: cfg, HealthCheckVersion: healthCheckVersion}
}

func writeHealthCheckErr(w http.ResponseWriter, err error) {
	switch {
	case cerrors.IsNotFound(err):
		writeError(w, http.StatusNotFound, "NoSuchHealthCheck", err.Error())
	case cerrors.IsInvalidArgument(err):
		writeError(w, http.StatusBadRequest, "InvalidInput", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "InternalError", err.Error())
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}

	return ""
}

func valueOr(v, fallback int) int {
	if v != 0 {
		return v
	}

	return fallback
}
