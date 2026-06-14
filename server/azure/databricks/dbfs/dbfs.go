// Package dbfs implements the Databricks DBFS data-plane REST API
// (the /api/2.0/dbfs/... surface) as a server.Handler, backed by an in-memory
// filesystem. Point the real github.com/databricks/databricks-sdk-go
// WorkspaceClient at a server registered with this handler and w.Dbfs.Put,
// Read, List, Mkdirs, Delete, GetStatus, and the block-upload helpers
// (WriteFile via create/add-block/close) work end-to-end.
//
// Covered endpoints:
//
//	POST /api/2.0/dbfs/mkdirs        {path}
//	POST /api/2.0/dbfs/put           {path, contents(base64), overwrite}
//	POST /api/2.0/dbfs/create        {path, overwrite} -> {handle}
//	POST /api/2.0/dbfs/add-block     {handle, data(base64)}
//	POST /api/2.0/dbfs/close         {handle}
//	POST /api/2.0/dbfs/delete        {path, recursive}
//	GET  /api/2.0/dbfs/get-status    ?path=        -> {path, is_dir, file_size}
//	GET  /api/2.0/dbfs/list          ?path=        -> {files:[...]}
//	GET  /api/2.0/dbfs/read          ?path=&offset=&length= -> {bytes_read, data}
package dbfs

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
)

const maxBodyBytes = 5 << 20

// Resource segment that this handler claims (3rd path segment after split).
const resDbfs = "dbfs"

// minSegs is the [api, ver, dbfs, action] segment count required to route.
const minSegs = 4

// Action path segments.
const (
	actMkdirs    = "mkdirs"
	actPut       = "put"
	actCreate    = "create"
	actAddBlock  = "add-block"
	actClose     = "close"
	actDelete    = "delete"
	actGetStatus = "get-status"
	actList      = "list"
	actRead      = "read"
)

// Databricks error codes.
const (
	codeNotFound     = "RESOURCE_DOES_NOT_EXIST"
	codeExists       = "RESOURCE_ALREADY_EXISTS"
	codeInvalidParam = "INVALID_PARAMETER_VALUE"
	codeNotFoundPath = "ENDPOINT_NOT_FOUND"
	codeMalformed    = "MALFORMED_REQUEST"
	codeMethod       = "INVALID_PARAMETER_VALUE"
)

// Handler serves the DBFS data-plane REST API over an in-memory filesystem.
type Handler struct {
	mu      sync.RWMutex
	files   map[string][]byte // absolute path -> contents
	dirs    map[string]bool   // absolute path -> exists (set of directories)
	blocks  map[int64][]byte  // open write-stream handle -> accumulated bytes
	bpaths  map[int64]string  // open write-stream handle -> target path
	nextHnd int64
}

// New returns a DBFS handler with an empty filesystem (root "/" always exists).
func New() *Handler {
	return &Handler{
		files:  make(map[string][]byte),
		dirs:   map[string]bool{"/": true},
		blocks: make(map[int64][]byte),
		bpaths: make(map[int64]string),
	}
}

// Matches claims /api/{ver}/dbfs/... paths.
func (*Handler) Matches(r *http.Request) bool {
	parts := splitPath(r.URL.Path)

	return len(parts) >= minSegs && parts[0] == "api" && parts[2] == resDbfs
}

// route binds an action to its required HTTP method and handler.
type route struct {
	method string
	fn     func(*Handler, http.ResponseWriter, *http.Request)
}

// routes maps each action segment to its handler. It is a method (not a global)
// to satisfy gochecknoglobals; the map is small and rebuilt per request.
func routes() map[string]route {
	return map[string]route{
		actMkdirs:    {http.MethodPost, (*Handler).mkdirs},
		actPut:       {http.MethodPost, (*Handler).put},
		actCreate:    {http.MethodPost, (*Handler).create},
		actAddBlock:  {http.MethodPost, (*Handler).addBlock},
		actClose:     {http.MethodPost, (*Handler).closeStream},
		actDelete:    {http.MethodPost, (*Handler).delete},
		actGetStatus: {http.MethodGet, (*Handler).getStatus},
		actList:      {http.MethodGet, (*Handler).list},
		actRead:      {http.MethodGet, (*Handler).read},
	}
}

// ServeHTTP routes by the action segment.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	parts := splitPath(r.URL.Path)
	if len(parts) < minSegs {
		writeErr(w, http.StatusNotFound, codeNotFoundPath, "unsupported path")

		return
	}

	rt, ok := routes()[parts[3]]
	if !ok {
		writeErr(w, http.StatusNotFound, codeNotFoundPath, "unknown action: "+parts[3])

		return
	}

	if r.Method != rt.method {
		methodNotAllowed(w)

		return
	}

	rt.fn(h, w, r)
}

type mkdirsRequest struct {
	Path string `json:"path"`
}

func (h *Handler) mkdirs(w http.ResponseWriter, r *http.Request) {
	var req mkdirsRequest
	if !decode(w, r, &req) {
		return
	}

	p := cleanPath(req.Path)
	if p == "" {
		writeErr(w, http.StatusBadRequest, codeInvalidParam, "path is required")

		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if _, ok := h.files[p]; ok {
		writeErr(w, http.StatusBadRequest, codeInvalidParam, "path already exists as a file: "+p)

		return
	}

	h.makeDirs(p)

	writeJSON(w, struct{}{})
}

type putRequest struct {
	Path      string `json:"path"`
	Contents  string `json:"contents"`
	Overwrite bool   `json:"overwrite"`
}

func (h *Handler) put(w http.ResponseWriter, r *http.Request) {
	var req putRequest
	if !decode(w, r, &req) {
		return
	}

	p := cleanPath(req.Path)
	if p == "" {
		writeErr(w, http.StatusBadRequest, codeInvalidParam, "path is required")

		return
	}

	data, err := base64.StdEncoding.DecodeString(req.Contents)
	if err != nil {
		writeErr(w, http.StatusBadRequest, codeInvalidParam, "contents is not valid base64")

		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if !h.writeFile(w, p, data, req.Overwrite) {
		return
	}

	writeJSON(w, struct{}{})
}

type createRequest struct {
	Path      string `json:"path"`
	Overwrite bool   `json:"overwrite"`
}

type createResponse struct {
	Handle int64 `json:"handle"`
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var req createRequest
	if !decode(w, r, &req) {
		return
	}

	p := cleanPath(req.Path)
	if p == "" {
		writeErr(w, http.StatusBadRequest, codeInvalidParam, "path is required")

		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if _, ok := h.files[p]; ok && !req.Overwrite {
		writeErr(w, http.StatusBadRequest, codeExists, "file already exists: "+p)

		return
	}

	if h.dirs[p] {
		writeErr(w, http.StatusBadRequest, codeInvalidParam, "path is a directory: "+p)

		return
	}

	h.nextHnd++
	hnd := h.nextHnd
	h.blocks[hnd] = []byte{}
	h.bpaths[hnd] = p

	writeJSON(w, createResponse{Handle: hnd})
}

type addBlockRequest struct {
	Handle int64  `json:"handle"`
	Data   string `json:"data"`
}

func (h *Handler) addBlock(w http.ResponseWriter, r *http.Request) {
	var req addBlockRequest
	if !decode(w, r, &req) {
		return
	}

	data, err := base64.StdEncoding.DecodeString(req.Data)
	if err != nil {
		writeErr(w, http.StatusBadRequest, codeInvalidParam, "data is not valid base64")

		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	buf, ok := h.blocks[req.Handle]
	if !ok {
		writeErr(w, http.StatusBadRequest, codeNotFound, "unknown stream handle")

		return
	}

	h.blocks[req.Handle] = append(buf, data...)

	writeJSON(w, struct{}{})
}

type closeRequest struct {
	Handle int64 `json:"handle"`
}

func (h *Handler) closeStream(w http.ResponseWriter, r *http.Request) {
	var req closeRequest
	if !decode(w, r, &req) {
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	buf, ok := h.blocks[req.Handle]
	if !ok {
		writeErr(w, http.StatusBadRequest, codeNotFound, "unknown stream handle")

		return
	}

	p := h.bpaths[req.Handle]
	delete(h.blocks, req.Handle)
	delete(h.bpaths, req.Handle)

	// The create call already enforced overwrite semantics, so persist
	// unconditionally here.
	if !h.writeFile(w, p, buf, true) {
		return
	}

	writeJSON(w, struct{}{})
}

type deleteRequest struct {
	Path      string `json:"path"`
	Recursive bool   `json:"recursive"`
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	var req deleteRequest
	if !decode(w, r, &req) {
		return
	}

	p := cleanPath(req.Path)
	if p == "" {
		writeErr(w, http.StatusBadRequest, codeInvalidParam, "path is required")

		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if _, ok := h.files[p]; ok {
		delete(h.files, p)
		writeJSON(w, struct{}{})

		return
	}

	if h.dirs[p] {
		h.deleteDir(w, p, req.Recursive)

		return
	}

	writeErr(w, http.StatusNotFound, codeNotFound, "no such file or directory: "+p)
}

// deleteDir removes directory p; requires recursive when it has children.
func (h *Handler) deleteDir(w http.ResponseWriter, p string, recursive bool) {
	children := h.childPaths(p)
	if len(children) > 0 && !recursive {
		writeErr(w, http.StatusBadRequest, codeInvalidParam, "directory not empty: "+p)

		return
	}

	for _, c := range children {
		delete(h.files, c)
		delete(h.dirs, c)
	}

	if p != "/" {
		delete(h.dirs, p)
	}

	writeJSON(w, struct{}{})
}

type fileInfo struct {
	Path     string `json:"path"`
	IsDir    bool   `json:"is_dir"`
	FileSize int64  `json:"file_size"`
}

func (h *Handler) getStatus(w http.ResponseWriter, r *http.Request) {
	p := cleanPath(r.URL.Query().Get("path"))
	if p == "" {
		writeErr(w, http.StatusBadRequest, codeInvalidParam, "path is required")

		return
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	if data, ok := h.files[p]; ok {
		writeJSON(w, fileInfo{Path: p, IsDir: false, FileSize: int64(len(data))})

		return
	}

	if h.dirs[p] {
		writeJSON(w, fileInfo{Path: p, IsDir: true})

		return
	}

	writeErr(w, http.StatusNotFound, codeNotFound, "no such file or directory: "+p)
}

type listResponse struct {
	Files []fileInfo `json:"files"`
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	p := cleanPath(r.URL.Query().Get("path"))
	if p == "" {
		writeErr(w, http.StatusBadRequest, codeInvalidParam, "path is required")

		return
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	// Listing a file returns details of just that file.
	if data, ok := h.files[p]; ok {
		writeJSON(w, listResponse{Files: []fileInfo{{Path: p, IsDir: false, FileSize: int64(len(data))}}})

		return
	}

	if !h.dirs[p] {
		writeErr(w, http.StatusNotFound, codeNotFound, "no such file or directory: "+p)

		return
	}

	writeJSON(w, listResponse{Files: h.directChildren(p)})
}

type readResponse struct {
	BytesRead int64  `json:"bytes_read"`
	Data      string `json:"data"`
}

func (h *Handler) read(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	p := cleanPath(q.Get("path"))
	if p == "" {
		writeErr(w, http.StatusBadRequest, codeInvalidParam, "path is required")

		return
	}

	offset, length, ok := parseReadRange(w, q.Get("offset"), q.Get("length"))
	if !ok {
		return
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	data, ok := h.files[p]
	if !ok {
		if h.dirs[p] {
			writeErr(w, http.StatusBadRequest, codeInvalidParam, "cannot read a directory: "+p)

			return
		}

		writeErr(w, http.StatusNotFound, codeNotFound, "no such file: "+p)

		return
	}

	chunk := sliceRange(data, offset, length)
	writeJSON(w, readResponse{
		BytesRead: int64(len(chunk)),
		Data:      base64.StdEncoding.EncodeToString(chunk),
	})
}

// parseReadRange parses the offset/length query params (both optional).
func parseReadRange(w http.ResponseWriter, offsetStr, lengthStr string) (offset, length int64, ok bool) {
	var err error

	if offsetStr != "" {
		offset, err = strconv.ParseInt(offsetStr, 10, 64)
		if err != nil || offset < 0 {
			writeErr(w, http.StatusBadRequest, codeInvalidParam, "invalid offset")

			return 0, 0, false
		}
	}

	if lengthStr != "" {
		length, err = strconv.ParseInt(lengthStr, 10, 64)
		if err != nil || length < 0 {
			writeErr(w, http.StatusBadRequest, codeInvalidParam, "invalid length")

			return 0, 0, false
		}
	}

	return offset, length, true
}

// sliceRange returns data[offset:offset+length], clamped to bounds. A length of
// 0 means read to end of file.
func sliceRange(data []byte, offset, length int64) []byte {
	if offset >= int64(len(data)) {
		return nil
	}

	end := int64(len(data))
	// Guard against int64 overflow: a huge length (e.g. math.MaxInt64) makes
	// offset+length wrap negative, which would slice with low > high and panic.
	if want := offset + length; length > 0 && want >= offset && want < end {
		end = want
	}

	return data[offset:end]
}

// writeFile persists data at p, enforcing overwrite and creating parent dirs.
// It writes an error response and returns false on failure. Caller holds the
// write lock.
func (h *Handler) writeFile(w http.ResponseWriter, p string, data []byte, overwrite bool) bool {
	if h.dirs[p] {
		writeErr(w, http.StatusBadRequest, codeInvalidParam, "path is a directory: "+p)

		return false
	}

	if _, ok := h.files[p]; ok && !overwrite {
		writeErr(w, http.StatusBadRequest, codeExists, "file already exists: "+p)

		return false
	}

	h.makeDirs(parentOf(p))
	h.files[p] = data

	return true
}

// makeDirs records p and all of its ancestors as directories. Caller holds the
// write lock.
func (h *Handler) makeDirs(p string) {
	for d := cleanPath(p); d != ""; d = parentOf(d) {
		h.dirs[d] = true

		if d == "/" {
			break
		}
	}
}

// childPaths returns every file and directory strictly beneath dir. Caller
// holds a lock.
func (h *Handler) childPaths(dir string) []string {
	prefix := dirPrefix(dir)

	var out []string

	for f := range h.files {
		if strings.HasPrefix(f, prefix) {
			out = append(out, f)
		}
	}

	for d := range h.dirs {
		if d != dir && strings.HasPrefix(d, prefix) {
			out = append(out, d)
		}
	}

	return out
}

// directChildren returns the immediate entries of dir, sorted by path. Caller
// holds a lock.
func (h *Handler) directChildren(dir string) []fileInfo {
	prefix := dirPrefix(dir)

	out := make([]fileInfo, 0)

	for f, data := range h.files {
		if isDirectChild(f, prefix) {
			out = append(out, fileInfo{Path: f, IsDir: false, FileSize: int64(len(data))})
		}
	}

	for d := range h.dirs {
		if d != dir && isDirectChild(d, prefix) {
			out = append(out, fileInfo{Path: d, IsDir: true})
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })

	return out
}

// isDirectChild reports whether p sits immediately under prefix (no deeper).
func isDirectChild(p, prefix string) bool {
	if !strings.HasPrefix(p, prefix) {
		return false
	}

	return !strings.Contains(p[len(prefix):], "/")
}

// dirPrefix returns dir with a guaranteed trailing slash for prefix matching.
func dirPrefix(dir string) string {
	if dir == "/" {
		return "/"
	}

	return dir + "/"
}

// parentOf returns the parent directory of an absolute path.
func parentOf(p string) string {
	parent := path.Dir(p)
	if parent == "." {
		return "/"
	}

	return parent
}

// cleanPath normalizes a DBFS path to an absolute, slash-cleaned form. An empty
// or root-only input returns "/" for callers that accept it; truly empty input
// (after trimming) returns "".
func cleanPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}

	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}

	cleaned := path.Clean(p)

	return cleaned
}

// splitPath strips leading/trailing slashes and splits on "/".
func splitPath(p string) []string {
	trimmed := strings.Trim(p, "/")
	if trimmed == "" {
		return nil
	}

	return strings.Split(trimmed, "/")
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, codeMalformed, "invalid JSON: "+err.Error())

		return false
	}

	return true
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(v)
}

// errorBody is the Databricks error envelope shape.
type errorBody struct {
	ErrorCode string `json:"error_code"`
	Message   string `json:"message"`
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorBody{ErrorCode: code, Message: msg})
}

func methodNotAllowed(w http.ResponseWriter) {
	writeErr(w, http.StatusMethodNotAllowed, codeMethod, "method not allowed")
}
