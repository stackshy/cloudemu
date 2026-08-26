package secretmanager

import (
	"net/http"
	"strconv"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/pagination"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	secretsdriver "github.com/stackshy/cloudemu/v2/services/secrets/driver"
)

// maskContains reports whether a field-update mask names the given field. An
// empty mask is treated as "update everything present in the body".
func maskContains(mask, field string) bool {
	if strings.TrimSpace(mask) == "" {
		return true
	}

	for _, f := range strings.Split(mask, ",") {
		if strings.TrimSpace(f) == field {
			return true
		}
	}

	return false
}

const (
	defaultPageSize = 100
	maxPageSize     = 25000
)

// pageSize reads ?pageSize, clamping to a sane default and ceiling.
func pageSize(r *http.Request) int {
	n, err := strconv.Atoi(r.URL.Query().Get("pageSize"))
	if err != nil || n <= 0 {
		return defaultPageSize
	}

	if n > maxPageSize {
		return maxPageSize
	}

	return n
}

// pageToken reads the opaque ?pageToken continuation cursor.
func pageToken(r *http.Request) string {
	return r.URL.Query().Get("pageToken")
}

// versionLess orders two numeric version ids ascending; non-numeric ids fall
// back to string order.
func versionLess(a, b string) bool {
	ai, aerr := strconv.Atoi(a)
	bi, berr := strconv.Atoi(b)

	if aerr == nil && berr == nil {
		return ai < bi
	}

	return a < b
}

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

	// Stable order by secret name so offset page tokens stay meaningful across
	// calls (ListSecrets iterates a map, which is unordered).
	page, err := pagination.PaginateSorted(infos,
		func(a, b secretsdriver.SecretInfo) bool { return a.Name < b.Name },
		pageToken(r), pageSize(r))
	if err != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "invalid pageToken")
		return
	}

	out := make([]secretJSON, 0, len(page.Items))
	for i := range page.Items {
		out = append(out, toSecretJSON(rt.project, &page.Items[i]))
	}

	gcprest.WriteJSON(w, http.StatusOK, listSecretsResponse{
		Secrets:       out,
		TotalSize:     len(infos),
		NextPageToken: page.NextPageToken,
	})
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

	// Real GCP lists versions newest-first (descending version number).
	page, err := pagination.PaginateSorted(versions,
		func(a, b secretsdriver.SecretVersion) bool { return versionLess(b.VersionID, a.VersionID) },
		pageToken(r), pageSize(r))
	if err != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "invalid pageToken")
		return
	}

	out := make([]versionResourceJSON, 0, len(page.Items))
	for i := range page.Items {
		out = append(out, toVersionJSON(rt.project, rt.secret, &page.Items[i]))
	}

	gcprest.WriteJSON(w, http.StatusOK, listVersionsResponse{
		Versions:      out,
		TotalSize:     len(versions),
		NextPageToken: page.NextPageToken,
	})
}

func (h *Handler) patchSecret(w http.ResponseWriter, r *http.Request, rt route) {
	if h.gcp == nil {
		writeUnsupported(w)
		return
	}

	var req patchSecretRequest
	if !gcprest.DecodeJSON(w, r, &req) {
		return
	}

	// The update mask names the fields to change; only labels are modeled.
	patch := secretsdriver.GCPSecretPatch{}
	if maskContains(r.URL.Query().Get("updateMask"), "labels") {
		patch.Labels = req.Labels
		patch.SetLabels = true
	}

	info, err := h.gcp.PatchSecret(r.Context(), rt.secret, patch)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toSecretJSON(rt.project, info))
}

// mutateVersion applies an enable/disable/destroy lifecycle verb to a version.
func (h *Handler) mutateVersion(w http.ResponseWriter, r *http.Request, rt route, verb string) {
	if h.gcp == nil {
		writeUnsupported(w)
		return
	}

	var (
		ver *secretsdriver.SecretVersion
		err error
	)

	switch verb {
	case verbEnable:
		ver, err = h.gcp.EnableSecretVersion(r.Context(), rt.secret, driverVersion(rt.version))
	case verbDisable:
		ver, err = h.gcp.DisableSecretVersion(r.Context(), rt.secret, driverVersion(rt.version))
	case verbDestroy:
		ver, err = h.gcp.DestroySecretVersion(r.Context(), rt.secret, driverVersion(rt.version))
	default:
		writeUnsupported(w)
		return
	}

	if err != nil {
		// An illegal state transition (e.g. destroy/disable/enable on a DESTROYED
		// version) is FAILED_PRECONDITION, which GCP reports as HTTP 400. The
		// shared gcprest mapping would turn it into 409, so answer 400 locally
		// without altering that cross-cutting mapping.
		if cerrors.IsFailedPrecondition(err) {
			gcprest.WriteError(w, http.StatusBadRequest, "failedPrecondition", err.Error())
			return
		}

		gcprest.WriteCErr(w, err)

		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toVersionJSON(rt.project, rt.secret, ver))
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

	// A version's payload can only be accessed in the ENABLED state; disabled
	// and destroyed versions fail with FAILED_PRECONDITION, matching real GCP.
	if ver.State != "" && ver.State != secretsdriver.VersionEnabled {
		gcprest.WriteError(w, http.StatusBadRequest, "failedPrecondition",
			"cannot access version "+ver.VersionID+" in state "+ver.State)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, accessResponse{
		Name:    versionName(rt.project, rt.secret, ver.VersionID),
		Payload: payloadJSON{Data: ver.Value},
	})
}
