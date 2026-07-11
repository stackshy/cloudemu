package keyvault

import (
	"net/http"

	cerrors "github.com/stackshy/cloudemu/errors"
	secretsdriver "github.com/stackshy/cloudemu/secrets/driver"
	"github.com/stackshy/cloudemu/server/wire"
)

// setSecret creates the secret on first PUT and adds a new version on
// subsequent PUTs, mirroring Key Vault's create-or-update SetSecret call.
func (h *Handler) setSecret(w http.ResponseWriter, r *http.Request, name string) {
	var req setSecretRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if _, err := h.secrets.GetSecret(r.Context(), name); err != nil {
		if !cerrors.IsNotFound(err) {
			writeCErr(w, err)
			return
		}

		h.createSecret(w, r, name, &req)

		return
	}

	ver, err := h.secrets.PutSecretValue(r.Context(), name, []byte(req.Value))
	if err != nil {
		writeCErr(w, err)
		return
	}

	h.writeBundle(w, r, name, ver)
}

func (h *Handler) createSecret(w http.ResponseWriter, r *http.Request, name string, req *setSecretRequest) {
	info, err := h.secrets.CreateSecret(r.Context(), secretsdriver.SecretConfig{
		Name: name,
		Tags: req.Tags,
	}, []byte(req.Value))
	if err != nil {
		writeCErr(w, err)
		return
	}

	ver, err := h.secrets.GetSecretValue(r.Context(), info.Name, "")
	if err != nil {
		writeCErr(w, err)
		return
	}

	wire.WriteJSON(w, toBundle(r, info, ver))
}

// writeBundle emits the full secret bundle for the given version.
func (h *Handler) writeBundle(w http.ResponseWriter, r *http.Request, name string, ver *secretsdriver.SecretVersion) {
	info, err := h.secrets.GetSecret(r.Context(), name)
	if err != nil {
		writeCErr(w, err)
		return
	}

	wire.WriteJSON(w, toBundle(r, info, ver))
}

func (h *Handler) getSecret(w http.ResponseWriter, r *http.Request, name, version string) {
	ver, err := h.secrets.GetSecretValue(r.Context(), name, version)
	if err != nil {
		writeCErr(w, err)
		return
	}

	h.writeBundle(w, r, name, ver)
}

func (h *Handler) deleteSecret(w http.ResponseWriter, r *http.Request, name string) {
	info, err := h.secrets.GetSecret(r.Context(), name)
	if err != nil {
		writeCErr(w, err)
		return
	}

	ver, err := h.secrets.GetSecretValue(r.Context(), name, "")
	if err != nil {
		writeCErr(w, err)
		return
	}

	if err := h.secrets.DeleteSecret(r.Context(), name); err != nil {
		writeCErr(w, err)
		return
	}

	wire.WriteJSON(w, deletedSecretBundleJSON{
		secretBundleJSON: toBundle(r, info, ver),
		RecoveryID:       vaultBaseURL(r) + "/deletedsecrets/" + info.Name,
	})
}

func (h *Handler) listSecrets(w http.ResponseWriter, r *http.Request) {
	infos, err := h.secrets.ListSecrets(r.Context())
	if err != nil {
		writeCErr(w, err)
		return
	}

	items := make([]secretItemJSON, 0, len(infos))
	for i := range infos {
		items = append(items, toItem(r, &infos[i]))
	}

	wire.WriteJSON(w, listResponseJSON{Value: items})
}

func (h *Handler) listSecretVersions(w http.ResponseWriter, r *http.Request, name string) {
	versions, err := h.secrets.ListSecretVersions(r.Context(), name)
	if err != nil {
		writeCErr(w, err)
		return
	}

	items := make([]secretItemJSON, 0, len(versions))
	for i := range versions {
		items = append(items, toVersionItem(r, name, &versions[i]))
	}

	wire.WriteJSON(w, listResponseJSON{Value: items})
}
