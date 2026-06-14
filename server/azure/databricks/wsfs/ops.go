package wsfs

import (
	"encoding/base64"
	"net/http"
	"strings"
)

// importRequest is the POST /import body.
type importRequest struct {
	Path      string `json:"path"`
	Content   string `json:"content"`
	Format    string `json:"format"`
	Language  string `json:"language"`
	Overwrite bool   `json:"overwrite"`
}

// exportResponse is the GET /export body.
type exportResponse struct {
	Content  string `json:"content"`
	FileType string `json:"file_type"`
}

// objectInfo is the wire shape returned by list and get-status.
type objectInfo struct {
	Path       string `json:"path"`
	ObjectType string `json:"object_type"`
	ObjectID   int64  `json:"object_id"`
	Language   string `json:"language,omitempty"`
}

// listResponse is the GET /list body.
type listResponse struct {
	Objects []objectInfo `json:"objects"`
}

// mkdirsRequest is the POST /mkdirs body.
type mkdirsRequest struct {
	Path string `json:"path"`
}

// deleteRequest is the POST /delete body.
type deleteRequest struct {
	Path      string `json:"path"`
	Recursive bool   `json:"recursive"`
}

func (h *Handler) serveImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		wsfsMethodNotAllowed(w)

		return
	}

	var req importRequest
	if !wsfsDecode(w, r, &req) {
		return
	}

	path := normalizePath(req.Path)
	if path == "" {
		wsfsError(w, http.StatusBadRequest, "INVALID_PARAMETER_VALUE", "path is required")

		return
	}

	content, err := base64.StdEncoding.DecodeString(req.Content)
	if err != nil {
		wsfsError(w, http.StatusBadRequest, "INVALID_PARAMETER_VALUE", "content is not valid base64")

		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if existing, ok := h.objects[path]; ok {
		if existing.objectType == objectDirectory {
			wsfsError(w, http.StatusBadRequest, "INVALID_PARAMETER_VALUE", "path is a directory")

			return
		}

		if !req.Overwrite {
			wsfsError(w, http.StatusBadRequest, "RESOURCE_ALREADY_EXISTS", "object already exists: "+path)

			return
		}
	}

	language := req.Language
	if language == "" {
		language = defaultLanguage
	}

	h.nextID++
	h.objects[path] = &object{
		content:    content,
		objectType: objectNotebook,
		language:   language,
		objectID:   h.nextID,
	}

	wsfsJSON(w, struct{}{})
}

func (h *Handler) serveExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		wsfsMethodNotAllowed(w)

		return
	}

	path := normalizePath(r.URL.Query().Get("path"))
	if path == "" {
		wsfsError(w, http.StatusBadRequest, "INVALID_PARAMETER_VALUE", "path is required")

		return
	}

	format := r.URL.Query().Get("format")
	if format == "" {
		format = "SOURCE"
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	obj, ok := h.objects[path]
	if !ok {
		wsfsError(w, http.StatusNotFound, "RESOURCE_DOES_NOT_EXIST", "object does not exist: "+path)

		return
	}

	if obj.objectType == objectDirectory {
		wsfsError(w, http.StatusBadRequest, "INVALID_PARAMETER_VALUE", "cannot export a directory in this format")

		return
	}

	wsfsJSON(w, exportResponse{
		Content:  base64.StdEncoding.EncodeToString(obj.content),
		FileType: format,
	})
}

func (h *Handler) serveList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		wsfsMethodNotAllowed(w)

		return
	}

	path := normalizePath(r.URL.Query().Get("path"))
	if path == "" {
		wsfsError(w, http.StatusBadRequest, "INVALID_PARAMETER_VALUE", "path is required")

		return
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	if _, ok := h.objects[path]; !ok {
		wsfsError(w, http.StatusNotFound, "RESOURCE_DOES_NOT_EXIST", "path does not exist: "+path)

		return
	}

	out := make([]objectInfo, 0)

	for p, obj := range h.objects {
		if p == path || p == "/" {
			continue
		}

		if parentOf(p) == path {
			out = append(out, h.toObjectInfo(p, obj))
		}
	}

	wsfsJSON(w, listResponse{Objects: out})
}

func (h *Handler) serveMkdirs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		wsfsMethodNotAllowed(w)

		return
	}

	var req mkdirsRequest
	if !wsfsDecode(w, r, &req) {
		return
	}

	path := normalizePath(req.Path)
	if path == "" {
		wsfsError(w, http.StatusBadRequest, "INVALID_PARAMETER_VALUE", "path is required")

		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if existing, ok := h.objects[path]; ok {
		if existing.objectType == objectDirectory {
			wsfsJSON(w, struct{}{})

			return
		}

		wsfsError(w, http.StatusBadRequest, "RESOURCE_ALREADY_EXISTS", "a non-directory object exists at: "+path)

		return
	}

	h.makeDirs(path)
	wsfsJSON(w, struct{}{})
}

func (h *Handler) serveDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		wsfsMethodNotAllowed(w)

		return
	}

	var req deleteRequest
	if !wsfsDecode(w, r, &req) {
		return
	}

	path := normalizePath(req.Path)
	if path == "" {
		wsfsError(w, http.StatusBadRequest, "INVALID_PARAMETER_VALUE", "path is required")

		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	obj, ok := h.objects[path]
	if !ok {
		wsfsError(w, http.StatusNotFound, "RESOURCE_DOES_NOT_EXIST", "object does not exist: "+path)

		return
	}

	if obj.objectType == objectDirectory {
		if children := h.childPaths(path); len(children) > 0 {
			if !req.Recursive {
				wsfsError(w, http.StatusBadRequest, "DIRECTORY_NOT_EMPTY", "directory is not empty: "+path)

				return
			}

			for _, c := range children {
				delete(h.objects, c)
			}
		}
	}

	delete(h.objects, path)
	wsfsJSON(w, struct{}{})
}

func (h *Handler) serveGetStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		wsfsMethodNotAllowed(w)

		return
	}

	path := normalizePath(r.URL.Query().Get("path"))
	if path == "" {
		wsfsError(w, http.StatusBadRequest, "INVALID_PARAMETER_VALUE", "path is required")

		return
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	obj, ok := h.objects[path]
	if !ok {
		wsfsError(w, http.StatusNotFound, "RESOURCE_DOES_NOT_EXIST", "object does not exist: "+path)

		return
	}

	wsfsJSON(w, h.toObjectInfo(path, obj))
}

// toObjectInfo converts a stored object to its wire shape. Caller holds the lock.
func (*Handler) toObjectInfo(path string, obj *object) objectInfo {
	info := objectInfo{
		Path:       path,
		ObjectType: obj.objectType,
		ObjectID:   obj.objectID,
	}

	if obj.objectType == objectNotebook {
		info.Language = obj.language
	}

	return info
}

// makeDirs creates path and any missing parent directories. Caller holds the lock.
func (h *Handler) makeDirs(path string) {
	for cur := path; cur != "/" && cur != ""; cur = parentOf(cur) {
		if _, ok := h.objects[cur]; ok {
			continue
		}

		h.nextID++
		h.objects[cur] = &object{objectType: objectDirectory, objectID: h.nextID}
	}
}

// childPaths returns every stored path nested under dir at any depth. Caller
// holds the lock.
func (h *Handler) childPaths(dir string) []string {
	prefix := dir + "/"

	var children []string

	for p := range h.objects {
		if strings.HasPrefix(p, prefix) {
			children = append(children, p)
		}
	}

	return children
}
