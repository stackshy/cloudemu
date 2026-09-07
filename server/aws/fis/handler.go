// Package fis implements the AWS Fault Injection Simulator (FIS) control-plane
// API (restJson1) as a server.Handler. Point the real
// aws-sdk-go-v2/service/fis client (or the `aws fis` CLI, or the
// aws_fis_experiment_template Terraform resource) at a Server registered with
// this handler and the experiment-template, experiment and tagging operations
// work end-to-end against an in-memory driver.
//
// FIS routes by HTTP verb + path at the root (e.g. POST /experimentTemplates,
// GET /experimentTemplates/{id}, PATCH /experimentTemplates/{id},
// POST /experiments, DELETE /experiments/{id}, POST /tags/{arn}); there is no
// X-Amz-Target header and no version prefix. Matches claims the
// /experimentTemplates and /experiments trees — which are distinctive to FIS —
// and the shared /tags path only when the ARN names a FIS (:fis:) resource, so
// it runs before the S3 catch-all and never shadows a sibling service's tag
// operations.
//
// This is a control-plane-only surface: it does NOT inject any real faults. An
// experiment is started directly into the running state and stopped on request.
package fis

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/fis/driver"
)

// Path roots at the service root.
const (
	rootTemplates   = "experimentTemplates"
	rootExperiments = "experiments"
	rootTags        = "tags"
)

// arnMarker scopes the shared /tags root to FIS ARNs.
const arnMarker = ":fis:"

// depthItem is the path tail length that names a single resource by id.
const depthItem = 1

// Handler serves AWS FIS requests against a driver.
type Handler struct {
	fis driver.FIS
}

// New returns a FIS handler backed by d.
func New(d driver.FIS) *Handler {
	return &Handler{fis: d}
}

// Matches claims the FIS path shapes. The /experimentTemplates and /experiments
// trees are unique to FIS. The /tags root is shared with other restJson1
// services, so it is claimed only for FIS ARNs; a non-FIS ARN falls through.
func (*Handler) Matches(r *http.Request) bool {
	segs := splitPath(r.URL.Path)
	if len(segs) == 0 {
		return false
	}

	switch segs[0] {
	case rootTemplates, rootExperiments:
		return len(segs) <= depthItem+1
	case rootTags:
		return len(segs) >= 2 && strings.Contains(strings.Join(segs[1:], "/"), arnMarker)
	default:
		return false
	}
}

// ServeHTTP dispatches a FIS request on its path shape.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	segs := splitPath(r.URL.Path)
	if len(segs) == 0 {
		notFoundPath(w, r.URL.Path)

		return
	}

	switch segs[0] {
	case rootTemplates:
		h.serveTemplates(w, r, segs[1:])
	case rootExperiments:
		h.serveExperiments(w, r, segs[1:])
	case rootTags:
		h.serveTags(w, r, segs[1:])
	default:
		notFoundPath(w, r.URL.Path)
	}
}

// serveTemplates routes the /experimentTemplates tree.
func (h *Handler) serveTemplates(w http.ResponseWriter, r *http.Request, rest []string) {
	switch len(rest) {
	case 0:
		h.serveTemplateCollection(w, r)
	case depthItem:
		h.serveTemplateItem(w, r, rest[0])
	default:
		notFoundPath(w, r.URL.Path)
	}
}

// serveTemplateCollection routes GET /experimentTemplates (list) and
// POST /experimentTemplates (create).
func (h *Handler) serveTemplateCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.listExperimentTemplates(w, r)
	case http.MethodPost:
		h.createExperimentTemplate(w, r)
	default:
		methodNotAllowed(w)
	}
}

// serveTemplateItem routes the verb-keyed operations on a single template.
func (h *Handler) serveTemplateItem(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		h.getExperimentTemplate(w, r, id)
	case http.MethodPatch:
		h.updateExperimentTemplate(w, r, id)
	case http.MethodDelete:
		h.deleteExperimentTemplate(w, r, id)
	default:
		methodNotAllowed(w)
	}
}

// serveExperiments routes the /experiments tree.
func (h *Handler) serveExperiments(w http.ResponseWriter, r *http.Request, rest []string) {
	switch len(rest) {
	case 0:
		h.serveExperimentCollection(w, r)
	case depthItem:
		h.serveExperimentItem(w, r, rest[0])
	default:
		notFoundPath(w, r.URL.Path)
	}
}

// serveExperimentCollection routes GET /experiments (list) and POST /experiments
// (start).
func (h *Handler) serveExperimentCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.listExperiments(w, r)
	case http.MethodPost:
		h.startExperiment(w, r)
	default:
		methodNotAllowed(w)
	}
}

// serveExperimentItem routes the verb-keyed operations on a single experiment.
func (h *Handler) serveExperimentItem(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		h.getExperiment(w, r, id)
	case http.MethodDelete:
		h.stopExperiment(w, r, id)
	default:
		methodNotAllowed(w)
	}
}

// splitPath splits a decoded URL path into its non-empty segments.
func splitPath(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}

	return strings.Split(p, "/")
}
