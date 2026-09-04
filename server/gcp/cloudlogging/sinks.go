package cloudlogging

import (
	"net/http"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	logdriver "github.com/stackshy/cloudemu/v2/services/logging/driver"
)

// logSinkJSON is the Cloud Logging LogSink resource shape.
type logSinkJSON struct {
	Name            string `json:"name,omitempty"`
	Destination     string `json:"destination,omitempty"`
	Filter          string `json:"filter,omitempty"`
	Description     string `json:"description,omitempty"`
	Disabled        bool   `json:"disabled,omitempty"`
	IncludeChildren bool   `json:"includeChildren,omitempty"`
	WriterIdentity  string `json:"writerIdentity,omitempty"`
	CreateTime      string `json:"createTime,omitempty"`
	UpdateTime      string `json:"updateTime,omitempty"`
}

type listSinksResponse struct {
	Sinks         []logSinkJSON `json:"sinks"`
	NextPageToken string        `json:"nextPageToken,omitempty"`
}

func (b *logSinkJSON) toDriver(name string) *logdriver.LogSink {
	return &logdriver.LogSink{
		Name:            name,
		Destination:     b.Destination,
		Filter:          b.Filter,
		Description:     b.Description,
		Disabled:        b.Disabled,
		IncludeChildren: b.IncludeChildren,
		WriterIdentity:  b.WriterIdentity,
	}
}

func toSinkJSON(s *logdriver.LogSink) logSinkJSON {
	out := logSinkJSON{
		Name:            s.Name,
		Destination:     s.Destination,
		Filter:          s.Filter,
		Description:     s.Description,
		Disabled:        s.Disabled,
		IncludeChildren: s.IncludeChildren,
		WriterIdentity:  s.WriterIdentity,
	}

	if !s.CreatedAt.IsZero() {
		out.CreateTime = s.CreatedAt.UTC().Format(time.RFC3339Nano)
	}

	if !s.UpdatedAt.IsZero() {
		out.UpdateTime = s.UpdatedAt.UTC().Format(time.RFC3339Nano)
	}

	return out
}

// writeSink writes a single sink or the driver error that produced it.
func writeSink(w http.ResponseWriter, s *logdriver.LogSink, err error) {
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toSinkJSON(s))
}

// gcpBackend returns the optional GCP-only logging surface, writing a 501 and
// returning false when the driver does not implement it.
func (h *Handler) gcpBackend(w http.ResponseWriter) (logdriver.GCPLogging, bool) {
	gcp, ok := h.logs.(logdriver.GCPLogging)
	if !ok {
		gcprest.WriteError(w, http.StatusNotImplemented, "notImplemented",
			"this logging backend does not support sinks or log-based metrics")
		return nil, false
	}

	return gcp, true
}

// routeSinks and routeMetrics are deliberately parallel: sinks and log-based
// metrics are sibling project-scoped REST collections with identical method
// routing but distinct resource types, so merging them would obscure the wire
// shapes rather than clarify them.
//
//nolint:dupl // parallel-by-design with routeMetrics; see comment above
func (h *Handler) routeSinks(w http.ResponseWriter, r *http.Request, tail string) {
	gcp, ok := h.gcpBackend(w)
	if !ok {
		return
	}

	project := projectFromPath(r.URL.Path)

	if tail == "/" {
		switch r.Method {
		case http.MethodPost:
			createSink(w, r, gcp, project)
		case http.MethodGet:
			listSinks(w, r, gcp, project)
		default:
			writeMethodNotAllowed(w)
		}

		return
	}

	sinkID := strings.TrimPrefix(tail, "/")

	switch r.Method {
	case http.MethodGet:
		s, err := gcp.GetSink(r.Context(), project, sinkID)
		writeSink(w, s, err)
	case http.MethodPut, http.MethodPatch:
		updateSink(w, r, gcp, project, sinkID)
	case http.MethodDelete:
		deleteResource(w, gcp.DeleteSink(r.Context(), project, sinkID))
	default:
		writeMethodNotAllowed(w)
	}
}

func createSink(w http.ResponseWriter, r *http.Request, gcp logdriver.GCPLogging, project string) {
	var body logSinkJSON
	if !gcprest.DecodeJSON(w, r, &body) {
		return
	}

	sink := body.toDriver(body.Name)

	// writerIdentity is output-only: its value is chosen by query params, never
	// the request body — a unique per-parent service agent
	// (uniqueWriterIdentity=true) or an explicit one (customWriterIdentity). When
	// neither is set this is "" and the provider fills the shared, non-unique
	// account.
	sink.WriterIdentity = sinkWriterIdentity(project, r)

	s, err := gcp.CreateSink(r.Context(), project, sink)
	writeSink(w, s, err)
}

// defaultSinkUpdateMask is the field set an updateMask-less sink PATCH touches,
// matching real Cloud Logging's documented backwards-compatible default.
const defaultSinkUpdateMask = "destination,filter,includeChildren"

func updateSink(w http.ResponseWriter, r *http.Request, gcp logdriver.GCPLogging, project, sinkID string) {
	var body logSinkJSON
	if !gcprest.DecodeJSON(w, r, &body) {
		return
	}

	mask := r.URL.Query().Get("updateMask")
	if mask == "" {
		mask = defaultSinkUpdateMask
	}

	update := &logdriver.SinkUpdate{
		Destination:        body.Destination,
		SetDestination:     maskHas(mask, "destination"),
		Filter:             body.Filter,
		SetFilter:          maskHas(mask, "filter"),
		Description:        body.Description,
		SetDescription:     maskHas(mask, "description"),
		Disabled:           body.Disabled,
		SetDisabled:        maskHas(mask, "disabled"),
		IncludeChildren:    body.IncludeChildren,
		SetIncludeChildren: maskHas(mask, "includeChildren") || maskHas(mask, "include_children"),
		WriterIdentity:     sinkWriterIdentity(project, r),
	}

	s, err := gcp.UpdateSink(r.Context(), project, sinkID, update)
	writeSink(w, s, err)
}

// sinkWriterIdentity resolves the writerIdentity a create/patch requests via
// query params. customWriterIdentity wins; uniqueWriterIdentity=true yields a
// per-parent Cloud Logging service agent; otherwise "" (the provider fills the
// shared non-unique account and a patch leaves the stored identity unchanged).
func sinkWriterIdentity(project string, r *http.Request) string {
	if custom := r.URL.Query().Get("customWriterIdentity"); custom != "" {
		return custom
	}

	if r.URL.Query().Get("uniqueWriterIdentity") == "true" {
		return uniqueWriterIdentityFor(project)
	}

	return ""
}

// uniqueWriterIdentityFor returns the deterministic per-parent Logging service
// agent used when a sink requests a unique writer identity. Real Cloud Logging
// keys this on the project number; the emulator has no project number, so it
// derives a stable, realistically-shaped agent from the project id. It differs
// from the shared non-unique account, which is what SDK/Terraform callers check
// to infer unique_writer_identity.
func uniqueWriterIdentityFor(project string) string {
	return "serviceAccount:service-" + project + "@gcp-sa-logging.iam.gserviceaccount.com"
}

func listSinks(w http.ResponseWriter, r *http.Request, gcp logdriver.GCPLogging, project string) {
	sinks, err := gcp.ListSinks(r.Context(), project)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	out := make([]logSinkJSON, 0, len(sinks))
	for i := range sinks {
		out = append(out, toSinkJSON(&sinks[i]))
	}

	gcprest.WriteJSON(w, http.StatusOK, listSinksResponse{Sinks: out})
}

// deleteResource writes the empty success body for a delete, or the driver error.
func deleteResource(w http.ResponseWriter, err error) {
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, struct{}{})
}
