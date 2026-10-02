package apigateway

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/apigateway/driver"
)

// clientCertificateResponse is the ClientCertificate wire object.
type clientCertificateResponse struct {
	ClientCertificateID   string            `json:"clientCertificateId"`
	Description           string            `json:"description,omitempty"`
	PEMEncodedCertificate string            `json:"pemEncodedCertificate"`
	CreatedDate           int64             `json:"createdDate"`
	ExpirationDate        int64             `json:"expirationDate"`
	Tags                  map[string]string `json:"tags,omitempty"`
}

type listClientCertificatesResponse struct {
	Position string                      `json:"position,omitempty"`
	Item     []clientCertificateResponse `json:"item"`
}

type generateClientCertificateRequest struct {
	Description string            `json:"description"`
	Tags        map[string]string `json:"tags"`
}

func toClientCertificateResponse(cc *driver.ClientCertificate) clientCertificateResponse {
	return clientCertificateResponse{
		ClientCertificateID: cc.ID, Description: cc.Description, PEMEncodedCertificate: cc.PEMEncodedCertificate,
		CreatedDate: cc.CreatedDate, ExpirationDate: cc.ExpirationDate, Tags: cc.Tags,
	}
}

// serveClientCertificates handles /clientcertificates (GET list, POST
// generate) and /clientcertificates/{id} (GET, PATCH, DELETE).
func (h *Handler) serveClientCertificates(w http.ResponseWriter, r *http.Request, id string) {
	if id != "" {
		serveItem(w, r,
			func(ops []driver.PatchOperation) (*driver.ClientCertificate, error) {
				return h.ag.UpdateClientCertificate(r.Context(), id, ops)
			},
			func() (*driver.ClientCertificate, error) { return h.ag.GetClientCertificate(r.Context(), id) },
			func() error { return h.ag.DeleteClientCertificate(r.Context(), id) },
			toClientCertificateResponse,
		)

		return
	}

	switch r.Method {
	case http.MethodGet:
		page, ok := pageInput(w, r)
		if !ok {
			return
		}

		res, err := h.ag.GetClientCertificates(r.Context(), page)
		if err != nil {
			writeErr(w, err)
			return
		}

		out := listClientCertificatesResponse{Position: res.Position, Item: make([]clientCertificateResponse, 0, len(res.Items))}
		for i := range res.Items {
			out.Item = append(out.Item, toClientCertificateResponse(&res.Items[i]))
		}

		writeJSON(w, http.StatusOK, out)
	case http.MethodPost:
		var req generateClientCertificateRequest
		if !decodeJSON(w, r, &req) {
			return
		}

		cc, err := h.ag.GenerateClientCertificate(r.Context(), driver.GenerateClientCertificateInput{
			Description: req.Description, Tags: req.Tags,
		})
		if err != nil {
			writeErr(w, err)
			return
		}

		writeJSON(w, http.StatusCreated, toClientCertificateResponse(cc))
	default:
		writeMethodNotAllowed(w)
	}
}

// pageInput reads the position and limit query parameters. A limit that is
// not an integer is a BadRequest.
func pageInput(w http.ResponseWriter, r *http.Request) (driver.PageInput, bool) {
	q := r.URL.Query()
	in := driver.PageInput{Position: q.Get("position")}

	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "BadRequestException", "Invalid limit specified: "+raw)
			return in, false
		}

		in.Limit = n
	}

	return in, true
}

// ownsTagsPath reports whether p is /tags/{arn} for a resource this handler
// tags: a REST API or a client certificate. Other API Gateway ARNs (the v2
// /apis tree) and other services' ARNs fall through.
func ownsTagsPath(p string) bool {
	arn, ok := strings.CutPrefix(p, tagsPrefix)
	if !ok {
		return false
	}

	_, resource, found := strings.Cut(arn, "::")

	return found && strings.Contains(arn, ":apigateway:") &&
		(strings.HasPrefix(resource, controlPrefix+"/") || strings.HasPrefix(resource, certsPrefix+"/"))
}

type tagsBody struct {
	Tags map[string]string `json:"tags"`
}

// serveTags handles /tags/{arn}: PUT=TagResource, DELETE=UntagResource,
// GET=GetTags.
func (h *Handler) serveTags(w http.ResponseWriter, r *http.Request, arn string) {
	switch r.Method {
	case http.MethodPut:
		var req tagsBody
		if !decodeJSON(w, r, &req) {
			return
		}

		if err := h.ag.TagResource(r.Context(), arn, req.Tags); err != nil {
			writeErr(w, err)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	case http.MethodDelete:
		if err := h.ag.UntagResource(r.Context(), arn, r.URL.Query()["tagKeys"]); err != nil {
			writeErr(w, err)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	case http.MethodGet:
		tags, err := h.ag.GetTags(r.Context(), arn)
		if err != nil {
			writeErr(w, err)
			return
		}

		writeJSON(w, http.StatusOK, tagsBody{Tags: tags})
	default:
		writeMethodNotAllowed(w)
	}
}
