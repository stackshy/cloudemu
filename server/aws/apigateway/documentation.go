package apigateway

import (
	"io"
	"net/http"
	"strconv"

	"github.com/stackshy/cloudemu/v2/services/apigateway/driver"
)

// Documentation sub-collections under /restapis/{id}/documentation.
const (
	docParts    = "parts"
	docVersions = "versions"
)

type docLocation struct {
	Type       string `json:"type"`
	Path       string `json:"path,omitempty"`
	Method     string `json:"method,omitempty"`
	StatusCode string `json:"statusCode,omitempty"`
	Name       string `json:"name,omitempty"`
}

type docPartResponse struct {
	ID         string      `json:"id"`
	Location   docLocation `json:"location"`
	Properties string      `json:"properties"`
}

type listDocPartsResponse struct {
	Position string            `json:"position,omitempty"`
	Item     []docPartResponse `json:"item"`
}

type createDocPartRequest struct {
	Location   docLocation `json:"location"`
	Properties string      `json:"properties"`
}

type docPartIDsResponse struct {
	IDs      []string `json:"ids"`
	Warnings []string `json:"warnings,omitempty"`
}

type docVersionResponse struct {
	Version     string `json:"version"`
	Description string `json:"description,omitempty"`
	CreatedDate int64  `json:"createdDate"`
}

type listDocVersionsResponse struct {
	Position string               `json:"position,omitempty"`
	Item     []docVersionResponse `json:"item"`
}

type createDocVersionRequest struct {
	DocumentationVersion string `json:"documentationVersion"`
	StageName            string `json:"stageName"`
	Description          string `json:"description"`
}

func toDocPartResponse(p *driver.DocumentationPart) docPartResponse {
	l := p.Location

	return docPartResponse{
		ID: p.ID, Properties: p.Properties,
		Location: docLocation{Type: l.Type, Path: l.Path, Method: l.Method, StatusCode: l.StatusCode, Name: l.Name},
	}
}

func toDocVersionResponse(v *driver.DocumentationVersion) docVersionResponse {
	return docVersionResponse{Version: v.Version, Description: v.Description, CreatedDate: v.CreatedDate}
}

// serveDocCollection handles /restapis/{id}/documentation/{parts|versions}.
func (h *Handler) serveDocCollection(w http.ResponseWriter, r *http.Request, id, coll string) {
	switch coll {
	case docParts:
		h.serveDocParts(w, r, id)
	case docVersions:
		h.serveDocVersions(w, r, id)
	default:
		writeError(w, http.StatusNotFound, "NotFoundException", "unsupported API Gateway path")
	}
}

// serveDocItem handles /restapis/{id}/documentation/{parts|versions}/{item}.
func (h *Handler) serveDocItem(w http.ResponseWriter, r *http.Request, segs []string) {
	id, item := segs[0], segs[3]

	switch {
	case segs[1] == subDocs && segs[2] == docParts:
		serveItem(w, r,
			func(ops []driver.PatchOperation) (*driver.DocumentationPart, error) {
				return h.ag.UpdateDocumentationPart(r.Context(), id, item, ops)
			},
			func() (*driver.DocumentationPart, error) { return h.ag.GetDocumentationPart(r.Context(), id, item) },
			func() error { return h.ag.DeleteDocumentationPart(r.Context(), id, item) },
			toDocPartResponse,
		)
	case segs[1] == subDocs && segs[2] == docVersions:
		serveItem(w, r,
			func(ops []driver.PatchOperation) (*driver.DocumentationVersion, error) {
				return h.ag.UpdateDocumentationVersion(r.Context(), id, item, ops)
			},
			func() (*driver.DocumentationVersion, error) {
				return h.ag.GetDocumentationVersion(r.Context(), id, item)
			},
			func() error { return h.ag.DeleteDocumentationVersion(r.Context(), id, item) },
			toDocVersionResponse,
		)
	default:
		writeError(w, http.StatusNotFound, "NotFoundException", "unsupported API Gateway path")
	}
}

// serveDocParts handles GET=GetDocumentationParts, POST=CreateDocumentationPart
// and PUT=ImportDocumentationParts.
func (h *Handler) serveDocParts(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		h.getDocParts(w, r, id)
	case http.MethodPost:
		var req createDocPartRequest
		if !decodeJSON(w, r, &req) {
			return
		}

		l := req.Location

		p, err := h.ag.CreateDocumentationPart(r.Context(), id, &driver.CreateDocumentationPartInput{
			Properties: req.Properties,
			Location: driver.DocumentationPartLocation{
				Type: l.Type, Path: l.Path, Method: l.Method, StatusCode: l.StatusCode, Name: l.Name,
			},
		})
		if err != nil {
			writeErr(w, err)
			return
		}

		writeJSON(w, http.StatusCreated, toDocPartResponse(p))
	case http.MethodPut:
		h.importDocParts(w, r, id)
	default:
		writeMethodNotAllowed(w)
	}
}

func (h *Handler) getDocParts(w http.ResponseWriter, r *http.Request, id string) {
	page, ok := pageInput(w, r)
	if !ok {
		return
	}

	q := r.URL.Query()

	res, err := h.ag.GetDocumentationParts(r.Context(), id, &driver.GetDocumentationPartsInput{
		Type: q.Get("type"), Path: q.Get("path"), NameQuery: q.Get("name"),
		LocationStatus: q.Get("locationStatus"), PageInput: page,
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	out := listDocPartsResponse{Position: res.Position, Item: make([]docPartResponse, 0, len(res.Items))}
	for i := range res.Items {
		out.Item = append(out.Item, toDocPartResponse(&res.Items[i]))
	}

	writeJSON(w, http.StatusOK, out)
}

// importDocParts reads the raw OpenAPI body plus the mode and failonwarnings
// query parameters.
func (h *Handler) importDocParts(w http.ResponseWriter, r *http.Request, id string) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "BadRequestException", err.Error())
		return
	}

	q := r.URL.Query()
	failOnWarnings, _ := strconv.ParseBool(q.Get("failonwarnings"))

	res, err := h.ag.ImportDocumentationParts(r.Context(), id, driver.ImportDocumentationPartsInput{
		Mode: q.Get("mode"), FailOnWarnings: failOnWarnings, Body: body,
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	out := docPartIDsResponse{IDs: res.IDs, Warnings: res.Warnings}
	if out.IDs == nil {
		out.IDs = []string{}
	}

	writeJSON(w, http.StatusOK, out)
}

// serveDocVersions handles GET=GetDocumentationVersions and
// POST=CreateDocumentationVersion.
func (h *Handler) serveDocVersions(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		page, ok := pageInput(w, r)
		if !ok {
			return
		}

		res, err := h.ag.GetDocumentationVersions(r.Context(), id, page)
		if err != nil {
			writeErr(w, err)
			return
		}

		out := listDocVersionsResponse{Position: res.Position, Item: make([]docVersionResponse, 0, len(res.Items))}
		for i := range res.Items {
			out.Item = append(out.Item, toDocVersionResponse(&res.Items[i]))
		}

		writeJSON(w, http.StatusOK, out)
	case http.MethodPost:
		var req createDocVersionRequest
		if !decodeJSON(w, r, &req) {
			return
		}

		v, err := h.ag.CreateDocumentationVersion(r.Context(), id, driver.CreateDocumentationVersionInput{
			Version: req.DocumentationVersion, StageName: req.StageName, Description: req.Description,
		})
		if err != nil {
			writeErr(w, err)
			return
		}

		writeJSON(w, http.StatusCreated, toDocVersionResponse(v))
	default:
		writeMethodNotAllowed(w)
	}
}
