package secretsmanager

import (
	"net/http"

	secretsdriver "github.com/stackshy/cloudemu/secrets/driver"
	"github.com/stackshy/cloudemu/server/wire"
)

func (h *Handler) createSecret(w http.ResponseWriter, r *http.Request) {
	var req createSecretRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	info, err := h.secrets.CreateSecret(r.Context(), secretsdriver.SecretConfig{
		Name:        req.Name,
		Description: req.Description,
		Tags:        tagsToMap(req.Tags),
	}, secretValue(req.SecretString, req.SecretBinary))
	if err != nil {
		writeErr(w, err)
		return
	}

	// The driver seeds the initial version internally; fetch it so the
	// response carries the VersionId real Secrets Manager returns.
	out := createSecretResponse{ARN: info.ResourceID, Name: info.Name}
	if ver, verr := h.secrets.GetSecretValue(r.Context(), info.Name, ""); verr == nil {
		out.VersionID = ver.VersionID
	}

	wire.WriteJSON(w, out)
}

func (h *Handler) deleteSecret(w http.ResponseWriter, r *http.Request) {
	var req secretIDRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	name := resolveSecretID(req.SecretID)

	// Secrets Manager echoes the deleted secret's ARN and name, so capture
	// them before removal.
	info, err := h.secrets.GetSecret(r.Context(), name)
	if err != nil {
		writeErr(w, err)
		return
	}

	if err := h.secrets.DeleteSecret(r.Context(), name); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, deleteSecretResponse{ARN: info.ResourceID, Name: info.Name})
}

func (h *Handler) describeSecret(w http.ResponseWriter, r *http.Request) {
	var req secretIDRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	info, err := h.secrets.GetSecret(r.Context(), resolveSecretID(req.SecretID))
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, toSecretListEntry(info))
}

func (h *Handler) listSecrets(w http.ResponseWriter, r *http.Request) {
	infos, err := h.secrets.ListSecrets(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}

	entries := make([]secretListEntryJSON, 0, len(infos))
	for i := range infos {
		entries = append(entries, toSecretListEntry(&infos[i]))
	}

	wire.WriteJSON(w, listSecretsResponse{SecretList: entries})
}

func (h *Handler) getSecretValue(w http.ResponseWriter, r *http.Request) {
	var req getSecretValueRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	name := resolveSecretID(req.SecretID)

	info, err := h.secrets.GetSecret(r.Context(), name)
	if err != nil {
		writeErr(w, err)
		return
	}

	ver, err := h.secrets.GetSecretValue(r.Context(), name, req.VersionID)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, getSecretValueResponse{
		ARN:           info.ResourceID,
		Name:          info.Name,
		VersionID:     ver.VersionID,
		SecretString:  string(ver.Value),
		VersionStages: stagesFor(ver.Current),
		CreatedDate:   epochSeconds(ver.CreatedAt),
	})
}

func (h *Handler) putSecretValue(w http.ResponseWriter, r *http.Request) {
	var req putSecretValueRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	name := resolveSecretID(req.SecretID)

	info, err := h.secrets.GetSecret(r.Context(), name)
	if err != nil {
		writeErr(w, err)
		return
	}

	ver, err := h.secrets.PutSecretValue(r.Context(), name, secretValue(req.SecretString, req.SecretBinary))
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, putSecretValueResponse{
		ARN:           info.ResourceID,
		Name:          info.Name,
		VersionID:     ver.VersionID,
		VersionStages: stagesFor(ver.Current),
	})
}

func (h *Handler) listSecretVersionIDs(w http.ResponseWriter, r *http.Request) {
	var req secretIDRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	name := resolveSecretID(req.SecretID)

	info, err := h.secrets.GetSecret(r.Context(), name)
	if err != nil {
		writeErr(w, err)
		return
	}

	versions, err := h.secrets.ListSecretVersions(r.Context(), name)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := make([]versionJSON, 0, len(versions))
	for _, v := range versions {
		out = append(out, versionJSON{
			VersionID:     v.VersionID,
			VersionStages: stagesFor(v.Current),
			CreatedDate:   epochSeconds(v.CreatedAt),
		})
	}

	wire.WriteJSON(w, listSecretVersionIDsResponse{ARN: info.ResourceID, Name: info.Name, Versions: out})
}
