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
	var req deleteSecretRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	name := resolveSecretID(req.SecretID)

	// The AWS provider honors RecoveryWindowInDays / ForceDeleteWithoutRecovery
	// and validates them; drivers without the stager surface fall back to a
	// plain soft delete.
	st, ok := h.secrets.(secretStager)
	if !ok {
		h.deleteSecretFallback(w, r, name)
		return
	}

	info, date, err := st.DeleteSecretWithOptions(r.Context(), name, req.RecoveryWindowInDays, req.ForceDeleteWithoutRecovery)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, deleteSecretResponse{ARN: info.ResourceID, Name: info.Name, DeletionDate: epochSeconds(date)})
}

// deleteSecretFallback handles DeleteSecret for drivers that don't implement the
// AWS stager surface (Azure/GCP), using the portable soft delete.
func (h *Handler) deleteSecretFallback(w http.ResponseWriter, r *http.Request, name string) {
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

	name := resolveSecretID(req.SecretID)

	// DescribeSecret keeps working for a secret scheduled for deletion (returning
	// a DeletedDate); only a missing secret is ResourceNotFoundException. Use the
	// stager's metadata read, which does not error on the soft-deleted state.
	st, isStager := h.secrets.(secretStager)

	var (
		info *secretsdriver.SecretInfo
		err  error
	)

	if isStager {
		info, err = st.SecretMetadata(r.Context(), name)
	} else {
		info, err = h.secrets.GetSecret(r.Context(), name)
	}

	if err != nil {
		writeErr(w, err)
		return
	}

	out := toSecretListEntry(info)

	if isStager {
		if stages, serr := st.SecretVersionStages(r.Context(), name); serr == nil {
			out.VersionIDsToStages = stages
		}

		if date, deleted := st.SecretDeletionDate(r.Context(), name); deleted {
			out.DeletedDate = epochSeconds(date)
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
		VersionStages: stagesForVersion(h.versionStages(r, name), ver),
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

// versionStages returns the per-version staging labels for a secret when the
// AWS staging surface is available, so callers report the exact AWSCURRENT/
// AWSPREVIOUS assignment (and no label at all for deprecated versions). It
// returns nil for providers without the staging surface, letting callers fall
// back to the coarse current/previous heuristic.
func (h *Handler) versionStages(r *http.Request, name string) map[string][]string {
	if st, ok := h.secrets.(secretStager); ok {
		if m, err := st.SecretVersionStages(r.Context(), name); err == nil {
			return m
		}
	}

	return nil
}

// stagesForVersion resolves the staging labels for one version, preferring the
// exact per-version map when present and falling back to the current/previous
// heuristic otherwise.
func stagesForVersion(m map[string][]string, ver *secretsdriver.SecretVersion) []string {
	if m != nil {
		return m[ver.VersionID]
	}

	return stagesFor(ver.Current)
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
		VersionStages: stagesForVersion(h.versionStages(r, name), ver),
	})
}

func (h *Handler) listSecretVersionIDs(w http.ResponseWriter, r *http.Request) {
	var req listSecretVersionIDsRequest
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

	// Only the current version carries AWSCURRENT and only the immediately
	// superseded one carries AWSPREVIOUS; older versions are deprecated (no
	// staging labels) and are omitted unless IncludeDeprecated is set.
	stageMap := h.versionStages(r, name)

	out := make([]versionJSON, 0, len(versions))
	for _, v := range versions {
		stages := stagesForVersion(stageMap, &v)
		if len(stages) == 0 && !req.IncludeDeprecated {
			continue
		}

		out = append(out, versionJSON{
			VersionID:     v.VersionID,
			VersionStages: stages,
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
