package composer

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	composer "google.golang.org/api/composer/v1"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	cdriver "github.com/stackshy/cloudemu/v2/services/composer/driver"
)

// maxBodyBytes caps a decoded request body, matching the guard the shared
// gcprest decoder applies.
const maxBodyBytes = 8 << 20

// operationJSON mirrors google.longrunning.Operation. Mutating ops complete
// inline, so `done` is always true; `response` carries the resulting resource
// (an environment Any for create/update, absent for delete).
type operationJSON struct {
	Name     string          `json:"name"`
	Done     bool            `json:"done"`
	Response json.RawMessage `json:"response,omitempty"`
}

// rawEnvelope captures the request body's raw config alongside the identifier
// fields, so unmodeled config sub-blocks can be preserved verbatim.
type rawEnvelope struct {
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels"`
	Config json.RawMessage   `json:"config"`
}

// decodeEnvironment reads the request body once and decodes it both as a typed
// composer.Environment (for the modeled fields) and as a rawEnvelope (for the
// verbatim config passthrough and the name).
func decodeEnvironment(w http.ResponseWriter, r *http.Request) (composer.Environment, rawEnvelope, bool) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
	if err != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "reading request body: "+err.Error())
		return composer.Environment{}, rawEnvelope{}, false
	}

	var (
		typed composer.Environment
		raw   rawEnvelope
	)

	if len(body) > 0 {
		if err := json.Unmarshal(body, &typed); err != nil {
			gcprest.WriteError(w, http.StatusBadRequest, "invalid", "malformed JSON body: "+err.Error())
			return composer.Environment{}, rawEnvelope{}, false
		}

		_ = json.Unmarshal(body, &raw)
	}

	return typed, raw, true
}

// createEnvironment handles POST .../environments — Create. The environmentId is
// the trailing segment of the body's environment.name (Composer has no id query
// param); the operation completes inline, so a done=true Operation carrying the
// new environment is returned.
func (h *Handler) createEnvironment(w http.ResponseWriter, r *http.Request, rt route) {
	typed, raw, ok := decodeEnvironment(w, r)
	if !ok {
		return
	}

	id := environmentIDFromName(raw.Name)
	if id == "" {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid",
			"environment.name is required and must end in the environment id")
		return
	}

	cfg := cdriver.CreateEnvironmentConfig{
		Project:       rt.project,
		Location:      rt.location,
		EnvironmentID: id,
		Labels:        raw.Labels,
		Config:        fromWireConfig(typed.Config, raw.Config),
	}

	env, op, err := h.db.CreateEnvironment(r.Context(), &cfg)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	h.writeEnvOperation(w, op, env)
}

// getEnvironment handles GET .../environments/{e} — Get.
func (h *Handler) getEnvironment(w http.ResponseWriter, r *http.Request, rt route) {
	env, err := h.db.GetEnvironment(r.Context(), rt.project, rt.location, rt.name)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	writeEnvironment(w, env)
}

// listEnvironments handles GET .../environments — List, scoped to the request's
// project+location and ordered by resource name.
func (h *Handler) listEnvironments(w http.ResponseWriter, r *http.Request, rt route) {
	envs, err := h.db.ListEnvironments(r.Context(), rt.project, rt.location)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	items := make([]json.RawMessage, 0, len(envs))

	for i := range envs {
		raw, mErr := toEnvironmentJSON(&envs[i])
		if mErr != nil {
			gcprest.WriteError(w, http.StatusInternalServerError, "internalError", mErr.Error())
			return
		}

		items = append(items, raw)
	}

	gcprest.WriteJSON(w, http.StatusOK, map[string]any{"environments": items})
}

// patchEnvironment handles PATCH .../environments/{e}?updateMask=... — Update.
// Only the masked fields mutate; a field outside the mask is left untouched.
func (h *Handler) patchEnvironment(w http.ResponseWriter, r *http.Request, rt route) {
	typed, raw, ok := decodeEnvironment(w, r)
	if !ok {
		return
	}

	mask := normalizeMask(r.URL.Query().Get("updateMask"))
	desired := fromWireConfig(typed.Config, raw.Config)

	env, op, err := h.db.UpdateEnvironment(r.Context(), rt.project, rt.location, rt.name, &desired, raw.Labels, mask)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	h.writeEnvOperation(w, op, env)
}

// deleteEnvironment handles DELETE .../environments/{e} — Delete. The operation
// completes inline, so a done=true Operation with no response is returned.
func (h *Handler) deleteEnvironment(w http.ResponseWriter, r *http.Request, rt route) {
	op, err := h.db.DeleteEnvironment(r.Context(), rt.project, rt.location, rt.name)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, h.doneOperation(op.Name, nil))
}

// writeEnvironment renders a driver environment as composer/v1 wire JSON.
func writeEnvironment(w http.ResponseWriter, env *cdriver.Environment) {
	raw, err := toEnvironmentJSON(env)
	if err != nil {
		gcprest.WriteError(w, http.StatusInternalServerError, "internalError", err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}

// writeEnvOperation writes a completed operation carrying the environment as its
// Any-typed response (create/update).
func (h *Handler) writeEnvOperation(w http.ResponseWriter, op *cdriver.Operation, env *cdriver.Environment) {
	raw, err := toEnvironmentJSON(env)
	if err != nil {
		gcprest.WriteError(w, http.StatusInternalServerError, "internalError", err.Error())
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, h.doneOperation(op.Name, environmentResponseAny(raw)))
}

// doneOperation builds a completed google.longrunning.Operation for the given
// operation name and records it with the shared LRO poller (a no-op on a nil
// registry) so a client polling the returned name resolves the same done
// operation (with its response) in the full server.
func (h *Handler) doneOperation(name string, resp json.RawMessage) operationJSON {
	h.ops.Register(name, resp)

	return operationJSON{Name: name, Done: true, Response: resp}
}

// operationResponse re-fetches the environment an operation acted on so a
// standalone poll can replay it. A delete operation (or an environment since
// removed) yields nil.
func (h *Handler) operationResponse(r *http.Request, op *cdriver.Operation) json.RawMessage {
	if op.Type == "delete" || op.TargetName == "" {
		return nil
	}

	project, location, id, ok := parseEnvironmentName(op.TargetName)
	if !ok {
		return nil
	}

	env, err := h.db.GetEnvironment(r.Context(), project, location, id)
	if err != nil {
		return nil
	}

	raw, err := toEnvironmentJSON(env)
	if err != nil {
		return nil
	}

	return environmentResponseAny(raw)
}

// environmentIDFromName extracts the environment id (the trailing path segment)
// from the body's environment.name. A bare id (no slash) is returned as-is.
func environmentIDFromName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}

	if i := strings.LastIndex(name, "/"); i >= 0 {
		return name[i+1:]
	}

	return name
}

// parseEnvironmentName splits projects/{p}/locations/{l}/environments/{e} into
// its parts.
func parseEnvironmentName(full string) (project, location, id string, ok bool) {
	const (
		wantLen   = 6
		iProject  = 1
		iLocation = 3
		iID       = 5
	)

	p := strings.Split(full, "/")
	if len(p) != wantLen || p[0] != projectsSeg || p[2] != locationsSeg || p[4] != environmentsSeg {
		return "", "", "", false
	}

	return p[iProject], p[iLocation], p[iID], true
}

// normalizeMask splits a comma-separated updateMask into trimmed, lowercase
// paths so "config.softwareConfig.imageVersion" (and stray whitespace) match the
// driver's field tokens.
func normalizeMask(mask string) []string {
	if mask == "" {
		return nil
	}

	parts := strings.Split(mask, ",")
	out := make([]string, 0, len(parts))

	for _, p := range parts {
		tok := strings.ToLower(strings.TrimSpace(p))
		if tok != "" {
			out = append(out, tok)
		}
	}

	return out
}

// doneOperation (package-level, standalone poll) builds a completed operation
// with the given response for a nil-registry server's own /operations poll.
func doneOperation(name string, resp json.RawMessage) operationJSON {
	return operationJSON{Name: name, Done: true, Response: resp}
}
