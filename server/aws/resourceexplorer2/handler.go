// Package resourceexplorer2 serves the AWS Resource Explorer 2 REST-JSON
// protocol against a *resourcediscovery.Engine.
//
// Resource Explorer maintains stateful Views (saved filter expressions)
// and Indexes (per-region resource catalogs). This handler stores both in
// memory and routes Search/ListResources through the engine.
//
// Filter language: a documented subset of real Resource Explorer query
// syntax. Supported tokens:
//
//	service:<name>          (e.g., service:s3)
//	tag.<key>:<value>       (e.g., tag.env:prod)
//	region:<name>           (e.g., region:us-east-1)
//
// Unknown tokens are tolerated and ignored. Real Resource Explorer supports
// a wider grammar; tighten this as later phases need.
package resourceexplorer2

import (
	"net/http"
	"strings"
	"sync"
	"time"

	cerrors "github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/internal/idgen"
	"github.com/stackshy/cloudemu/resourcediscovery"
	"github.com/stackshy/cloudemu/server/wire"
)

// AWS service identifiers used in Resource Explorer ResourceType strings
// and in TagResources/UntagResources ARN routing.
const (
	awsServiceEC2      = "ec2"
	awsServiceS3       = "s3"
	awsServiceDynamoDB = "dynamodb"
	awsServiceLambda   = "lambda"
)

// Portable-API service identifiers as emitted by resourcediscovery walkers.
const (
	portableServiceCompute    = "compute"
	portableServiceNetworking = "networking"
	portableServiceStorage    = "storage"
	portableServiceDatabase   = "database"
	portableServiceServerless = "serverless"
)

// Handler serves Resource Explorer 2 REST-JSON requests.
type Handler struct {
	engine *resourcediscovery.Engine

	accountID string
	region    string

	mu      sync.RWMutex
	views   map[string]*view  // keyed by ViewArn
	indexes map[string]*index // keyed by region
}

type view struct {
	ARN         string
	Name        string
	QueryString string
	Tags        map[string]string
	CreatedAt   time.Time
}

type index struct {
	ARN       string
	Region    string
	Type      string // "LOCAL" or "AGGREGATOR"
	CreatedAt time.Time
}

// New returns a Resource Explorer 2 handler.
func New(engine *resourcediscovery.Engine, accountID, region string) *Handler {
	h := &Handler{
		engine:    engine,
		accountID: accountID,
		region:    region,
		views:     make(map[string]*view),
		indexes:   make(map[string]*index),
	}

	// Real Resource Explorer requires an Index in the calling region before
	// Search works. Bootstrap a LOCAL index for the configured region so
	// SDK clients don't have to call CreateIndex first.
	idxARN := h.indexARN(region)
	h.indexes[region] = &index{
		ARN: idxARN, Region: region, Type: "LOCAL", CreatedAt: time.Now().UTC(),
	}

	return h
}

// Known REST-JSON paths for Resource Explorer 2. The handler matches only
// these exact paths to avoid shadowing S3's REST catch-all.
//
//nolint:gochecknoglobals // immutable lookup table.
var knownPaths = map[string]struct{}{
	"/CreateView":    {},
	"/DeleteView":    {},
	"/ListViews":     {},
	"/GetView":       {},
	"/Search":        {},
	"/ListResources": {},
	"/ListIndexes":   {},
	"/GetIndex":      {},
}

// Matches returns true for POST requests whose path is one of the known
// Resource Explorer 2 operations.
func (*Handler) Matches(r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}

	_, ok := knownPaths[r.URL.Path]

	return ok
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/CreateView":
		h.createView(w, r)
	case "/DeleteView":
		h.deleteView(w, r)
	case "/ListViews":
		h.listViews(w, r)
	case "/GetView":
		h.getView(w, r)
	case "/Search":
		h.search(w, r)
	case "/ListResources":
		h.listResources(w, r)
	case "/ListIndexes":
		h.listIndexes(w, r)
	case "/GetIndex":
		h.getIndex(w, r)
	default:
		wire.WriteJSONError(w, http.StatusNotFound, "ResourceNotFoundException", "unknown path: "+r.URL.Path)
	}
}

func (h *Handler) createView(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ViewName    string            `json:"ViewName"`
		Tags        map[string]string `json:"Tags"`
		QueryString string            `json:"-"` // Smithy puts this under Filters.FilterString; tolerate either.
		Filters     struct {
			FilterString string `json:"FilterString"`
		} `json:"Filters"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if req.ViewName == "" {
		wire.WriteJSONError(w, http.StatusBadRequest, "ValidationException", "ViewName is required")
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	arn := h.viewARN(req.ViewName)
	if _, exists := h.views[arn]; exists {
		wire.WriteJSONError(w, http.StatusConflict, "ConflictException", "view already exists: "+req.ViewName)
		return
	}

	v := &view{
		ARN:         arn,
		Name:        req.ViewName,
		QueryString: req.Filters.FilterString,
		Tags:        copyStringMap(req.Tags),
		CreatedAt:   time.Now().UTC(),
	}
	h.views[arn] = v

	wire.WriteJSON(w, map[string]any{
		"View": viewToWire(v),
	})
}

func (h *Handler) deleteView(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ViewArn string `json:"ViewArn"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if _, ok := h.views[req.ViewArn]; !ok {
		wire.WriteJSONError(w, http.StatusNotFound, "ResourceNotFoundException", "view not found: "+req.ViewArn)
		return
	}

	delete(h.views, req.ViewArn)
	wire.WriteJSON(w, map[string]any{"ViewArn": req.ViewArn})
}

func (h *Handler) listViews(w http.ResponseWriter, _ *http.Request) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	arns := make([]string, 0, len(h.views))
	for arn := range h.views {
		arns = append(arns, arn)
	}

	wire.WriteJSON(w, map[string]any{"Views": arns})
}

func (h *Handler) getView(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ViewArn string `json:"ViewArn"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	v, ok := h.views[req.ViewArn]
	if !ok {
		wire.WriteJSONError(w, http.StatusNotFound, "ResourceNotFoundException", "view not found: "+req.ViewArn)
		return
	}

	wire.WriteJSON(w, map[string]any{
		"View": viewToWire(v),
		"Tags": v.Tags,
	})
}

func (h *Handler) search(w http.ResponseWriter, r *http.Request) {
	var req struct {
		QueryString string `json:"QueryString"`
		ViewArn     string `json:"ViewArn"`
		MaxResults  int    `json:"MaxResults"`
		NextToken   string `json:"NextToken"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	query := req.QueryString

	// If a view is referenced, merge its FilterString with the request's
	// QueryString. Real Resource Explorer treats them as additive AND.
	if req.ViewArn != "" {
		h.mu.RLock()
		v, ok := h.views[req.ViewArn]
		h.mu.RUnlock()

		if !ok {
			wire.WriteJSONError(w, http.StatusNotFound, "ResourceNotFoundException", "view not found: "+req.ViewArn)
			return
		}

		query = strings.TrimSpace(v.QueryString + " " + query)
	}

	q := parseFilter(query)

	results, err := h.engine.List(r.Context(), q)
	if err != nil {
		writeErr(w, err)
		return
	}

	resources := make([]map[string]any, 0, len(results))
	for i := range results {
		resources = append(resources, resourceToWire(&results[i]))
	}

	wire.WriteJSON(w, map[string]any{
		"Resources": resources,
		"Count":     map[string]any{"TotalResources": len(resources), "Complete": true},
	})
}

func (h *Handler) listResources(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Filters struct {
			FilterString string `json:"FilterString"`
		} `json:"Filters"`
		ViewArn string `json:"ViewArn"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	query := req.Filters.FilterString

	if req.ViewArn != "" {
		h.mu.RLock()
		v, ok := h.views[req.ViewArn]
		h.mu.RUnlock()

		if !ok {
			wire.WriteJSONError(w, http.StatusNotFound, "ResourceNotFoundException", "view not found: "+req.ViewArn)
			return
		}

		query = strings.TrimSpace(v.QueryString + " " + query)
	}

	q := parseFilter(query)

	results, err := h.engine.List(r.Context(), q)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := make([]map[string]any, 0, len(results))
	for i := range results {
		out = append(out, resourceToWire(&results[i]))
	}

	wire.WriteJSON(w, map[string]any{"Resources": out})
}

func (h *Handler) listIndexes(w http.ResponseWriter, _ *http.Request) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	out := make([]map[string]any, 0, len(h.indexes))
	for _, idx := range h.indexes {
		out = append(out, map[string]any{
			"Arn":    idx.ARN,
			"Region": idx.Region,
			"Type":   idx.Type,
		})
	}

	wire.WriteJSON(w, map[string]any{"Indexes": out})
}

func (h *Handler) getIndex(w http.ResponseWriter, _ *http.Request) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	idx, ok := h.indexes[h.region]
	if !ok {
		wire.WriteJSONError(w, http.StatusNotFound, "ResourceNotFoundException", "no index in region: "+h.region)
		return
	}

	wire.WriteJSON(w, map[string]any{
		"Arn":         idx.ARN,
		"Region":      idx.Region,
		"Type":        idx.Type,
		"State":       "ACTIVE",
		"CreatedAt":   idx.CreatedAt.Format(time.RFC3339),
		"LastUpdated": idx.CreatedAt.Format(time.RFC3339),
	})
}

func (h *Handler) viewARN(name string) string {
	return idgen.AWSARN("resource-explorer-2", h.region, h.accountID, "view/"+name+"/"+idgen.GenerateID(""))
}

func (h *Handler) indexARN(region string) string {
	return idgen.AWSARN("resource-explorer-2", region, h.accountID, "index/"+idgen.GenerateID(""))
}

func viewToWire(v *view) map[string]any {
	return map[string]any{
		"ViewArn": v.ARN,
		"Filters": map[string]any{"FilterString": v.QueryString},
		"Owner":   "",
	}
}

func resourceToWire(r *resourcediscovery.Resource) map[string]any {
	return map[string]any{
		"Arn":             r.ARN,
		"OwningAccountId": "",
		"Region":          r.Region,
		"ResourceType":    r.Service + ":" + strings.ToLower(r.Type),
		"Service":         portableToAWSService(r.Service),
		"Properties":      []map[string]any{},
	}
}

func portableToAWSService(s string) string {
	switch s {
	case portableServiceCompute, portableServiceNetworking:
		return awsServiceEC2
	case portableServiceStorage:
		return awsServiceS3
	case portableServiceDatabase:
		return awsServiceDynamoDB
	case portableServiceServerless:
		return awsServiceLambda
	default:
		return s
	}
}

func copyStringMap(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}

	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}

	return out
}

func writeErr(w http.ResponseWriter, err error) {
	switch {
	case cerrors.IsNotFound(err):
		wire.WriteJSONError(w, http.StatusNotFound, "ResourceNotFoundException", err.Error())
	case cerrors.IsInvalidArgument(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "ValidationException", err.Error())
	default:
		wire.WriteJSONError(w, http.StatusInternalServerError, "InternalServerException", err.Error())
	}
}
