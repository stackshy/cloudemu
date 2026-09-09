package cloudfront

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire"
	cfdriver "github.com/stackshy/cloudemu/v2/services/cloudfront/driver"
)

// listMaxItems is the default page size reported in a DistributionList/
// InvalidationList — the emulator never truncates.
const listMaxItems = 100

// ifMatch returns the request's If-Match header, trimmed of the weak-validator
// quoting some clients add.
func ifMatch(r *http.Request) string {
	v := strings.TrimSpace(r.Header.Get("If-Match"))

	return strings.Trim(v, `"`)
}

func (h *Handler) createDistribution(w http.ResponseWriter, r *http.Request) {
	var req distributionConfigRequest
	if !decodeXML(w, r, &req) {
		return
	}

	h.create(w, r, &req, nil)
}

func (h *Handler) createDistributionWithTags(w http.ResponseWriter, r *http.Request) {
	var req distributionConfigWithTagsRequest
	if !decodeXML(w, r, &req) {
		return
	}

	h.create(w, r, &req.DistributionConfig, req.Tags.toMap())
}

// create is the shared body of CreateDistribution and CreateDistributionWithTags.
func (h *Handler) create(w http.ResponseWriter, r *http.Request, cfg *distributionConfigRequest, tags map[string]string) {
	dist, err := h.cf.CreateDistribution(r.Context(), &cfdriver.CreateDistributionInput{
		CallerReference: cfg.CallerReference,
		Enabled:         cfg.Enabled,
		Comment:         cfg.Comment,
		ConfigXML:       cfg.Inner,
		Tags:            tags,
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	w.Header().Set("ETag", dist.ETag)
	w.Header().Set("Location", distPrefix+"/"+dist.ID)
	wire.WriteXML(w, http.StatusCreated, toDistributionXML(dist))
}

func (h *Handler) getDistribution(w http.ResponseWriter, r *http.Request, id string) {
	dist, err := h.cf.GetDistribution(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}

	w.Header().Set("ETag", dist.ETag)
	wire.WriteXML(w, http.StatusOK, toDistributionXML(dist))
}

func (h *Handler) getDistributionConfig(w http.ResponseWriter, r *http.Request, id string) {
	dist, err := h.cf.GetDistribution(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}

	w.Header().Set("ETag", dist.ETag)
	wire.WriteXML(w, http.StatusOK, distributionConfigResponse{Xmlns: xmlns, Inner: normalizeConfigXML(dist.ConfigXML)})
}

func (h *Handler) updateDistribution(w http.ResponseWriter, r *http.Request, id string) {
	var req distributionConfigRequest
	if !decodeXML(w, r, &req) {
		return
	}

	dist, err := h.cf.UpdateDistribution(r.Context(), &cfdriver.UpdateDistributionInput{
		ID:              id,
		IfMatch:         ifMatch(r),
		CallerReference: req.CallerReference,
		Enabled:         req.Enabled,
		Comment:         req.Comment,
		ConfigXML:       req.Inner,
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	w.Header().Set("ETag", dist.ETag)
	wire.WriteXML(w, http.StatusOK, toDistributionXML(dist))
}

func (h *Handler) deleteDistribution(w http.ResponseWriter, r *http.Request, id string) {
	if err := h.cf.DeleteDistribution(r.Context(), id, ifMatch(r)); err != nil {
		writeErr(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) listDistributions(w http.ResponseWriter, r *http.Request) {
	dists, err := h.cf.ListDistributions(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}

	resp := distributionListResponse{
		Xmlns:       xmlns,
		MaxItems:    listMaxItems,
		IsTruncated: false,
		Quantity:    len(dists),
		Items:       make([]distributionSummaryXML, 0, len(dists)),
	}

	for i := range dists {
		resp.Items = append(resp.Items, toSummaryXML(&dists[i]))
	}

	wire.WriteXML(w, http.StatusOK, resp)
}

// toDistributionXML builds the <Distribution> response for a stored distribution.
func toDistributionXML(dist *cfdriver.Distribution) distributionXML {
	return distributionXML{
		Xmlns:                         xmlns,
		ID:                            dist.ID,
		ARN:                           dist.ARN,
		Status:                        dist.Status,
		LastModifiedTime:              isoTime(dist.LastModifiedTime),
		InProgressInvalidationBatches: 0,
		DomainName:                    dist.DomainName,
		Config:                        xmlRaw{Inner: normalizeConfigXML(dist.ConfigXML)},
	}
}

// toSummaryXML builds a <DistributionSummary> for a stored distribution.
func toSummaryXML(dist *cfdriver.Distribution) distributionSummaryXML {
	return distributionSummaryXML{
		ID:               dist.ID,
		ARN:              dist.ARN,
		Status:           dist.Status,
		LastModifiedTime: isoTime(dist.LastModifiedTime),
		DomainName:       dist.DomainName,
		ConfigInner:      normalizeConfigXML(dist.ConfigXML),
	}
}
