package secretmanager

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	secretsdriver "github.com/stackshy/cloudemu/v2/services/secrets/driver"
)

func (h *Handler) createSecret(w http.ResponseWriter, r *http.Request, rt route) {
	var req createSecretRequest
	if !gcprest.DecodeJSON(w, r, &req) {
		return
	}

	id := r.URL.Query().Get("secretId")

	info, err := h.secrets.CreateSecret(r.Context(), secretsdriver.SecretConfig{
		Name: id,
		Tags: req.Labels,
	}, nil)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toSecretJSON(rt.project, info))
}

func (h *Handler) getSecret(w http.ResponseWriter, r *http.Request, rt route) {
	info, err := h.secrets.GetSecret(r.Context(), rt.secret)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toSecretJSON(rt.project, info))
}

func (h *Handler) listSecrets(w http.ResponseWriter, r *http.Request, rt route) {
	infos, err := h.secrets.ListSecrets(r.Context())
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	out := make([]secretJSON, 0, len(infos))
	for i := range infos {
		out = append(out, toSecretJSON(rt.project, &infos[i]))
	}

	gcprest.WriteJSON(w, http.StatusOK, listSecretsResponse{Secrets: out, TotalSize: len(out)})
}

func (h *Handler) deleteSecret(w http.ResponseWriter, r *http.Request, rt route) {
	if err := h.secrets.DeleteSecret(r.Context(), rt.secret); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	// google.protobuf.Empty.
	gcprest.WriteJSON(w, http.StatusOK, struct{}{})
}

func (h *Handler) addVersion(w http.ResponseWriter, r *http.Request, rt route) {
	var req addVersionRequest
	if !gcprest.DecodeJSON(w, r, &req) {
		return
	}

	ver, err := h.secrets.PutSecretValue(r.Context(), rt.secret, req.Payload.Data)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toVersionJSON(rt.project, rt.secret, ver))
}

func (h *Handler) listVersions(w http.ResponseWriter, r *http.Request, rt route) {
	versions, err := h.secrets.ListSecretVersions(r.Context(), rt.secret)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	out := make([]versionResourceJSON, 0, len(versions))
	for i := range versions {
		out = append(out, toVersionJSON(rt.project, rt.secret, &versions[i]))
	}

	gcprest.WriteJSON(w, http.StatusOK, listVersionsResponse{Versions: out, TotalSize: len(out)})
}

func (h *Handler) getVersion(w http.ResponseWriter, r *http.Request, rt route) {
	ver, err := h.secrets.GetSecretValue(r.Context(), rt.secret, driverVersion(rt.version))
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toVersionJSON(rt.project, rt.secret, ver))
}

func (h *Handler) accessVersion(w http.ResponseWriter, r *http.Request, rt route) {
	ver, err := h.secrets.GetSecretValue(r.Context(), rt.secret, driverVersion(rt.version))
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, accessResponse{
		Name:    versionName(rt.project, rt.secret, ver.VersionID),
		Payload: payloadJSON{Data: ver.Value},
	})
}
