package dataform

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	dfdriver "github.com/stackshy/cloudemu/v2/services/dataform/driver"
)

// maxBodyBytes caps a decoded request body.
const maxBodyBytes = 8 << 20

// repoResourceName rebuilds a repository's full resource name; it must match the
// driver's stable name.
func repoResourceName(project, location, id string) string {
	return "projects/" + project + "/locations/" + location + "/" + repositoriesSeg + "/" + id
}

// repositoryBody is the wire shape of a repository create/patch request. The rich
// nested blocks gitRemoteSettings and workspaceCompilationOverrides are captured
// as raw JSON so they round-trip verbatim without the handler modeling every
// sub-field.
type repositoryBody struct {
	Name                                   string            `json:"name"`
	DisplayName                            string            `json:"displayName"`
	Labels                                 map[string]string `json:"labels"`
	ServiceAccount                         string            `json:"serviceAccount"`
	KmsKeyName                             string            `json:"kmsKeyName"`
	NpmrcEnvironmentVariablesSecretVersion string            `json:"npmrcEnvironmentVariablesSecretVersion"`
	GitRemoteSettings                      json.RawMessage   `json:"gitRemoteSettings"`
	WorkspaceCompilationOverrides          json.RawMessage   `json:"workspaceCompilationOverrides"`
}

// toConfig maps a decoded body to a driver config for the given identity.
func (b *repositoryBody) toConfig(project, location, id string) *dfdriver.RepositoryConfig {
	return &dfdriver.RepositoryConfig{
		Project: project, Location: location, ID: id,
		DisplayName:                            b.DisplayName,
		Labels:                                 b.Labels,
		ServiceAccount:                         b.ServiceAccount,
		KmsKeyName:                             b.KmsKeyName,
		NpmrcEnvironmentVariablesSecretVersion: b.NpmrcEnvironmentVariablesSecretVersion,
		GitRemoteSettings:                      b.GitRemoteSettings,
		WorkspaceCompilationOverrides:          b.WorkspaceCompilationOverrides,
	}
}

// decodeBody reads and unmarshals the request body into v, tolerating an empty
// body. It writes a 400 and returns false on a malformed body.
func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
	if err != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "reading request body: "+err.Error())
		return false
	}

	if len(raw) == 0 {
		return true
	}

	if err := json.Unmarshal(raw, v); err != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "malformed JSON body: "+err.Error())
		return false
	}

	return true
}

func (h *Handler) createRepository(w http.ResponseWriter, r *http.Request, rt *route) {
	var body repositoryBody
	if !decodeBody(w, r, &body) {
		return
	}

	id := r.URL.Query().Get("repositoryId")
	if id == "" {
		id = lastSegment(body.Name)
	}

	repo, err := h.db.CreateRepository(r.Context(), body.toConfig(rt.project, rt.location, id))
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, repositoryJSON(repo))
}

func (h *Handler) getRepository(w http.ResponseWriter, r *http.Request, rt *route) {
	repo, err := h.db.GetRepository(r.Context(), rt.project, rt.location, rt.repo)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, repositoryJSON(repo))
}

func (h *Handler) listRepositories(w http.ResponseWriter, r *http.Request, rt *route) {
	all, err := h.db.ListRepositories(r.Context(), rt.project, rt.location)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	items := make([]map[string]any, 0, len(all))
	for i := range all {
		items = append(items, repositoryJSON(&all[i]))
	}

	gcprest.WriteJSON(w, http.StatusOK, map[string]any{"repositories": items})
}

func (h *Handler) patchRepository(w http.ResponseWriter, r *http.Request, rt *route) {
	var body repositoryBody
	if !decodeBody(w, r, &body) {
		return
	}

	repo, err := h.db.PatchRepository(
		r.Context(),
		body.toConfig(rt.project, rt.location, rt.repo),
		parseMask(r.URL.Query().Get("updateMask")),
	)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, repositoryJSON(repo))
}

func (h *Handler) deleteRepository(w http.ResponseWriter, r *http.Request, rt *route) {
	if err := h.db.DeleteRepository(r.Context(), rt.project, rt.location, rt.repo); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, map[string]any{})
}

// repositoryJSON renders a repository as dataform/v1beta1 wire JSON. Zero-value
// fields are omitted, matching the real API, so an unset field never drifts
// against a Terraform config that also leaves it unset. The rich nested blocks
// are re-emitted as the raw JSON the client sent.
func repositoryJSON(repo *dfdriver.Repository) map[string]any {
	m := map[string]any{"name": repoResourceName(repo.Project, repo.Location, repo.ID)}

	if repo.CreateTime != "" {
		m["createTime"] = repo.CreateTime
	}

	if repo.DisplayName != "" {
		m["displayName"] = repo.DisplayName
	}

	if len(repo.Labels) > 0 {
		m["labels"] = repo.Labels
	}

	if repo.ServiceAccount != "" {
		m["serviceAccount"] = repo.ServiceAccount
	}

	if repo.KmsKeyName != "" {
		m["kmsKeyName"] = repo.KmsKeyName
	}

	if repo.NpmrcEnvironmentVariablesSecretVersion != "" {
		m["npmrcEnvironmentVariablesSecretVersion"] = repo.NpmrcEnvironmentVariablesSecretVersion
	}

	if len(repo.GitRemoteSettings) > 0 {
		m["gitRemoteSettings"] = repo.GitRemoteSettings
	}

	if len(repo.WorkspaceCompilationOverrides) > 0 {
		m["workspaceCompilationOverrides"] = repo.WorkspaceCompilationOverrides
	}

	return m
}

// parseMask splits a comma-separated updateMask query param into field paths.
func parseMask(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))

	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}

	return out
}

// lastSegment returns the trailing path segment of a resource name.
func lastSegment(name string) string {
	if i := strings.LastIndex(name, "/"); i >= 0 {
		return name[i+1:]
	}

	return name
}
