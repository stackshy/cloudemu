package secretsmanager

import (
	"context"
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
		Name:               req.Name,
		Description:        req.Description,
		Tags:               tagsToMap(req.Tags),
		KMSKeyID:           req.KmsKeyID,
		ClientRequestToken: req.ClientRequestToken,
		AddReplicaRegions:  replicaRegionsToDriver(req.AddReplicaRegions),
	}, secretValue(req.SecretString, req.SecretBinary))
	if err != nil {
		writeErr(w, err)
		return
	}

	// The driver seeds the initial version internally; look it up so the
	// response carries the VersionId real Secrets Manager returns.
	out := createSecretResponse{ARN: info.ResourceID, Name: info.Name}
	if len(info.ReplicationStatus) > 0 {
		out.ReplicationStatus = toReplicationStatusJSON(info.ReplicationStatus)
	}

	if versionID := h.currentVersionID(r, info.Name); versionID != "" {
		out.VersionID = versionID

		if isBinary(req.SecretString, req.SecretBinary) {
			if st, ok := h.secrets.(secretStager); ok {
				_ = st.MarkVersionBinary(r.Context(), info.Name, versionID)
			}
		}
	}

	wire.WriteJSON(w, out)
}

// currentVersionID returns the id of the version carrying AWSCURRENT ("" when
// there is none). With the AWS staging surface it reads the staging index, so
// CreateSecret's own lookup is not a retrieval of the value and does not stamp
// LastAccessedDate; other drivers fall back to a portable value read.
func (h *Handler) currentVersionID(r *http.Request, name string) string {
	if st, ok := h.secrets.(secretStager); ok {
		stages, err := st.SecretVersionStages(r.Context(), name)
		if err != nil {
			return ""
		}

		for id, labels := range stages {
			for _, l := range labels {
				if l == stageCurrent {
					return id
				}
			}
		}

		return ""
	}

	if ver, err := h.secrets.GetSecretValue(r.Context(), name, ""); err == nil {
		return ver.VersionID
	}

	return ""
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
	if len(info.ReplicationStatus) > 0 {
		out.ReplicationStatus = toReplicationStatusJSON(info.ReplicationStatus)
	}

	if isStager {
		if stages, serr := st.SecretVersionStages(r.Context(), name); serr == nil {
			out.VersionIDsToStages = stages
		}

		if date, deleted := st.SecretDeletionDate(r.Context(), name); deleted {
			out.DeletedDate = epochSeconds(date)
		}

		if rot, rerr := st.SecretRotationDetails(r.Context(), name); rerr == nil {
			applyRotationInfo(&out, rot)
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

	respondSecretRef(w, r, st.RestoreSecret)
}

// respondSecretRef decodes a SecretId request, runs op against the resolved
// secret name, and writes the shared {ARN, Name} envelope. It is the common
// shape of RestoreSecret and DeleteResourcePolicy.
func respondSecretRef(
	w http.ResponseWriter, r *http.Request,
	op func(ctx context.Context, name string) (*secretsdriver.SecretInfo, error),
) {
	var req secretIDRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	info, err := op(r.Context(), resolveSecretID(req.SecretID))
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, secretRefResponse{ARN: info.ResourceID, Name: info.Name})
}

func (h *Handler) rotateSecret(w http.ResponseWriter, r *http.Request) {
	st, ok := h.secrets.(secretStager)
	if !ok {
		writeErr(w, errNotSupported)
		return
	}

	var req rotateSecretRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	name := resolveSecretID(req.SecretID)

	info, err := h.secrets.GetSecret(r.Context(), name)
	if err != nil {
		writeErr(w, err)
		return
	}

	// RotateImmediately defaults to true, matching the real service: an absent
	// field runs the rotation now rather than only storing the schedule.
	rotateImmediately := true
	if req.RotateImmediately != nil {
		rotateImmediately = *req.RotateImmediately
	}

	ver, err := st.RotateSecret(r.Context(), name, req.RotationLambdaARN, req.RotationRules.toDriver(), rotateImmediately)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, rotateSecretResponse{ARN: info.ResourceID, Name: info.Name, VersionID: ver.VersionID})
}

// cancelRotateSecret turns off automatic rotation. The configured
// RotationLambdaARN/RotationRules are left in place, so a later RotateSecret
// can re-enable rotation without re-supplying them.
func (h *Handler) cancelRotateSecret(w http.ResponseWriter, r *http.Request) {
	st, ok := h.secrets.(secretStager)
	if !ok {
		writeErr(w, errNotSupported)
		return
	}

	var req secretIDRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	info, versionID, err := st.CancelRotateSecret(r.Context(), resolveSecretID(req.SecretID))
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, rotateSecretResponse{ARN: info.ResourceID, Name: info.Name, VersionID: versionID})
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

func (h *Handler) listSecrets(w http.ResponseWriter, r *http.Request) {
	var req listSecretsRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := validateListSecrets(&req); err != nil {
		writeValidation(w, err)
		return
	}

	infos, err := h.listSecretInfos(r, req.IncludePlannedDeletion)
	if err != nil {
		writeErr(w, err)
		return
	}

	filtered := infos[:0:0]

	for i := range infos {
		if matchesSecretFilters(&infos[i], req.Filters) {
			filtered = append(filtered, infos[i])
		}
	}

	page, next, err := listSecretsPage(filtered, &req)
	if err != nil {
		wire.WriteJSONError(w, http.StatusBadRequest, "InvalidNextTokenException", msgInvalidNextToken)
		return
	}

	entries := make([]secretListEntryJSON, 0, len(page))

	for i := range page {
		entries = append(entries, h.listEntry(r, &page[i]))
	}

	wire.WriteJSON(w, listSecretsResponse{SecretList: entries, NextToken: next})
}

// listEntry renders one ListSecrets entry: the secret's metadata plus, when
// the AWS staging surface is available, its rotation details and (for a
// secret scheduled for deletion, only listed with IncludePlannedDeletion) its
// DeletedDate, as DescribeSecret reports them.
func (h *Handler) listEntry(r *http.Request, info *secretsdriver.SecretInfo) secretListEntryJSON {
	entry := toSecretListEntry(info)

	st, ok := h.secrets.(secretStager)
	if !ok {
		return entry
	}

	if rot, err := st.SecretRotationDetails(r.Context(), info.Name); err == nil {
		applyRotationInfo(&entry, rot)
	}

	if date, deleted := st.SecretDeletionDate(r.Context(), info.Name); deleted {
		entry.DeletedDate = epochSeconds(date)
	}

	return entry
}

// listSecretInfos returns the secrets ListSecrets considers: active secrets,
// plus those scheduled for deletion when includePlannedDeletion is set and the
// driver supports it (the AWS provider does).
func (h *Handler) listSecretInfos(r *http.Request, includePlannedDeletion bool) ([]secretsdriver.SecretInfo, error) {
	if rep, ok := h.secrets.(secretReplicator); ok {
		return rep.ListSecretsWithOptions(r.Context(), includePlannedDeletion)
	}

	return h.secrets.ListSecrets(r.Context())
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

	// Real Secrets Manager requires exactly one of SecretString/SecretBinary;
	// PutSecretValue exists only to add a new version's content.
	if req.SecretString == "" && len(req.SecretBinary) == 0 {
		wire.WriteJSONError(w, http.StatusBadRequest,
			"InvalidParameterException", "You must provide a value for either SecretString or SecretBinary")

		return
	}

	name := resolveSecretID(req.SecretID)

	info, err := h.secrets.GetSecret(r.Context(), name)
	if err != nil {
		writeErr(w, err)
		return
	}

	ver, err := h.putSecretVersion(r, name, &req)
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

// putSecretVersion appends a new version. When the AWS staging surface is
// available it honors ClientRequestToken (idempotency + version id) and
// VersionStages; otherwise it falls back to the portable append-a-version path.
func (h *Handler) putSecretVersion(
	r *http.Request, name string, req *putSecretValueRequest,
) (*secretsdriver.SecretVersion, error) {
	value := secretValue(req.SecretString, req.SecretBinary)

	if st, ok := h.secrets.(secretStager); ok {
		return st.PutSecretValueStaged(r.Context(), name, value, req.ClientRequestToken, req.VersionStages)
	}

	return h.secrets.PutSecretValue(r.Context(), name, value)
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

	all := make([]versionJSON, 0, len(versions))

	for _, v := range versions {
		stages := stagesForVersion(stageMap, &v)
		if len(stages) == 0 && !req.IncludeDeprecated {
			continue
		}

		all = append(all, versionJSON{
			VersionID:     v.VersionID,
			VersionStages: stages,
			CreatedDate:   epochSeconds(v.CreatedAt),
		})
	}

	// Newest first, VersionId as the tiebreaker so the offset NextToken stays
	// stable across pages when two versions share a CreatedDate.
	sort.Slice(all, func(i, j int) bool {
		if all[i].CreatedDate != all[j].CreatedDate {
			return all[i].CreatedDate > all[j].CreatedDate
		}

		return all[i].VersionID < all[j].VersionID
	})

	start, end, next, err := pageWindow(req.NextToken, req.MaxResults, len(all))
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, listSecretVersionIDsResponse{
		ARN: info.ResourceID, Name: info.Name, Versions: all[start:end], NextToken: next,
	})
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

	info, versionID, err := mut.UpdateSecret(r.Context(), resolveSecretID(req.SecretID),
		req.Description, secretValue(req.SecretString, req.SecretBinary))
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, updateSecretResponse{ARN: info.ResourceID, Name: info.Name, VersionID: versionID})
}

// updateSecretVersionStage moves a staging label between versions (the
// finishSecret step of the AWS rotation contract). It requires the AWS staging
// surface; providers without it report the operation as unsupported.
func (h *Handler) updateSecretVersionStage(w http.ResponseWriter, r *http.Request) {
	st, ok := h.secrets.(secretStager)
	if !ok {
		writeErr(w, errNotSupported)
		return
	}

	var req updateSecretVersionStageRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	info, err := st.UpdateSecretVersionStage(r.Context(), resolveSecretID(req.SecretID),
		req.VersionStage, req.RemoveFromVersionID, req.MoveToVersionID)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, updateSecretVersionStageResponse{ARN: info.ResourceID, Name: info.Name})
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
