package sesv2

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/sesv2/driver"
)

// serveIPPools routes /dedicated-ip-pools and its sub-paths.
func (h *Handler) serveIPPools(w http.ResponseWriter, r *http.Request, rest []string) {
	switch len(rest) {
	case 0:
		switch r.Method {
		case http.MethodPost:
			h.createIPPool(w, r)
		case http.MethodGet:
			h.listIPPools(w, r)
		default:
			methodNotAllowed(w)
		}
	case 1:
		switch r.Method {
		case http.MethodGet:
			h.getIPPool(w, r, rest[0])
		case http.MethodDelete:
			h.deleteIPPool(w, r, rest[0])
		default:
			methodNotAllowed(w)
		}
	case twoSegments:
		if rest[1] == "scaling" && r.Method == http.MethodPut {
			h.putIPPoolScaling(w, r, rest[0])

			return
		}

		notFound(w, r.URL.Path)
	default:
		notFound(w, r.URL.Path)
	}
}

func (h *Handler) createIPPool(w http.ResponseWriter, r *http.Request) {
	var req createIPPoolRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	writeOK(w, h.ses.CreateDedicatedIPPool(r.Context(), req.PoolName, req.ScalingMode, tagsToMap(req.Tags)))
}

func (h *Handler) getIPPool(w http.ResponseWriter, r *http.Request, name string) {
	p, err := h.ses.GetDedicatedIPPool(r.Context(), name)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, getIPPoolResponse{DedicatedIPPool: dedicatedIPPoolJSON{
		PoolName:    p.Name,
		ScalingMode: p.ScalingMode,
	}})
}

func (h *Handler) deleteIPPool(w http.ResponseWriter, r *http.Request, name string) {
	writeOK(w, h.ses.DeleteDedicatedIPPool(r.Context(), name))
}

func (h *Handler) listIPPools(w http.ResponseWriter, r *http.Request) {
	names, err := h.ses.ListDedicatedIPPools(r.Context())
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, listIPPoolsResponse{DedicatedIPPools: names})
}

func (h *Handler) putIPPoolScaling(w http.ResponseWriter, r *http.Request, name string) {
	var req putIPPoolScalingRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	writeOK(w, h.ses.PutDedicatedIPPoolScalingAttributes(r.Context(), name, req.ScalingMode))
}

// serveDedicatedIps routes /dedicated-ips and its sub-paths.
func (h *Handler) serveDedicatedIps(w http.ResponseWriter, r *http.Request, rest []string) {
	switch len(rest) {
	case 0:
		if r.Method != http.MethodGet {
			methodNotAllowed(w)

			return
		}

		h.getDedicatedIPs(w, r)
	case 1:
		if r.Method != http.MethodGet {
			methodNotAllowed(w)

			return
		}

		h.getDedicatedIP(w, r, rest[0])
	case twoSegments:
		h.serveDedicatedIPSub(w, r, rest[0], rest[1])
	default:
		notFound(w, r.URL.Path)
	}
}

func (h *Handler) serveDedicatedIPSub(w http.ResponseWriter, r *http.Request, ip, sub string) {
	if r.Method != http.MethodPut {
		methodNotAllowed(w)

		return
	}

	switch sub {
	case "pool":
		var req putIPInPoolRequest
		if !decodeJSON(w, r, &req) {
			return
		}

		writeOK(w, h.ses.PutDedicatedIPInPool(r.Context(), ip, req.DestinationPoolName))
	case "warmup":
		var req putIPWarmupRequest
		if !decodeJSON(w, r, &req) {
			return
		}

		writeOK(w, h.ses.PutDedicatedIPWarmupAttributes(r.Context(), ip, req.WarmupPercentage))
	default:
		notFound(w, r.URL.Path)
	}
}

func (h *Handler) getDedicatedIP(w http.ResponseWriter, r *http.Request, ip string) {
	d, err := h.ses.GetDedicatedIP(r.Context(), ip)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, getDedicatedIPResponse{DedicatedIP: dedicatedIPToJSON(d)})
}

func (h *Handler) getDedicatedIPs(w http.ResponseWriter, r *http.Request) {
	pool := r.URL.Query().Get("PoolName")

	ips, err := h.ses.GetDedicatedIPs(r.Context(), pool)
	if err != nil {
		writeErr(w, err)

		return
	}

	out := make([]dedicatedIPJSON, 0, len(ips))
	for i := range ips {
		out = append(out, dedicatedIPToJSON(&ips[i]))
	}

	writeJSON(w, getDedicatedIPsResponse{DedicatedIPs: out})
}

func dedicatedIPToJSON(d *driver.DedicatedIP) dedicatedIPJSON {
	return dedicatedIPJSON{
		IP:               d.IP,
		WarmupStatus:     d.WarmupStatus,
		WarmupPercentage: d.WarmupPct,
		PoolName:         d.PoolName,
	}
}
