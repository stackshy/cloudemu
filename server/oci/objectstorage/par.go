package objectstorage

import (
	"net/http"
	"time"

	osprovider "github.com/stackshy/cloudemu/v2/providers/oci/objectstorage"
	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
)

// servePARs routes /p and /p/{parId} under a bucket.
func (h *Handler) servePARs(w http.ResponseWriter, r *http.Request, rt *route) {
	if rt.Rest == "" {
		switch r.Method {
		case http.MethodPost:
			h.createPAR(w, r, rt.Bucket)
		case http.MethodGet:
			h.listPARs(w, r, rt.Bucket)
		default:
			methodNotAllowed(w, r)
		}

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getPAR(w, r, rt.Bucket, rt.Rest)
	case http.MethodDelete:
		h.deletePAR(w, r, rt.Bucket, rt.Rest)
	default:
		methodNotAllowed(w, r)
	}
}

func (h *Handler) createPAR(w http.ResponseWriter, r *http.Request, bucket string) {
	var req createPARBody

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	spec := osprovider.PARSpec{
		Name:                req.Name,
		ObjectName:          req.ObjectName,
		AccessType:          req.AccessType,
		BucketListingAction: req.BucketListingAction,
	}

	if req.TimeExpires != "" {
		expires, err := time.Parse(time.RFC3339, req.TimeExpires)
		if err != nil {
			ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter,
				"timeExpires must be an RFC3339 timestamp: "+err.Error())

			return
		}

		spec.TimeExpires = expires
	}

	par, err := h.extras.CreatePAR(r.Context(), bucket, spec)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	body := toPARBody(par)
	body.AccessURI = par.AccessURI
	body.FullPath = par.AccessURI

	ocirest.WriteJSON(w, r, http.StatusOK, body)
}

func (h *Handler) listPARs(w http.ResponseWriter, r *http.Request, bucket string) {
	pars, err := h.extras.ListPARs(r.Context(), bucket, r.URL.Query().Get("objectNamePrefix"))
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	out := make([]parBody, 0, len(pars))
	for i := range pars {
		out = append(out, toPARBody(&pars[i]))
	}

	ocirest.WriteJSON(w, r, http.StatusOK, out)
}

func (h *Handler) getPAR(w http.ResponseWriter, r *http.Request, bucket, parID string) {
	par, err := h.extras.GetPAR(r.Context(), bucket, parID)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, toPARBody(par))
}

func (h *Handler) deletePAR(w http.ResponseWriter, r *http.Request, bucket, parID string) {
	if err := h.extras.DeletePAR(r.Context(), bucket, parID); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusNoContent, nil)
}

// servePAR redeems a pre-authenticated request: /p/{token}/n/{ns}/b/{b}/o/{o}.
// The token stands in for authentication, so only the object read and write
// the request authorizes are served here.
func (h *Handler) servePAR(w http.ResponseWriter, r *http.Request, rt *route) {
	par, err := h.extras.ResolvePAR(r.Context(), rt.PARToken)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	if rt.Sub != subObjects || rt.Rest == "" {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter,
			"a pre-authenticated request addresses an object under /o/")

		return
	}

	if rt.Bucket != par.Bucket {
		ocirest.WriteError(w, r, http.StatusForbidden, codeNotAuthorized,
			"pre-authenticated request is scoped to bucket "+par.Bucket)

		return
	}

	if !osprovider.PARAllows(par, r.Method, rt.Rest) {
		ocirest.WriteError(w, r, http.StatusForbidden, codeNotAuthorized,
			"pre-authenticated request does not authorize "+r.Method+" on "+rt.Rest)

		return
	}

	switch r.Method {
	case http.MethodGet, http.MethodHead:
		h.getObject(w, r, par.Bucket, rt.Rest)
	default:
		h.putObject(w, r, par.Bucket, rt.Rest)
	}
}

func toPARBody(par *osprovider.PreauthenticatedRequest) parBody {
	return parBody{
		ID:                  par.ID,
		Name:                par.Name,
		ObjectName:          par.ObjectName,
		AccessType:          par.AccessType,
		BucketListingAction: par.BucketListingAction,
		TimeCreated:         par.TimeCreated,
		TimeExpires:         par.TimeExpires,
	}
}
