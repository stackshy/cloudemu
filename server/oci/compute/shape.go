package compute

import (
	"net/http"

	ocicompute "github.com/stackshy/cloudemu/v2/providers/oci/compute"
	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
)

// serveShapes lists the shapes a compartment may launch. OCI publishes no
// per-shape resource, so there is nothing under /shapes/{name}.
func (h *Handler) serveShapes(w http.ResponseWriter, r *http.Request, rt route) {
	if rt.ID != "" || rt.Sub != "" {
		ocirest.WriteError(w, r, http.StatusNotFound, codeNotFound,
			"shapes has no member resource; list it with GET /shapes")

		return
	}

	if r.Method != http.MethodGet {
		methodNotAllowed(w, r)
		return
	}

	if _, given := ocirest.RequireCompartmentID(w, r); !given {
		return
	}

	shapes, err := h.extras.ListShapes(r.Context(), r.URL.Query().Get("imageId"))
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	renderPage(w, r, shapes, toShapeResponse)
}

func toShapeResponse(s *ocicompute.Shape) shapeResponse {
	out := shapeResponse{
		Shape:                     s.Name,
		ProcessorDescription:      s.ProcessorDescription,
		Ocpus:                     s.OCPUs,
		MemoryInGBs:               s.MemoryInGBs,
		NetworkingBandwidthInGbps: s.NetworkingBandwidthInGbps,
		MaxVnicAttachments:        s.MaxVNICAttachments,
		IsFlexible:                s.IsFlexible,
	}

	if s.IsFlexible {
		out.OcpuOptions = &shapeRange{Min: s.MinOCPUs, Max: s.MaxOCPUs}
		out.MemoryOptions = &shapeMemRange{MinInGBs: s.MinMemoryInGBs, MaxInGBs: s.MaxMemoryInGBs}
	}

	return out
}
