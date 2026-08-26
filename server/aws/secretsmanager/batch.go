package secretsmanager

import (
	"net/http"
	"sort"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
)

// batchGetSecretValue retrieves the AWSCURRENT value of up to a page of secrets
// named by SecretIdList (or matched by Filters). A missing (or otherwise
// unreadable) secret is reported per-item in Errors rather than failing the
// whole batch, matching the real service.
func (h *Handler) batchGetSecretValue(w http.ResponseWriter, r *http.Request) {
	var req batchGetSecretValueRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	ids := req.SecretIDList
	if len(ids) == 0 && len(req.Filters) > 0 {
		matched, err := h.filteredSecretIDs(r, req.Filters)
		if err != nil {
			writeErr(w, err)
			return
		}

		ids = matched
	}

	start, end, next, err := pageWindow(req.NextToken, req.MaxResults, len(ids))
	if err != nil {
		writeErr(w, err)
		return
	}

	values := make([]secretValueEntry, 0, end-start)
	errs := make([]batchErrorEntry, 0)

	for _, id := range ids[start:end] {
		entry, apiErr := h.batchLookup(r, id)
		if apiErr != nil {
			errs = append(errs, *apiErr)
			continue
		}

		values = append(values, *entry)
	}

	wire.WriteJSON(w, batchGetSecretValueResponse{SecretValues: values, Errors: errs, NextToken: next})
}

// filteredSecretIDs returns the names of secrets matching the BatchGetSecretValue
// filters, in the stable (by-name) order the offset NextToken relies on.
func (h *Handler) filteredSecretIDs(r *http.Request, filters []secretFilter) ([]string, error) {
	infos, err := h.secrets.ListSecrets(r.Context())
	if err != nil {
		return nil, err
	}

	ids := make([]string, 0, len(infos))

	for i := range infos {
		if matchesSecretFilters(&infos[i], filters) {
			ids = append(ids, infos[i].Name)
		}
	}

	sort.Strings(ids)

	return ids, nil
}

// batchLookup resolves one secret id to its AWSCURRENT value, returning a
// per-item error entry (rather than a fatal error) when the secret is missing or
// unreadable.
func (h *Handler) batchLookup(r *http.Request, secretID string) (*secretValueEntry, *batchErrorEntry) {
	name := resolveSecretID(secretID)

	info, err := h.secrets.GetSecret(r.Context(), name)
	if err != nil {
		return nil, &batchErrorEntry{SecretID: secretID, ErrorCode: batchErrorCode(err), Message: err.Error()}
	}

	ver, err := h.getVersion(r, name, "", "")
	if err != nil {
		return nil, &batchErrorEntry{SecretID: secretID, ErrorCode: batchErrorCode(err), Message: err.Error()}
	}

	entry := secretValueEntry{
		ARN:           info.ResourceID,
		Name:          info.Name,
		VersionID:     ver.VersionID,
		VersionStages: stagesForVersion(h.versionStages(r, name), ver),
		CreatedDate:   epochSeconds(ver.CreatedAt),
	}

	if ver.Binary {
		entry.SecretBinary = ver.Value
	} else {
		entry.SecretString = string(ver.Value)
	}

	return &entry, nil
}

// batchErrorCode maps a canonical cloudemu error to the per-item ErrorCode
// BatchGetSecretValue reports.
func batchErrorCode(err error) string {
	switch {
	case cerrors.IsNotFound(err):
		return "ResourceNotFoundException"
	case cerrors.IsFailedPrecondition(err):
		return "InvalidRequestException"
	case cerrors.IsInvalidArgument(err):
		return "InvalidParameterException"
	default:
		return "InternalServiceError"
	}
}
