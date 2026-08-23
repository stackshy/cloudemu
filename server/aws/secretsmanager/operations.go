package secretsmanager

import (
	"net/http"
	"sort"

	"github.com/stackshy/cloudemu/v2/server/wire"
	secretsdriver "github.com/stackshy/cloudemu/v2/services/secrets/driver"
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

		if isBinary(req.SecretString, req.SecretBinary) {
			if st, ok := h.secrets.(secretStager); ok {
				_ = st.MarkVersionBinary(r.Context(), info.Name, ver.VersionID)
			}
		}
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

	out := deleteSecretResponse{ARN: info.ResourceID, Name: info.Name}

	if st, ok := h.secrets.(secretStager); ok {
		if date, deleted := st.SecretDeletionDate(r.Context(), name); deleted {
			out.DeletionDate = epochSeconds(date)
		}
	}

	wire.WriteJSON(w, out)
}

func (h *Handler) describeSecret(w http.ResponseWriter, r *http.Request) {
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

	out := toSecretListEntry(info)

	if st, ok := h.secrets.(secretStager); ok {
		if stages, serr := st.SecretVersionStages(r.Context(), name); serr == nil {
			out.VersionIDsToStages = stages
		}
	}

	wire.WriteJSON(w, out)
}

func (h *Handler) restoreSecret(w http.ResponseWriter, r *http.Request) {
	st, ok := h.secrets.(secretStager)
	if !ok {
		writeErr(w, errNotSupported)
		return
	}

	var req secretIDRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	info, err := st.RestoreSecret(r.Context(), resolveSecretID(req.SecretID))
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, restoreSecretResponse{ARN: info.ResourceID, Name: info.Name})
}

func (h *Handler) rotateSecret(w http.ResponseWriter, r *http.Request) {
	st, ok := h.secrets.(secretStager)
	if !ok {
		writeErr(w, errNotSupported)
		return
	}

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

	ver, err := st.RotateSecret(r.Context(), name)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, rotateSecretResponse{ARN: info.ResourceID, Name: info.Name, VersionID: ver.VersionID})
}

func (*Handler) getRandomPassword(w http.ResponseWriter, r *http.Request) {
	var req getRandomPasswordRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	pw, err := randomPassword(req)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, getRandomPasswordResponse{RandomPassword: pw})
}

// getResourcePolicy returns the secret's resource policy. None is modeled, so
// ResourcePolicy is absent — which the aws_secretsmanager_secret resource reads
// as "no policy". Without the operation the read fails outright.
func (h *Handler) getResourcePolicy(w http.ResponseWriter, r *http.Request) {
	var req secretIDRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	info, err := h.secrets.GetSecret(r.Context(), resolveSecretID(req.SecretID))
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{"ARN": info.ResourceID, "Name": info.Name})
}

func (h *Handler) listSecrets(w http.ResponseWriter, r *http.Request) {
	var req listSecretsRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	infos, err := h.secrets.ListSecrets(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}

	// A stable order (by name) keeps the offset-based NextToken valid across pages.
	sort.Slice(infos, func(i, j int) bool { return infos[i].Name < infos[j].Name })

	filtered := infos[:0:0]

	for i := range infos {
		if matchesSecretFilters(&infos[i], req.Filters) {
			filtered = append(filtered, infos[i])
		}
	}

	start, end, next, err := pageWindow(req.NextToken, req.MaxResults, len(filtered))
	if err != nil {
		writeErr(w, err)
		return
	}

	entries := make([]secretListEntryJSON, 0, end-start)
	for i := start; i < end; i++ {
		entries = append(entries, toSecretListEntry(&filtered[i]))
	}

	wire.WriteJSON(w, listSecretsResponse{SecretList: entries, NextToken: next})
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

	ver, err := h.getVersion(r, name, req.VersionID, req.VersionStage)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := getSecretValueResponse{
		ARN:           info.ResourceID,
		Name:          info.Name,
		VersionID:     ver.VersionID,
		VersionStages: stagesFor(ver.Current),
		CreatedDate:   epochSeconds(ver.CreatedAt),
	}

	// Binary secrets round-trip through SecretBinary; string secrets through
	// SecretString. AWS keeps the two fields mutually exclusive.
	if ver.Binary {
		out.SecretBinary = ver.Value
	} else {
		out.SecretString = string(ver.Value)
	}

	wire.WriteJSON(w, out)
}

// getVersion resolves a secret version by ID or stage, using the AWS staging
// surface when available (so AWSPREVIOUS and binary flags resolve) and falling
// back to the portable driver otherwise.
func (h *Handler) getVersion(
	r *http.Request, name, versionID, stage string,
) (*secretsdriver.SecretVersion, error) {
	if st, ok := h.secrets.(secretStager); ok {
		return st.GetSecretValueStage(r.Context(), name, versionID, stage)
	}

	return h.secrets.GetSecretValue(r.Context(), name, versionID)
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

	if isBinary(req.SecretString, req.SecretBinary) {
		if st, ok := h.secrets.(secretStager); ok {
			_ = st.MarkVersionBinary(r.Context(), name, ver.VersionID)
		}
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

func (h *Handler) updateSecret(w http.ResponseWriter, r *http.Request) {
	mut, ok := h.secrets.(secretMutator)
	if !ok {
		writeErr(w, errNotSupported)
		return
	}

	var req updateSecretRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	info, err := mut.UpdateSecret(r.Context(), resolveSecretID(req.SecretID),
		req.Description, secretValue(req.SecretString, req.SecretBinary))
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, updateSecretResponse{ARN: info.ResourceID, Name: info.Name})
}

func (h *Handler) tagResource(w http.ResponseWriter, r *http.Request) {
	mut, ok := h.secrets.(secretMutator)
	if !ok {
		writeErr(w, errNotSupported)
		return
	}

	var req tagResourceRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := mut.TagSecret(r.Context(), resolveSecretID(req.SecretID), tagsToMap(req.Tags)); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, struct{}{})
}

func (h *Handler) untagResource(w http.ResponseWriter, r *http.Request) {
	mut, ok := h.secrets.(secretMutator)
	if !ok {
		writeErr(w, errNotSupported)
		return
	}

	var req untagResourceRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := mut.UntagSecret(r.Context(), resolveSecretID(req.SecretID), req.TagKeys); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, struct{}{})
}
